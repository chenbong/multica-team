package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const defaultMachineReleaseAPI = "https://api.github.com/repos/chenbong/multica-team/releases?per_page=100"
const machineReleaseCacheTTL = 5 * time.Minute

var machineReleaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-overlay\.[0-9]+$`)

type machineReleaseEntry struct {
	TagName     string     `json:"tag_name"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt *time.Time `json:"published_at"`
}

type machineReleaseCache struct {
	mu       sync.Mutex
	endpoint string
	version  string
	expires  time.Time
}

var machineReleases machineReleaseCache

// latest returns the newest published stable Team Mesh release, independently
// of GitHub's Latest marker used by existing daemon updaters.
func (c *machineReleaseCache) latest(ctx context.Context, endpoint string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.endpoint == endpoint && time.Now().Before(c.expires) {
		if c.version == "" {
			return "", errors.New("release lookup recently failed")
		}
		return c.version, nil
	}
	entries, err := fetchMachineReleases(ctx, endpoint)
	if err != nil {
		if ctx.Err() == nil {
			c.endpoint, c.version, c.expires = endpoint, "", time.Now().Add(15*time.Second)
		}
		return "", err
	}
	var best machineReleaseEntry
	for _, entry := range entries {
		if entry.Draft || entry.Prerelease || entry.PublishedAt == nil || !machineReleaseTag.MatchString(entry.TagName) {
			continue
		}
		if best.PublishedAt == nil || entry.PublishedAt.After(*best.PublishedAt) {
			best = entry
		}
	}
	if best.PublishedAt == nil {
		c.endpoint, c.version, c.expires = endpoint, "", time.Now().Add(15*time.Second)
		return "", errors.New("no published stable Team Mesh CLI release")
	}
	c.endpoint, c.version, c.expires = endpoint, best.TagName, time.Now().Add(machineReleaseCacheTTL)
	return best.TagName, nil
}

func fetchMachineReleases(ctx context.Context, endpoint string) ([]machineReleaseEntry, error) {
	if !machineSafeURL(endpoint) {
		return nil, errors.New("invalid release endpoint")
	}
	fetch := func(proxy func(*http.Request) (*url.URL, error)) ([]machineReleaseEntry, error) {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			base = &http.Transport{}
		}
		transport := base.Clone()
		transport.Proxy = proxy
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "team-mesh-machine-provision")
		response, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("release lookup returned HTTP %d", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
		if err != nil || len(data) > 4*1024*1024 {
			return nil, errors.New("release response unavailable")
		}
		var releases []machineReleaseEntry
		if err := json.Unmarshal(data, &releases); err != nil {
			return nil, errors.New("invalid release response")
		}
		return releases, nil
	}
	releases, err := fetch(http.ProxyFromEnvironment)
	if err == nil {
		return releases, nil
	}
	parsed, _ := url.Parse(endpoint)
	if parsed == nil || !strings.EqualFold(parsed.Hostname(), "api.github.com") {
		return nil, err
	}
	// This deployment's installer already uses the same corporate proxy. Try
	// it only if direct/environment-configured access to GitHub is unavailable.
	proxy, _ := url.Parse("http://agent.baidu.com:8188")
	return fetch(http.ProxyURL(proxy))
}

// LatestMachineRelease is user-authenticated and returns only a public tag.
func (h *Handler) LatestMachineRelease(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	endpoint := strings.TrimSpace(os.Getenv("MULTICA_MACHINE_RELEASE_API_URL"))
	if endpoint == "" {
		endpoint = defaultMachineReleaseAPI
	}
	version, err := machineReleases.latest(r.Context(), endpoint)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unable to determine a published Team Mesh CLI release")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintln(w, version)
}
