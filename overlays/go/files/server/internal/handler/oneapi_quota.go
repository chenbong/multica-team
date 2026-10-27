package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

const oneAPIQuotaInterval = 10 * time.Minute

type quotaValues struct {
	Unlimited bool     `json:"is_unlimited"`
	Total     *float64 `json:"month_quota_limit"`
	Remaining *float64 `json:"remaining_quota"`
	Used      *float64 `json:"used_quota"`
	Username  string   `json:"username"`
}
type oneAPIQuotaResponse struct {
	Status    string       `json:"status"`
	Values    *quotaValues `json:"values,omitempty"`
	UpdatedAt *time.Time   `json:"updated_at,omitempty"`
}
type quotaCacheKey struct {
	User, Endpoint string
	Credential     [32]byte
}
type quotaCacheEntry struct {
	Result  oneAPIQuotaResponse
	Expires time.Time
	Ready   chan struct{}
}

var personalQuotaCache = struct {
	sync.Mutex
	Entries map[quotaCacheKey]*quotaCacheEntry
}{Entries: map[quotaCacheKey]*quotaCacheEntry{}}

func validateQuotaValues(v quotaValues, username string) bool {
	if v.Username != username {
		return false
	}
	if v.Unlimited {
		return true
	}
	for _, value := range []*float64{v.Total, v.Used} {
		if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
			return false
		}
	}
	return v.Remaining != nil && !math.IsNaN(*v.Remaining) && !math.IsInf(*v.Remaining, 0)
}

func fetchPersonalQuota(ctx context.Context, client *http.Client, endpoint, username, credential string) oneAPIQuotaResponse {
	fail := oneAPIQuotaResponse{Status: "unavailable"}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fail
	}
	args := u.Query()
	args.Set("username", username)
	u.RawQuery = args.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fail
	}
	req.Header.Set("Authorization", strings.TrimSpace(credential))
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fail
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return oneAPIQuotaResponse{Status: "credential_invalid"}
	}
	if resp.StatusCode != 200 {
		return fail
	}
	var body struct {
		Code    int         `json:"code"`
		Success bool        `json:"success"`
		Data    quotaValues `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body) != nil {
		return fail
	}
	if body.Code == 401 || body.Code == 403 {
		return oneAPIQuotaResponse{Status: "credential_invalid"}
	}
	if body.Code != 200 || !body.Success || !validateQuotaValues(body.Data, username) {
		return fail
	}
	// Keep the provider identity out of the browser response.
	body.Data.Username = ""
	if body.Data.Unlimited {
		body.Data.Total = nil
		body.Data.Remaining = nil
		body.Data.Used = nil
	}
	now := time.Now().UTC()
	return oneAPIQuotaResponse{Status: "ready", Values: &body.Data, UpdatedAt: &now}
}

func cachedPersonalQuota(ctx context.Context, key quotaCacheKey, fetch func(context.Context) oneAPIQuotaResponse) oneAPIQuotaResponse {
	for {
		personalQuotaCache.Lock()
		entry := personalQuotaCache.Entries[key]
		if entry != nil && entry.Ready != nil {
			ready := entry.Ready
			personalQuotaCache.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return oneAPIQuotaResponse{Status: "unavailable"}
			}
		}
		if entry != nil && time.Now().Before(entry.Expires) {
			result := entry.Result
			personalQuotaCache.Unlock()
			return result
		}
		previous := oneAPIQuotaResponse{}
		if entry != nil {
			previous = entry.Result
		}
		if len(personalQuotaCache.Entries) >= 4096 {
			for k, e := range personalQuotaCache.Entries {
				if e.Ready == nil && !time.Now().Before(e.Expires) {
					delete(personalQuotaCache.Entries, k)
				}
			}
		}
		// Refuse unbounded growth rather than evict another in-flight request.
		if entry == nil && len(personalQuotaCache.Entries) >= 4096 {
			personalQuotaCache.Unlock()
			return oneAPIQuotaResponse{Status: "unavailable"}
		}
		next := &quotaCacheEntry{Ready: make(chan struct{})}
		personalQuotaCache.Entries[key] = next
		personalQuotaCache.Unlock()
		result := fetch(ctx)
		if result.Status == "unavailable" && previous.Values != nil {
			result = previous
			result.Status = "stale"
		}
		personalQuotaCache.Lock()
		next.Result = result
		next.Expires = time.Now().Add(oneAPIQuotaInterval)
		close(next.Ready)
		next.Ready = nil
		personalQuotaCache.Unlock()
		return result
	}
}

// This personal endpoint never accepts a client-selected username or task actor.
func (h *Handler) GetOneAPIQuota(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if isMachineCredentialActor(r) {
		writeError(w, 403, "human account required")
		return
	}
	if os.Getenv("MULTICA_ONEAPI_QUOTA_URL") == "" {
		writeJSON(w, 200, oneAPIQuotaResponse{Status: "disabled"})
		return
	}
	id, username, ok := h.duccIdentity(w, r)
	if !ok {
		return
	}
	var sealed []byte
	if err := h.DB.QueryRow(r.Context(), `SELECT sealed FROM ducc_credential WHERE user_id=$1`, id).Scan(&sealed); err != nil {
		writeJSON(w, 200, oneAPIQuotaResponse{Status: "unavailable"})
		return
	}
	if len(sealed) == 0 {
		writeJSON(w, 200, oneAPIQuotaResponse{Status: "not_linked"})
		return
	}
	key, err := secretbox.LoadKey("MULTICA_PLUGIN_SECRET_KEY")
	if err != nil {
		writeJSON(w, 200, oneAPIQuotaResponse{Status: "unavailable"})
		return
	}
	box, err := secretbox.New(key)
	if err != nil {
		writeJSON(w, 200, oneAPIQuotaResponse{Status: "unavailable"})
		return
	}
	plain, err := box.Open(sealed)
	if err != nil {
		writeJSON(w, 200, oneAPIQuotaResponse{Status: "credential_invalid"})
		return
	}
	endpoint := os.Getenv("MULTICA_ONEAPI_QUOTA_URL")
	if endpoint == "" {
		writeJSON(w, 200, oneAPIQuotaResponse{Status: "unavailable"})
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	result := cachedPersonalQuota(r.Context(), quotaCacheKey{User: id, Endpoint: endpoint, Credential: sha256.Sum256(sealed)}, func(ctx context.Context) oneAPIQuotaResponse {
		return fetchPersonalQuota(ctx, client, endpoint, username, string(plain))
	})
	writeJSON(w, 200, result)
}
