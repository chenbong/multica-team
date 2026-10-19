package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	skillpkg "github.com/multica-ai/multica/server/internal/skill"
)

var oneToolCopiedLink = regexp.MustCompile(`(?i)^下载并安装此\s*skill\s*[:：]?\s+(https://\S+)$`)
var oneToolPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var oneToolID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var oneToolPermanentKey = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func normalizeOneToolImportInput(raw string) string {
	raw = strings.TrimSpace(raw)
	if match := oneToolCopiedLink.FindStringSubmatch(raw); match != nil {
		return match[1]
	}
	return raw
}

// Detail pages require authorization. Anonymous imports use only the exact
// published package endpoints; this is not a generic server-side URL fetcher.
func oneToolDownloadURL(raw string) (*url.URL, error) {
	u, err := url.Parse(normalizeOneToolImportInput(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" ||
		(u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("use a plain HTTPS OneTool ZIP download link without credentials or query parameters")
	}
	if !strings.EqualFold(u.Hostname(), "mcp-proxy.baidu-int.com") {
		return nil, errors.New("use a OneTool package download link, not a skill detail page")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 7 || strings.Join(parts[:4], "/") != "api/v1/skills/download" ||
		!strings.HasSuffix(strings.ToLower(parts[6]), ".zip") || !oneToolPart.MatchString(parts[6]) {
		return nil, errors.New("invalid OneTool ZIP download path")
	}
	if parts[4] == "permanent" {
		if !oneToolPermanentKey.MatchString(parts[5]) {
			return nil, errors.New("invalid OneTool permanent download key")
		}
	} else {
		id, err := strconv.ParseInt(parts[4], 10, 64)
		if err != nil || id <= 0 || !oneToolID.MatchString(parts[4]) || !oneToolPart.MatchString(parts[5]) {
			return nil, errors.New("invalid OneTool skill ID or version")
		}
	}
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

func fetchFromOneTool(ctx context.Context, supplied *http.Client, raw string) (*importedSkill, error) {
	u, err := oneToolDownloadURL(raw)
	if err != nil {
		return nil, err
	}
	// Never reuse a browser session, cookie jar, account credential, or incoming
	// request header. Validate each redirect before another request is sent.
	client := &http.Client{Transport: supplied.Transport, Timeout: supplied.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("OneTool download redirected too many times")
			}
			_, err := oneToolDownloadURL(req.URL.String())
			return err
		}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("invalid OneTool download request")
	}
	req.Header.Set("Accept", "application/zip, application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Transport errors may contain proxy credentials or redirect URLs.
		return nil, errors.New("OneTool package download failed; check the download link and server connectivity")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("OneTool denied anonymous download; download the ZIP through OneTool and use local archive import")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OneTool package download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxImportArchiveUploadSize {
		return nil, fmt.Errorf("%w: OneTool ZIP exceeds the 16 MiB download limit", errImportCapExceeded)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImportArchiveUploadSize+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("OneTool ZIP download was interrupted")
	}
	if len(data) > maxImportArchiveUploadSize {
		return nil, fmt.Errorf("%w: OneTool ZIP exceeds the 16 MiB download limit", errImportCapExceeded)
	}
	if err := validateOneToolArchive(data); err != nil {
		return nil, err
	}
	imported, err := parseSkillArchive(data, path.Base(u.Path))
	if err != nil {
		return nil, errors.New("OneTool package is not a valid skill ZIP containing SKILL.md")
	}
	if strings.TrimSpace(imported.content) == "" {
		return nil, errors.New("OneTool package contains an empty SKILL.md")
	}
	imported.origin = map[string]any{"type": "onetool", "source_url": u.String()}
	return imported, nil
}

func validateOneToolArchive(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errors.New("OneTool download is not a valid ZIP; use the package download link rather than a web page")
	}
	if len(zr.File) > 1024 {
		return fmt.Errorf("%w: OneTool ZIP has too many entries", errImportCapExceeded)
	}
	seen := make(map[string]bool)
	var total uint64
	for _, file := range zr.File {
		original := strings.ReplaceAll(file.Name, "\\", "/")
		clean := cleanArchiveEntryName(file.Name)
		if strings.HasPrefix(original, "/") || !validateArchiveFilePath(clean) ||
			strings.Contains("/"+original+"/", "/../") || file.Mode()&os.ModeSymlink != 0 {
			return errors.New("OneTool ZIP contains an unsafe entry")
		}
		if file.FileInfo().IsDir() || strings.HasSuffix(original, "/") {
			continue
		}
		if !file.Mode().IsRegular() || seen[clean] {
			return errors.New("OneTool ZIP contains duplicate or non-regular files")
		}
		seen[clean] = true
		if isIgnoredArchiveEntry(clean) || skillpkg.IsLikelyBinaryFilePath(clean) {
			continue
		}
		if file.UncompressedSize64 > maxImportFileSize {
			return fmt.Errorf("%w: OneTool text file exceeds the 1 MiB file limit", errImportCapExceeded)
		}
		total += file.UncompressedSize64
		if total > maxImportTotalSize+maxImportFileSize {
			return fmt.Errorf("%w: OneTool text bundle exceeds the import limit", errImportCapExceeded)
		}
		text, err := readZipFile(file, maxImportFileSize)
		if err != nil || !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') {
			return errors.New("OneTool ZIP contains unreadable or invalid text files")
		}
	}
	return nil
}
