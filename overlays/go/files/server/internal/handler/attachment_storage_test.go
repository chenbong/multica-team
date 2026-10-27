package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/testutil"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func attachmentTestStores(t *testing.T) (*storage.AttachmentStorage, *storage.LocalStorage) {
	t.Helper()
	t.Setenv("LOCAL_UPLOAD_DIR", t.TempDir())
	t.Setenv("LOCAL_UPLOAD_BASE_URL", "http://legacy.example.test")
	legacy := storage.NewLocalStorageFromEnv()
	t.Setenv("LOCAL_UPLOAD_DIR", t.TempDir())
	t.Setenv("LOCAL_UPLOAD_BASE_URL", "http://private.example.test")
	primary := storage.NewLocalStorageFromEnv()
	return &storage.AttachmentStorage{Primary: primary, Legacy: legacy, Prefix: "test-attachments"}, legacy
}
func TestAttachmentStorageVerifiedMigrationAndDraftExpiry(t *testing.T) {
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	store, legacy := attachmentTestStores(t)
	ctx := context.Background()
	key := "workspaces/fixture/" + uuid.NewString() + ".txt"
	body := []byte("safe attachment migration fixture")
	raw, err := legacy.Upload(ctx, key, body, "text/plain", "fixture.txt")
	if err != nil {
		t.Fatal(err)
	}
	issue := dbfx.Issue(t, "Keep attachment history")
	att := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "issue_id": issue, "uploader_type": "member", "uploader_id": testUserID, "filename": "fixture.txt", "url": raw, "content_type": "text/plain", "size_bytes": len(body)})
	comment := dbfx.Comment(t, issue, "Historical Markdown ![sample]("+raw+")")
	agent := dbfx.Agent(t, "Migrated avatar", testRuntimeID, testutil.Cols{"avatar_url": raw})
	report, err := service.RunAttachmentStorageMaintenance(ctx, testPool, store, "migrate")
	if err != nil {
		t.Fatal(err)
	}
	if report.Migrated != 1 {
		t.Fatalf("migration count %+v", report)
	}
	var migrated string
	dbfx.QueryRow(t, `SELECT url FROM attachment WHERE id=$1`, att).Scan(&migrated)
	if !strings.Contains(migrated, "test-attachments/") {
		t.Fatal("metadata was not moved to private storage")
	}
	reader, err := store.GetReader(ctx, store.KeyFromURL(migrated))
	if err != nil {
		t.Fatal(err)
	}
	read, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(read) != string(body) {
		t.Fatal("migration verification did not preserve bytes")
	}
	if old, e := legacy.GetReader(ctx, key); e == nil {
		_ = old.Close()
		t.Fatal("verified local copy was retained")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM comment WHERE id=$1 AND content=$2`, comment, "Historical Markdown ![sample]("+raw+")"); n != 1 {
		t.Fatal("historical Markdown was rewritten")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM agent WHERE id=$1 AND avatar_url=$2`, agent, migrated); n != 1 {
		t.Fatal("avatar URL did not follow verified migration")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM attachment_storage_alias WHERE attachment_id=$1 AND old_url=$2`, att, raw); n != 1 {
		t.Fatal("legacy link alias missing")
	}
	makeDraft := func(name string, hours int) string {
		object, err := store.Upload(ctx, "workspaces/fixture/"+uuid.NewString(), []byte(name), "text/plain", name)
		if err != nil {
			t.Fatal(err)
		}
		id := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "uploader_type": "member", "uploader_id": testUserID, "filename": name, "url": object, "content_type": "text/plain", "size_bytes": len(name), "created_at": time.Now().Add(-time.Duration(hours) * time.Hour)})
		return id
	}
	expired := makeDraft("expired", 25)
	recent := makeDraft("recent", 23)
	referenced := makeDraft("referenced", 25)
	dbfx.Exec(t, `UPDATE issue SET description=$2 WHERE id=$1`, issue, "Published inline attachment /api/attachments/"+referenced+"/download")
	report, err = service.RunAttachmentStorageMaintenance(ctx, testPool, store, "cleanup")
	if err != nil {
		t.Fatal(err)
	}
	if report.DraftsExpired != 1 || report.ObjectsDeleted != 1 {
		t.Fatalf("unexpected cleanup %+v", report)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM attachment WHERE id=$1`, expired); n != 0 {
		t.Fatal("expired draft remains")
	}
	for _, keep := range []string{att, recent, referenced} {
		if n := dbfx.Count(t, `SELECT count(*) FROM attachment WHERE id=$1`, keep); n != 1 {
			t.Fatal("formally referenced or recent attachment was removed")
		}
	}
	// The verified legacy URL remains a stable mapping; deleting its source
	// record makes it a tombstone instead of exposing any old local object.
}

