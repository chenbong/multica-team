package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/util"
)

type AttachmentMaintenanceReport struct {
	StoreVerified  bool `json:"store_verified,omitempty"`
	Migrated       int  `json:"migrated"`
	LegacyMissing  int  `json:"legacy_missing"`
	LocalRemoved   int  `json:"local_removed"`
	DraftsExpired  int  `json:"drafts_expired"`
	ObjectsDeleted int  `json:"objects_deleted"`
}
type legacyAttachment struct {
	ID, WorkspaceID            pgtype.UUID
	URL, Filename, ContentType string
	Size                       int64
}

// No bucket listing or lifecycle changes: only attachment rows and the exact
// attachment-owned namespace are eligible. Image exports and backups stay intact.
func RunAttachmentStorageMaintenance(ctx context.Context, pool *pgxpool.Pool, store storage.Storage, mode string) (AttachmentMaintenanceReport, error) {
	report := AttachmentMaintenanceReport{}
	mixed, ok := store.(*storage.AttachmentStorage)
	if !ok {
		return report, errors.New("private attachment storage is not enabled")
	}
	if mode != "migrate" && mode != "cleanup" && mode != "verify" {
		return report, errors.New("unknown attachment maintenance mode")
	}
	if mode == "verify" {
		err := verifyPrivateAttachmentStore(ctx, mixed)
		report.StoreVerified = err == nil
		return report, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return report, err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(82461129)`).Scan(&locked); err != nil || !locked {
		return report, errors.New("attachment maintenance is already running")
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(82461129)`)
	if mode == "migrate" {
		if mixed.Legacy == nil {
			return report, errors.New("legacy local storage unavailable")
		}
		rows, err := pool.Query(ctx, `SELECT id,workspace_id,url,filename,content_type,size_bytes FROM attachment ORDER BY created_at,id`)
		if err != nil {
			return report, err
		}
		items := []legacyAttachment{}
		for rows.Next() {
			var a legacyAttachment
			if err = rows.Scan(&a.ID, &a.WorkspaceID, &a.URL, &a.Filename, &a.ContentType, &a.Size); err != nil {
				rows.Close()
				return report, err
			}
			if storage.IsLegacyAttachment(store, a.URL) {
				items = append(items, a)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return report, err
		}
		for _, a := range items {
			// Missing historical originals predate the cutover. Keep their
			// metadata unchanged; never turn them into empty objects or aliases.
			original, openErr := mixed.Legacy.GetReader(ctx, strings.TrimPrefix(mixed.KeyFromURL(a.URL), "local:"))
			if errors.Is(openErr, os.ErrNotExist) {
				report.LegacyMissing++
				continue
			}
			if openErr != nil {
				return report, openErr
			}
			_ = original.Close()
			if err = migrateLegacyAttachment(ctx, pool, mixed, a); err != nil {
				return report, fmt.Errorf("attachment %x migration failed: %w", a.ID.Bytes, err)
			}
			report.Migrated++
		}
		// A crash after URL commit but before local deletion resumes here.
		rows, err = pool.Query(ctx, `SELECT DISTINCT legacy_key,old_url FROM attachment_storage_alias`)
		if err != nil {
			return report, err
		}
		type local struct{ key, url string }
		locals := []local{}
		for rows.Next() {
			var v local
			if err = rows.Scan(&v.key, &v.url); err != nil {
				rows.Close()
				return report, err
			}
			locals = append(locals, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return report, err
		}
		for _, v := range locals {
			var referenced bool
			err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attachment WHERE url=$1 OR right(url,length($2))=$2) OR EXISTS(SELECT 1 FROM "user" WHERE avatar_url=$1) OR EXISTS(SELECT 1 FROM agent WHERE avatar_url=$1) OR EXISTS(SELECT 1 FROM workspace WHERE avatar_url=$1) OR EXISTS(SELECT 1 FROM squad WHERE avatar_url=$1)`, v.url, "/uploads/"+v.key).Scan(&referenced)
			if err != nil {
				return report, err
			}
			if !referenced {
				if err = mixed.DeleteObject(ctx, "local:"+v.key); err != nil {
					return report, err
				}
				report.LocalRemoved++
			}
		}
		return report, nil
	}
	for n := 0; n < 100; n++ {
		changed, err := expireAttachmentDraft(ctx, pool, mixed)
		if err != nil {
			return report, err
		}
		if !changed {
			break
		}
		report.DraftsExpired++
	}
	rows, err := pool.Query(ctx, `SELECT storage_key FROM attachment_object_delete ORDER BY created_at LIMIT 100`)
	if err != nil {
		return report, err
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return report, err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, err
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "bos:"+mixed.Prefix+"/") {
			return report, errors.New("delete ledger contains a foreign namespace")
		}
		if err = mixed.DeleteObject(ctx, key); err != nil {
			_, _ = pool.Exec(ctx, `UPDATE attachment_object_delete SET attempts=attempts+1 WHERE storage_key=$1`, key)
			continue
		}
		if _, err = pool.Exec(ctx, `DELETE FROM attachment_object_delete WHERE storage_key=$1`, key); err != nil {
			return report, err
		}
		report.ObjectsDeleted++
	}
	return report, nil
}

func migrateLegacyAttachment(ctx context.Context, pool *pgxpool.Pool, store *storage.AttachmentStorage, a legacyAttachment) error {
	oldKey := store.KeyFromURL(a.URL)
	source, err := store.GetReader(ctx, oldKey)
	if err != nil {
		return err
	}
	digest := sha256.New()
	key := fmt.Sprintf("workspaces/%s/legacy/%s%s", util.UUIDToString(a.WorkspaceID), util.UUIDToString(a.ID), path.Ext(a.Filename))
	target, err := store.UploadStream(ctx, key, io.TeeReader(source, digest), a.Size, a.ContentType, a.Filename)
	_ = source.Close()
	if err != nil {
		return err
	}
	uploaded, err := store.GetReader(ctx, store.KeyFromURL(target))
	if err != nil {
		return err
	}
	remote := sha256.New()
	size, err := io.Copy(remote, uploaded)
	_ = uploaded.Close()
	if err != nil || size != a.Size || hex.EncodeToString(remote.Sum(nil)) != hex.EncodeToString(digest.Sum(nil)) {
		return errors.New("object verification failed; local file retained")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, `SELECT url FROM attachment WHERE id=$1 FOR UPDATE`, a.ID).Scan(&current); err != nil {
		return err
	}
	if current != a.URL {
		return errors.New("attachment changed during migration; local file retained")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO attachment_storage_alias(legacy_key,attachment_id,old_url) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, strings.TrimPrefix(oldKey, "local:"), a.ID, a.URL); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE attachment SET url=$2 WHERE id=$1`, a.ID, target); err != nil {
		return err
	}
	for _, table := range []string{`"user"`, `agent`, `workspace`, `squad`} {
		if _, err = tx.Exec(ctx, "UPDATE "+table+" SET avatar_url=$2 WHERE avatar_url=$1 OR right(avatar_url,length($3))=$3", a.URL, target, "/uploads/"+strings.TrimPrefix(oldKey, "local:")); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// A session-scoped staging row without a message is still a draft. A task,
// issue, comment or frozen source-context binding is always kept.
const expiredDraftPredicate = `a.created_at<now()-interval '24 hours'
 AND a.issue_id IS NULL AND a.comment_id IS NULL AND a.chat_message_id IS NULL AND a.task_id IS NULL AND a.source_context_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM issue i WHERE strpos(coalesce(i.description,'')||coalesce(i.context_refs::text,''),a.id::text)>0 OR strpos(coalesce(i.description,''),a.url)>0)
 AND NOT EXISTS(SELECT 1 FROM comment c WHERE strpos(c.content,a.id::text)>0 OR strpos(c.content,a.url)>0)
 AND NOT EXISTS(SELECT 1 FROM chat_message m WHERE strpos(m.content,a.id::text)>0 OR strpos(m.content,a.url)>0)
 AND NOT EXISTS(SELECT 1 FROM agent_task_queue t WHERE strpos(coalesce(t.context::text,'')||coalesce(t.result::text,''),a.id::text)>0 OR strpos(coalesce(t.context::text,'')||coalesce(t.result::text,''),a.url)>0)
 AND NOT EXISTS(SELECT 1 FROM "user" u WHERE u.avatar_url=a.url)
 AND NOT EXISTS(SELECT 1 FROM agent ag WHERE ag.avatar_url=a.url OR strpos(coalesce(ag.instructions,''),a.url)>0)
 AND NOT EXISTS(SELECT 1 FROM workspace w WHERE w.avatar_url=a.url)
 AND NOT EXISTS(SELECT 1 FROM squad s WHERE s.avatar_url=a.url)
 AND NOT EXISTS(SELECT 1 FROM resource_web_panel p WHERE p.url=a.url)
 AND NOT EXISTS(SELECT 1 FROM project_resource p WHERE strpos(p.resource_ref::text,a.url)>0)`

func expireAttachmentDraft(ctx context.Context, pool *pgxpool.Pool, store *storage.AttachmentStorage) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id pgtype.UUID
	var raw string
	err = tx.QueryRow(ctx, `SELECT a.id,a.url FROM attachment a WHERE `+expiredDraftPredicate+` AND a.url LIKE $1 ORDER BY a.created_at,a.id LIMIT 1 FOR UPDATE OF a SKIP LOCKED`, store.Primary.ObjectURL(store.Prefix+"/")+"%").Scan(&id, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	key := store.KeyFromURL(raw)
	if !strings.HasPrefix(key, "bos:"+store.Prefix+"/") {
		return false, errors.New("draft outside attachment namespace")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO attachment_object_delete(storage_key) VALUES($1) ON CONFLICT DO NOTHING`, key); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM attachment WHERE id=$1`, id); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
func RunAttachmentStorageCleaner(ctx context.Context, pool *pgxpool.Pool, store storage.Storage) {
	if _, ok := store.(*storage.AttachmentStorage); !ok {
		return
	}
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			tick, cancel := context.WithTimeout(ctx, 2*time.Minute)
			report, err := RunAttachmentStorageMaintenance(tick, pool, store, "cleanup")
			cancel()
			if err != nil {
				slog.Warn("attachment draft cleanup failed; will retry")
			} else if report.DraftsExpired+report.ObjectsDeleted > 0 {
				slog.Info("attachment drafts cleaned", "drafts", report.DraftsExpired, "objects", report.ObjectsDeleted)
			}
			timer.Reset(10 * time.Minute)
		}
	}
}

func verifyPrivateAttachmentStore(ctx context.Context, store *storage.AttachmentStorage) (err error) {
	key := "verification/" + uuid.NewString() + ".txt"
	data := []byte("Team Mesh private attachment storage verification. No user data.")
	raw, err := store.Upload(ctx, key, data, "text/plain", "verification.txt")
	if err != nil {
		return errors.New("attachment object upload verification failed")
	}
	objectKey := store.KeyFromURL(raw)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if e := store.DeleteObject(cleanup, key); e != nil {
			err = fmt.Errorf("verification object cleanup failed for %s", key)
		}
	}()
	signed, e := store.PresignGet(ctx, objectKey, time.Minute)
	if e != nil {
		return errors.New("attachment signing verification failed")
	}
	original, e := url.Parse(raw)
	if e != nil || original.Scheme != "https" {
		return errors.New("attachment object URL must use HTTPS")
	}
	prepared, e := url.Parse(signed)
	if e != nil || prepared.Host != original.Host || prepared.EscapedPath() != original.EscapedPath() {
		return errors.New("attachment URL does not match the signed object endpoint")
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	if e != nil {
		return errors.New("attachment signed-read request failed")
	}
	response, e := client.Do(request)
	if e != nil {
		return errors.New("attachment signed-read verification failed")
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, int64(len(data))+1))
	_ = response.Body.Close()
	if e != nil || response.StatusCode != 200 || !bytes.Equal(body, data) {
		return errors.New("attachment signed-read content verification failed")
	}
	request, e = http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return errors.New("attachment anonymous probe failed")
	}
	response, e = client.Do(request)
	if e != nil {
		return errors.New("attachment anonymous-access verification unavailable")
	}
	_ = response.Body.Close()
	if response.StatusCode != 401 && response.StatusCode != 403 && response.StatusCode != 404 {
		return errors.New("attachment namespace is anonymously accessible or redirects unexpectedly")
	}
	return nil
}
