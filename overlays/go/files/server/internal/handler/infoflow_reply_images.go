package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type infoFlowReplyImage struct {
	ID           string `json:"id"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	UploaderType string `json:"uploader_type"`
	URL          string `json:"url"`
	MarkdownURL  string `json:"markdown_url"`
}

// The metadata is additive to the delivery protocol. No webpage attachment or
// canonical comment is rewritten, and user-authored image mirrors stay counts.
func (h *Handler) infoFlowReplyImages(ctx context.Context, tx pgx.Tx, scope infoFlowScope, delivery *infoFlowDelivery) error {
	var created time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM comment WHERE id=$1 AND workspace_id=$2`, delivery.CommentID, scope.WorkspaceID).Scan(&created); err != nil {
		return err
	}
	delivery.CommentCreatedAt = created.Format(time.RFC3339Nano)
	if delivery.AuthorType != "agent" {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT a.id::text,a.filename,a.content_type,a.size_bytes,a.url,count(*) OVER ()
		FROM attachment a JOIN comment c ON c.id=a.comment_id
		WHERE a.comment_id=$1 AND a.workspace_id=$2 AND a.issue_id=$3
		AND c.issue_id=a.issue_id AND c.workspace_id=a.workspace_id AND c.author_type='agent' AND c.deleted_at IS NULL
		AND a.uploader_type='agent' AND a.content_type LIKE 'image/%'
		ORDER BY a.created_at,a.id LIMIT 100`, delivery.CommentID, scope.WorkspaceID, delivery.IssueID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		v := infoFlowReplyImage{UploaderType: "agent"}
		if err = rows.Scan(&v.ID, &v.Filename, &v.ContentType, &v.SizeBytes, &v.URL, &delivery.ImageCount); err != nil {
			return err
		}
		v.MarkdownURL = strings.TrimRight(h.cfg.PublicURL, "/") + "/api/attachments/" + v.ID + "/download"
		delivery.ImageAttachments = append(delivery.ImageAttachments, v)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if delivery.ImageCount > 0 {
		delivery.ImageNotice = fmt.Sprintf("\n\n图片：%d 张，请打开任务链接查看。", delivery.ImageCount)
	}
	return nil
}

// The relay token does not grant arbitrary attachment access. Every request is
// fenced by the live delivery/binding generation, sync switch and comment scope.
func (h *Handler) InfoFlowIssueReplyImage(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowDeliveryRequest
	if !infoFlowDecode(w, r, &req) {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	imageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "attachmentId"), "attachmentId")
	if !ok {
		return
	}
	if !req.infoFlowScope.valid() {
		infoFlowError(w, 400, "invalid image scope")
		return
	}
	allowed, err := h.infoFlowDeliveryAllowed(r.Context(), req.infoFlowScope, id)
	if err != nil {
		infoFlowError(w, 500, "image delivery check")
		return
	}
	if !allowed || h.Storage == nil {
		infoFlowError(w, 404, "image unavailable")
		return
	}
	var rawURL, contentType string
	var size int64
	err = h.DB.QueryRow(r.Context(), `SELECT a.url,a.content_type,a.size_bytes FROM attachment a
		JOIN comment c ON c.id=a.comment_id JOIN infoflow_issue_delivery d ON d.comment_id=c.id
		JOIN infoflow_issue_binding b ON b.id=d.binding_id
		WHERE d.id=$1 AND a.id=$2 AND a.workspace_id=$3 AND c.workspace_id=a.workspace_id
		AND a.issue_id=c.issue_id AND b.issue_id=c.issue_id AND b.workspace_id=a.workspace_id
		AND c.author_type='agent' AND c.deleted_at IS NULL AND a.uploader_type='agent'
		AND d.generation=b.generation AND b.sync_enabled AND d.acked_at IS NULL AND d.cancelled_at IS NULL`, id, imageID, req.WorkspaceID).Scan(&rawURL, &contentType, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		infoFlowError(w, 404, "image unavailable")
		return
	}
	if err != nil {
		infoFlowError(w, 500, "image lookup")
		return
	}
	if size <= 0 || size > 20*1024*1024 || !strings.HasPrefix(contentType, "image/") {
		infoFlowError(w, 413, "image unsupported or too large")
		return
	}
	reader, err := h.Storage.GetReader(r.Context(), h.Storage.KeyFromURL(rawURL))
	if err != nil {
		infoFlowError(w, 404, "image unavailable")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprint(size))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(reader, size))
}
