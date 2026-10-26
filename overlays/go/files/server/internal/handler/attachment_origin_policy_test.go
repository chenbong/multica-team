package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/storage"
)

func TestAttachmentOriginOptInPreservesOtherCSPDirectives(t *testing.T) {
	t.Setenv("MULTICA_ATTACHMENT_ALLOW_ALL_FRAME_ANCESTORS", "true")
	got := attachmentPreviewCSPHeader([]string{"https://old.example.test"})
	for _, want := range []string{"frame-ancestors *;", "default-src 'none'", "img-src 'self' data:", "media-src 'self'", "object-src 'none'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(got, want) {
			t.Fatal("directive missing", got, want)
		}
	}
	if strings.Contains(got, "old.example.test") {
		t.Fatal("old origins retained unexpectedly")
	}
	t.Setenv("MULTICA_ATTACHMENT_ALLOW_ALL_FRAME_ANCESTORS", "false")
	got = attachmentPreviewCSPHeader([]string{"https://old.example.test"})
	if !strings.Contains(got, "frame-ancestors 'self' https://old.example.test;") || strings.Contains(got, "frame-ancestors *;") {
		t.Fatal("default behavior changed", got)
	}
}

func TestLocalAttachmentOriginOptInServesPreviewWithWildcard(t *testing.T) {
	t.Setenv("MULTICA_ATTACHMENT_ALLOW_ALL_FRAME_ANCESTORS", "true")
	dir := t.TempDir()
	if e := os.MkdirAll(filepath.Join(dir, "fixture"), 0o700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "fixture", "preview.pdf"), []byte("%PDF-1.7 fixture"), 0o600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LOCAL_UPLOAD_DIR", dir)
	t.Setenv("LOCAL_UPLOAD_BASE_URL", "")
	h := &Handler{Storage: storage.NewLocalStorageFromEnv(), cfg: Config{AttachmentFrameAncestors: []string{"https://old.example.test"}}}
	r := httptest.NewRequest(http.MethodGet, "/uploads/fixture/preview.pdf", nil)
	w := httptest.NewRecorder()
	h.ServeLocalUpload(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors *;") {
		t.Fatal("local preview not embeddable", w.Code, w.Header())
	}
}

func TestAttachmentOriginOptInDoesNotOpenAvatarFrames(t *testing.T) {
	t.Setenv("MULTICA_ATTACHMENT_ALLOW_ALL_FRAME_ANCESTORS", "true")
	withAvatarStorage(t, &mockStorageNoCdn{}, "")
	key := seedAvatarObject(t, avatarObjectOpts{})
	w := serveAvatarRequest(t, signAvatarKey(key), key)
	if w.Code != http.StatusFound {
		t.Fatal("avatar fixture failed", w.Code)
	}
	header := w.Header().Get("Content-Security-Policy")
	if strings.Contains(header, "frame-ancestors *;") || !strings.Contains(header, "frame-ancestors 'self'") {
		t.Fatal("avatar framing changed", header)
	}
}