func TestAttachmentStorageKeepsAvatarBoundaryAndHistoricalURLs(t *testing.T) {
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	store, _ := attachmentTestStores(t)
	h := *testHandler
	h.Storage = store
	ctx := context.Background()
	id := uuid.NewString()
	key := "workspaces/" + testWorkspaceID + "/" + id + ".png"
	raw, err := store.Upload(ctx, key, []byte("synthetic fixture image"), "image/png", "avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Insert(t, "attachment", testutil.Cols{"id": id, "workspace_id": testWorkspaceID, "uploader_type": "member", "uploader_id": testUserID, "filename": "avatar.png", "url": raw, "content_type": "image/png", "size_bytes": 23})
	routed := store.KeyFromURL(raw)
	if h.ownedStorageKey(raw) != routed || !h.avatarKeyIsPublishable(ctx, routed) {
		t.Fatal("private standalone avatar storage no longer round-trips")
	}
	issue := dbfx.Issue(t, "Private task image")
	dbfx.Exec(t, `UPDATE attachment SET issue_id=$2 WHERE id=$1`, id, issue)
	if h.avatarKeyIsPublishable(ctx, routed) {
		t.Fatal("private task image became publishable through a storage routing prefix")
	}
	if store.KeyFromURL("https://foreign.example.test/backups/secret.txt") != "" {
		t.Fatal("foreign storage namespace was accepted")
	}
	if err := store.DeleteObject(ctx, "bos:backups/foreign"); err == nil {
		t.Fatal("foreign object deletion was accepted")
	}
}

type attachmentZeroReader struct{}

func (attachmentZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type discardAttachmentStore struct {
	mockStorage
	bytes int64
	calls int
}

func (s *discardAttachmentStore) UploadStream(ctx context.Context, key string, r io.Reader, _ int64, ct, name string) (string, error) {
	s.calls++
	n, err := io.Copy(io.Discard, r)
	s.bytes += n
	return s.ObjectURL(key), err
}
func TestAttachmentUploadAcceptsExactlyFiftyMiB(t *testing.T) {
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	for _, size := range []int64{50 << 20, (50 << 20) + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			store := &discardAttachmentStore{}
			h := *testHandler
			h.Storage = store
			reader, writer := io.Pipe()
			form := multipart.NewWriter(writer)
			done := make(chan struct{})
			go func() {
				defer close(done)
				part, err := form.CreateFormFile("file", "boundary.bin")
				if err == nil {
					_, err = io.CopyN(part, attachmentZeroReader{}, size)
				}
				if err == nil {
					err = form.Close()
				}
				_ = writer.CloseWithError(err)
			}()
			req := httptest.NewRequest(http.MethodPost, "/api/upload-file", reader)
			req.Header.Set("Content-Type", form.FormDataContentType())
			req.Header.Set("X-User-ID", testUserID)
			req.Header.Set("X-Workspace-ID", testWorkspaceID)
			recorder := httptest.NewRecorder()
			h.UploadFile(recorder, req)
			_ = reader.Close()
			<-done
			if size == 50<<20 {
				if recorder.Code != 200 || store.bytes != size || store.calls != 1 {
					t.Fatalf("exact boundary rejected: status=%d bytes=%d", recorder.Code, store.bytes)
				}
				var response AttachmentResponse
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				dbfx.Cleanup(t, `DELETE FROM attachment WHERE id=$1`, response.ID)
			} else if recorder.Code != 413 || store.calls != 0 {
				t.Fatalf("oversized payload reached object storage: status=%d calls=%d", recorder.Code, store.calls)
			}
		})
	}
}

