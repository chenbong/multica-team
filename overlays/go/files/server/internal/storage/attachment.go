package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

// New writes use the private object store. Legacy disk is read during verified
// migration; a failed object upload never falls back to persistent local disk.
type AttachmentStorage struct {
	Primary Storage
	Legacy  *LocalStorage
	Prefix  string
}

func NewAttachmentStorageFromEnv() Storage {
	primary := NewS3StorageFromEnv()
	if os.Getenv("MULTICA_ATTACHMENT_BOS_ENABLED") == "true" {
		if primary == nil {
			panic("private attachment object storage is required")
		}
		prefix := strings.Trim(os.Getenv("MULTICA_ATTACHMENT_BOS_PREFIX"), "/")
		if prefix == "" || strings.Contains(prefix, "..") || strings.ContainsAny(prefix, "\\\x00\r\n") {
			panic("invalid attachment object prefix")
		}
		return &AttachmentStorage{Primary: primary, Legacy: NewLocalStorageFromEnv(), Prefix: prefix}
	}
	if primary != nil {
		return primary
	}
	local := NewLocalStorageFromEnv()
	if local != nil {
		return local
	}
	return nil
}
func (s *AttachmentStorage) objectKey(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.ContainsAny(key, "\\\x00\r\n") {
		return "", errors.New("invalid attachment object key")
	}
	return s.Prefix + "/" + key, nil
}
func (s *AttachmentStorage) Upload(ctx context.Context, key string, data []byte, ct, name string) (string, error) {
	return s.UploadStream(ctx, key, bytes.NewReader(data), int64(len(data)), ct, name)
}
func (s *AttachmentStorage) UploadStream(ctx context.Context, key string, r io.Reader, size int64, ct, name string) (string, error) {
	target, err := s.objectKey(key)
	if err != nil {
		return "", err
	}
	stream, ok := s.Primary.(interface {
		UploadStream(context.Context, string, io.Reader, int64, string, string) (string, error)
	})
	if !ok {
		return "", errors.New("attachment storage does not support streaming")
	}
	return stream.UploadStream(ctx, target, r, size, ct, name)
}
func (s *AttachmentStorage) ObjectURL(key string) string {
	if strings.HasPrefix(key, "bos:") || strings.HasPrefix(key, "local:") {
		store, target, err := s.route(key)
		if err != nil {
			return ""
		}
		return store.ObjectURL(target)
	}
	target, err := s.objectKey(key)
	if err != nil {
		return ""
	}
	return s.Primary.ObjectURL(target)
}
func (s *AttachmentStorage) CdnDomain() string { return "" }
func (s *AttachmentStorage) KeyFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" {
		return ""
	}
	expected, err := url.Parse(s.Primary.ObjectURL(s.Prefix + "/probe"))
	if err != nil {
		return ""
	}
	if strings.EqualFold(u.Host, expected.Host) && u.Scheme == expected.Scheme {
		if key := s.Primary.KeyFromURL(raw); strings.HasPrefix(key, s.Prefix+"/") {
			return "bos:" + key
		}
	}
	if s.Legacy != nil && (u.Scheme == "" || u.Scheme == "http" || u.Scheme == "https") && strings.HasPrefix(u.Path, "/uploads/") && u.RawQuery == "" && u.Fragment == "" {
		if key := strings.TrimPrefix(u.Path, "/uploads/"); key != "" {
			return "local:" + key
		}
	}
	return ""
}
func (s *AttachmentStorage) route(key string) (Storage, string, error) {
	if strings.Contains(key, "..") || strings.ContainsAny(key, "\\\x00\r\n") {
		return nil, "", errors.New("invalid object key")
	}
	if strings.HasPrefix(key, "bos:"+s.Prefix+"/") {
		return s.Primary, strings.TrimPrefix(key, "bos:"), nil
	}
	if strings.HasPrefix(key, "local:") && s.Legacy != nil {
		local := strings.TrimPrefix(key, "local:")
		if local != "" && !strings.HasPrefix(local, "/") {
			return s.Legacy, local, nil
		}
	}
	if !strings.HasPrefix(key, "bos:") && !strings.HasPrefix(key, "local:") {
		target, err := s.objectKey(key)
		if err == nil {
			return s.Primary, target, nil
		}
	}
	return nil, "", errors.New("object is outside attachment storage namespace")
}
func (s *AttachmentStorage) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	store, key, err := s.route(key)
	if err != nil {
		return nil, err
	}
	return store.GetReader(ctx, key)
}
func (s *AttachmentStorage) DeleteObject(ctx context.Context, key string) error {
	store, key, err := s.route(key)
	if err != nil {
		return err
	}
	return store.DeleteObject(ctx, key)
}
func (s *AttachmentStorage) Delete(ctx context.Context, key string) { _ = s.DeleteObject(ctx, key) }
func (s *AttachmentStorage) DeleteKeys(ctx context.Context, keys []string) {
	for _, key := range keys {
		s.Delete(ctx, key)
	}
}
func (s *AttachmentStorage) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	store, key, err := s.route(key)
	if err != nil {
		return "", err
	}
	if p, ok := store.(Presigner); ok {
		return p.PresignGet(ctx, key, ttl)
	}
	return "", errors.New("legacy attachment requires authenticated proxy")
}
func (s *AttachmentStorage) PresignGetWithContentDisposition(ctx context.Context, key string, ttl time.Duration, disposition string) (string, error) {
	store, key, err := s.route(key)
	if err != nil {
		return "", err
	}
	if p, ok := store.(DownloadPresigner); ok {
		return p.PresignGetWithContentDisposition(ctx, key, ttl, disposition)
	}
	return "", errors.New("attachment requires authenticated proxy")
}
func LegacyLocalStorage(store Storage) *LocalStorage {
	if local, ok := store.(*LocalStorage); ok {
		return local
	}
	if mixed, ok := store.(*AttachmentStorage); ok {
		return mixed.Legacy
	}
	return nil
}
func IsLegacyAttachment(store Storage, raw string) bool {
	mixed, ok := store.(*AttachmentStorage)
	return ok && strings.HasPrefix(mixed.KeyFromURL(raw), "local:")
}

// Consumers that classify ownership namespaces must inspect the logical key,
// not confuse an object-store routing tag with a public asset namespace.
func AttachmentLogicalKey(store Storage, key string) string {
	if mixed, ok := store.(*AttachmentStorage); ok {
		if strings.HasPrefix(key, "bos:"+mixed.Prefix+"/") {
			return strings.TrimPrefix(key, "bos:"+mixed.Prefix+"/")
		}
		if strings.HasPrefix(key, "local:") {
			return strings.TrimPrefix(key, "local:")
		}
		if !strings.HasPrefix(key, "bos:") {
			if _, err := mixed.objectKey(key); err == nil {
				return key
			}
		}
		return ""
	}
	return key
}

// Old durable object intents already contain their storage URL. Resolve that
// origin before routing a key recorded before the BOS cutover.
func AttachmentDeletionKey(store any, key, rawURL string) string {
	if mixed, ok := store.(*AttachmentStorage); ok {
		if resolved := mixed.KeyFromURL(rawURL); resolved != "" {
			return resolved
		}
	}
	return key
}
