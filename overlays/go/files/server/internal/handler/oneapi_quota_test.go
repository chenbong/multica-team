package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQuotaProviderResponseValidation(t *testing.T) {
	cases := []struct {
		name, body, status string
		code               int
	}{
		{"normal", `{"code":200,"success":true,"data":{"username":"alice","is_unlimited":false,"month_quota_limit":9000,"used_quota":3800,"remaining_quota":5200}}`, "ready", 200},
		{"unlimited", `{"code":200,"success":true,"data":{"username":"alice","is_unlimited":true}}`, "ready", 200},
		{"other-account", `{"code":200,"success":true,"data":{"username":"bob","is_unlimited":true}}`, "unavailable", 200},
		{"missing-values", `{"code":200,"success":true,"data":{"username":"alice","is_unlimited":false}}`, "unavailable", 200},
		{"negative", `{"code":200,"success":true,"data":{"username":"alice","is_unlimited":false,"month_quota_limit":9,"used_quota":-1,"remaining_quota":10}}`, "unavailable", 200},
		{"invalid-json", `secret-error-not-for-browser`, "unavailable", 200},
		{"expired", `secret-error-not-for-browser`, "credential_invalid", 401},
		{"rejected", `secret-error-not-for-browser`, "credential_invalid", 403},
		{"provider-error", `secret-error-not-for-browser`, "unavailable", 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "fixture-login" || r.URL.Query().Get("username") != "alice" {
					t.Error("provider authorization/query mismatch")
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			result := fetchPersonalQuota(context.Background(), server.Client(), server.URL, "alice", "fixture-login")
			if result.Status != tc.status {
				t.Fatalf("status %s, want %s", result.Status, tc.status)
			}
			if result.Values != nil && result.Values.Username != "" {
				t.Fatal("provider identity returned")
			}
		})
	}
	if fetchPersonalQuota(context.Background(), http.DefaultClient, "http://example.invalid", "alice", "fixture").Status != "unavailable" {
		t.Fatal("plaintext endpoint accepted")
	}
}
func TestQuotaCacheCoalescesAndKeepsLastSuccess(t *testing.T) {
	key := quotaCacheKey{User: t.Name(), Endpoint: "test"}
	var calls atomic.Int32
	used, total, remaining := 3800.0, 9000.0, 5200.0
	now := time.Now()
	result := oneAPIQuotaResponse{Status: "ready", Values: &quotaValues{Used: &used, Total: &total, Remaining: &remaining}, UpdatedAt: &now}
	fetch := func(context.Context) oneAPIQuotaResponse {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return result
	}
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cachedPersonalQuota(context.Background(), key, fetch).Status != "ready" {
				t.Error("cache failed")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent provider queries were not coalesced")
	}
	personalQuotaCache.Lock()
	personalQuotaCache.Entries[key].Expires = time.Now().Add(-time.Second)
	personalQuotaCache.Unlock()
	stale := cachedPersonalQuota(context.Background(), key, func(context.Context) oneAPIQuotaResponse { return oneAPIQuotaResponse{Status: "unavailable"} })
	if stale.Status != "stale" || stale.Values.Used == nil || *stale.Values.Used != 3800 || !stale.UpdatedAt.Equal(now) {
		t.Fatal("transient failure lost last successful values")
	}
	changed := key
	changed.Credential[0] = 1
	if cachedPersonalQuota(context.Background(), changed, func(context.Context) oneAPIQuotaResponse { return oneAPIQuotaResponse{Status: "credential_invalid"} }).Values != nil {
		t.Fatal("credential change reused previous data")
	}
	if strings.Contains(stale.Status, "fixture") {
		t.Fatal("credential leaked")
	}
	personalQuotaCache.Lock()
	delete(personalQuotaCache.Entries, key)
	delete(personalQuotaCache.Entries, changed)
	personalQuotaCache.Unlock()
}