type corruptAttachmentReadback struct{ storage.Storage }

func (s corruptAttachmentReadback) UploadStream(ctx context.Context, key string, r io.Reader, size int64, ct, name string) (string, error) {
	return s.Storage.(interface {
		UploadStream(context.Context, string, io.Reader, int64, string, string) (string, error)
	}).UploadStream(ctx, key, r, size, ct, name)
}

func (s corruptAttachmentReadback) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("wrong bytes")), nil
}
func TestAttachmentStorageFailedVerificationKeepsLocalFile(t *testing.T) {
	store, legacy := attachmentTestStores(t)
	ctx := context.Background()
	store.Primary = corruptAttachmentReadback{store.Primary}
	key := "workspaces/fixture/" + uuid.NewString() + ".txt"
	raw, err := legacy.Upload(ctx, key, []byte("original bytes"), "text/plain", "keep.txt")
	if err != nil {
		t.Fatal(err)
	}
	att := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "uploader_type": "member", "uploader_id": testUserID, "filename": "keep.txt", "url": raw, "content_type": "text/plain", "size_bytes": 14})
	if _, err = service.RunAttachmentStorageMaintenance(ctx, testPool, store, "migrate"); err == nil || !strings.Contains(err.Error(), "object verification failed") {
		t.Fatal("corrupt readback was accepted")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM attachment WHERE id=$1 AND url=$2`, att, raw); n != 1 {
		t.Fatal("failed migration changed metadata")
	}
	reader, err := legacy.GetReader(ctx, key)
	if err != nil {
		t.Fatal("failed verification removed original", err)
	}
	defer reader.Close()
	bytes, _ := io.ReadAll(reader)
	if string(bytes) != "original bytes" {
		t.Fatal("original content changed")
	}
}

func TestAttachmentMigrationRetainsMissingHistoricalMetadata(t *testing.T) {
	store, legacy := attachmentTestStores(t)
	ctx := context.Background()
	missingURL := legacy.ObjectURL("workspaces/fixture/" + uuid.NewString() + ".txt")
	missing := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "uploader_type": "member", "uploader_id": testUserID, "filename": "missing.txt", "url": missingURL, "content_type": "text/plain", "size_bytes": 7})
	raw, err := legacy.Upload(ctx, "workspaces/fixture/"+uuid.NewString()+".txt", []byte("present"), "text/plain", "present.txt")
	if err != nil {
		t.Fatal(err)
	}
	present := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "uploader_type": "member", "uploader_id": testUserID, "filename": "present.txt", "url": raw, "content_type": "text/plain", "size_bytes": 7})
	report, err := service.RunAttachmentStorageMaintenance(ctx, testPool, store, "migrate")
	if err != nil || report.LegacyMissing != 1 || report.Migrated != 1 {
		t.Fatalf("migration did not retain/skip missing original: %+v %v", report, err)
	}
	if dbfx.Count(t, `SELECT count(*) FROM attachment WHERE id=$1 AND url=$2`, missing, missingURL) != 1 {
		t.Fatal("missing metadata was fabricated or removed")
	}
	if dbfx.Count(t, `SELECT count(*) FROM attachment WHERE id=$1 AND url<>$2`, present, raw) != 1 {
		t.Fatal("missing original blocked valid migration")
	}
	if dbfx.Count(t, `SELECT count(*) FROM attachment_storage_alias WHERE attachment_id=$1`, missing) != 0 {
		t.Fatal("missing original acquired a misleading alias")
	}
}
