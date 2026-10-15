package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/sync/singleflight"
)

const oneAPICatalogTTL = 5 * time.Minute
const oneAPICatalogLimit = 2 * 1024 * 1024

var errOneAPIConfig = errors.New("OneAPI model catalog configuration is unavailable; contact the platform administrator")
var errOneAPIFetch = errors.New("OneAPI model catalog is unavailable; retry or enter a model ID manually")

// OneAPIModelCatalog is opt-in server-side discovery, not a provider for model
// execution. The credential is read from a private deployment file and never
// enters a model entry, error, log, daemon payload or source-controlled config.
type OneAPIModelCatalog struct {
	endpoint, tokenFile string
	client              *http.Client
	configError         bool
	mu                  sync.Mutex
	key                 [32]byte
	models              []oneAPIModel
	storedAt            time.Time
	group               singleflight.Group
}

type oneAPIModel struct {
	ID        string   `json:"id"`
	Endpoints []string `json:"supported_endpoint_types"`
}

type OneAPIModelResult struct {
	Models   []Model
	StoredAt time.Time
	Cached   bool
}

// The third argument is retained for deployment compatibility. The old protocol
// allowlist is deliberately ignored: discovery lists every returned model and
// does not assert that an arbitrary model supports a runtime's wire protocol.
func NewOneAPIModelCatalog(endpoint, tokenFile, _ string, caFile string) *OneAPIModelCatalog {
	if endpoint == "" {
		return nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The internal catalog is independent of the proxy used for public CLI
	// downloads or the IM gateway. Do not forward its token through that proxy.
	transport.Proxy = nil
	configError := false
	if caFile != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		pem, err := readOneAPIFile(caFile, oneAPICatalogLimit, false)
		if err != nil || !roots.AppendCertsFromPEM(pem) {
			configError = true
		} else {
			transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		}
	}
	return &OneAPIModelCatalog{
		endpoint: endpoint, tokenFile: tokenFile,
		configError: configError,
		client: &http.Client{Transport: transport, Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func readOneAPIFile(path string, limit int64, secret bool) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > limit || (secret && before.Mode().Perm()&0077 != 0) {
		return nil, errOneAPIConfig
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errOneAPIConfig
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errOneAPIConfig
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errOneAPIConfig
	}
	return data, nil
}

func (c *OneAPIModelCatalog) Models(ctx context.Context, protocol string, force bool) (OneAPIModelResult, error) {
	var result OneAPIModelResult
	if c.configError {
		return result, errOneAPIConfig
	}
	if protocol != "messages" && protocol != "responses" {
		return result, errOneAPIConfig
	}
	u, err := url.Parse(c.endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return result, errOneAPIConfig
	}
	secret, err := readOneAPIFile(c.tokenFile, 8192, true)
	if err != nil {
		return result, err
	}
	token := strings.TrimSpace(string(secret))
	if token == "" || strings.ContainsFunc(token, unicode.IsSpace) {
		return result, errOneAPIConfig
	}
	key := sha256.Sum256([]byte(c.endpoint + "\x00" + token))
	c.mu.Lock()
	models, storedAt := c.models, c.storedAt
	cached := c.key == key && models != nil && time.Since(storedAt) < oneAPICatalogTTL
	c.mu.Unlock()
	if !cached || force {
		// Concurrent ducc/ducx pickers share one bounded fetch. A cancelled
		// browser request may leave the fetch warming the cache for at most 10s.
		ch := c.group.DoChan(string(key[:]), func() (any, error) {
			fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			fresh, fetchErr := c.fetch(fetchCtx, token)
			c.mu.Lock()
			defer c.mu.Unlock()
			if fetchErr != nil {
				if c.key == key {
					c.models = nil
				}
				return nil, fetchErr
			}
			c.models, c.key, c.storedAt = fresh, key, time.Now()
			return OneAPIModelResult{StoredAt: c.storedAt, Models: nil}, nil
		})
		select {
		case <-ctx.Done():
			return result, errOneAPIFetch
		case response := <-ch:
			if response.Err != nil {
				return result, response.Err
			}
		}
		c.mu.Lock()
		if c.key != key {
			c.mu.Unlock()
			return result, errOneAPIFetch
		}
		models, storedAt = c.models, c.storedAt
		c.mu.Unlock()
		cached = false
	}
	result.Models = listOneAPIModels(models)
	result.StoredAt, result.Cached = storedAt, cached
	return result, nil
}

func (c *OneAPIModelCatalog) fetch(ctx context.Context, token string) ([]oneAPIModel, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint, nil)
	if err != nil {
		return nil, errOneAPIConfig
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errOneAPIFetch
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errOneAPIFetch
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, oneAPICatalogLimit+1))
	if err != nil || len(body) > oneAPICatalogLimit {
		return nil, errOneAPIFetch
	}
	var payload struct {
		Data []oneAPIModel `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Data == nil || len(payload.Data) == 0 || len(payload.Data) > 4096 {
		return nil, errOneAPIFetch
	}
	for _, model := range payload.Data {
		if model.ID == "" || strings.TrimSpace(model.ID) != model.ID || len(model.ID) > 256 || strings.ContainsFunc(model.ID, unicode.IsControl) {
			return nil, errOneAPIFetch
		}
	}
	return payload.Data, nil
}

func listOneAPIModels(models []oneAPIModel) []Model {
	seen := make(map[string]bool)
	result := make([]Model, 0)
	for _, m := range models {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		result = append(result, Model{ID: m.ID, Label: m.ID, Provider: "OneAPI"})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
