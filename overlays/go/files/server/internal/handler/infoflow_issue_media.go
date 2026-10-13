package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

type infoFlowAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	DataBase64  string `json:"data_base64"`
}

func (h *Handler) infoFlowUploadAttachments(r *http.Request, attachments []infoFlowAttachment, issueID pgtype.UUID, creating bool) ([]string, []string, error) {
	if len(attachments) > 5 {
		return nil, nil, errors.New("too many attachments")
	}
	// Validate the entire batch before any object-storage write. The upload
	// handler sniffs content, so client-supplied MIME cannot confer authority.
	data := make([][]byte, len(attachments))
	total := 0
	for i, a := range attachments {
		if a.Filename == "" || len(a.Filename) > 255 || strings.ContainsAny(a.Filename, "\x00\r\n/\\") || filepath.Base(a.Filename) != a.Filename {
			return nil, nil, errors.New("invalid filename")
		}
		var err error
		data[i], err = base64.StdEncoding.Strict().DecodeString(a.DataBase64)
		if err != nil || len(data[i]) == 0 || len(data[i]) > 20<<20 {
			return nil, nil, errors.New("invalid attachment")
		}
		total += len(data[i])
		if total > 20<<20 {
			return nil, nil, errors.New("attachments too large")
		}
	}
	var ids, urls []string
	for i, a := range attachments {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		mh := make(textproto.MIMEHeader)
		mh.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": a.Filename}))
		mh.Set("Content-Type", "application/octet-stream")
		part, err := mw.CreatePart(mh)
		if err != nil {
			return ids, urls, err
		}
		if _, err = part.Write(data[i]); err != nil {
			return ids, urls, err
		}
		if !creating {
			if err = mw.WriteField("issue_id", uuidToString(issueID)); err != nil {
				return ids, urls, err
			}
		}
		if err = mw.Close(); err != nil {
			return ids, urls, err
		}
		upload := infoFlowMemberRequest(r.Context(), requestUserID(r), h.resolveWorkspaceID(r), "", buf.Bytes())
		upload.Header.Set("Content-Type", mw.FormDataContentType())
		capture := &infoFlowCapture{}
		h.UploadFile(capture, upload)
		if capture.status < 200 || capture.status >= 300 {
			return ids, urls, errors.New("attachment upload failed")
		}
		var out struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		}
		err = json.Unmarshal(capture.Bytes(), &out)
		if out.URL != "" {
			urls = append(urls, out.URL)
		}
		if err != nil || out.ID == "" || out.URL == "" {
			return ids, urls, errors.New("invalid upload result")
		}
		ids = append(ids, out.ID)
	}
	return ids, urls, nil
}
