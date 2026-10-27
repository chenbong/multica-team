package handler

import (
	"context"
	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/storage"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

func uploadAttachmentReader(ctx context.Context, store storage.Storage, key string, r io.ReadSeeker, size int64, ct, name string) (string, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if stream, ok := store.(interface {
		UploadStream(context.Context, string, io.Reader, int64, string, string) (string, error)
	}); ok {
		return stream.UploadStream(ctx, key, r, size, ct, name)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxUploadSize+1))
	if err != nil {
		return "", err
	}
	return store.Upload(ctx, key, data, ct, name)
}
func (h *Handler) attachmentPublicURL() string {
	candidates := []string{h.cfg.PublicURL}
	if _, ok := h.Storage.(*storage.AttachmentStorage); ok {
		candidates = []string{os.Getenv("MULTICA_ATTACHMENT_PUBLIC_APP_URL"), h.cfg.AppURL, h.cfg.PublicURL}
	}
	for _, raw := range candidates {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/") {
			return strings.TrimRight(u.String(), "/")
		}
	}
	return ""
}

// Redirect old-origin bookmarks to the configured app origin, where the user
// has their login cookie. The destination repeats current membership checks.
func (h *Handler) serveMigratedAttachment(w http.ResponseWriter, r *http.Request, key string) bool {
	if _, ok := h.Storage.(*storage.AttachmentStorage); !ok {
		return false
	}
	var exists bool
	if err := h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM attachment_storage_alias WHERE legacy_key=$1)`, key).Scan(&exists); err != nil {
		writeError(w, 503, "Attachment lookup unavailable")
		return true
	}
	if !exists {
		return false
	}
	http.Redirect(w, r, h.attachmentPublicURL()+"/api/attachments/legacy?key="+url.QueryEscape(key), http.StatusFound)
	return true
}
func (h *Handler) LegacyAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" || len(key) > 4096 {
		http.NotFound(w, r)
		return
	}
	var id, ws string
	err := h.DB.QueryRow(r.Context(), `SELECT a.id,a.workspace_id FROM attachment_storage_alias alias JOIN attachment a ON a.id=alias.attachment_id JOIN member m ON m.workspace_id=a.workspace_id AND m.user_id=$2 WHERE alias.legacy_key=$1 ORDER BY a.id LIMIT 1`, key, user).Scan(&id, &ws)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("X-Actor-Source") == "task_token" && r.Header.Get("X-Workspace-ID") != ws {
		http.NotFound(w, r)
		return
	}
	route := chi.NewRouteContext()
	route.URLParams.Add("id", id)
	inner := r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
	h.DownloadAttachment(w, inner)
}

var attachmentAliasCache = struct {
	sync.Mutex
	Rows map[string][]string
}{Rows: map[string][]string{}}

func (h *Handler) legacyAttachmentURLs(a db.Attachment) []string {
	if _, ok := h.Storage.(*storage.AttachmentStorage); !ok || !strings.Contains(a.Url, "/legacy/") {
		return nil
	}
	cacheKey := uuidToString(a.ID) + "\n" + a.Url
	attachmentAliasCache.Lock()
	cached, ok := attachmentAliasCache.Rows[cacheKey]
	attachmentAliasCache.Unlock()
	if ok {
		return cached
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := h.DB.Query(ctx, `SELECT old_url FROM attachment_storage_alias WHERE attachment_id=$1 ORDER BY old_url`, a.ID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			return nil
		}
		values = append(values, raw)
	}
	if rows.Err() != nil {
		return nil
	}
	attachmentAliasCache.Lock()
	if len(attachmentAliasCache.Rows) >= 4096 {
		attachmentAliasCache.Rows = map[string][]string{}
	}
	attachmentAliasCache.Rows[cacheKey] = values
	attachmentAliasCache.Unlock()
	return values
}
