package agent

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func oneAPITestCatalog(t *testing.T, handler http.HandlerFunc) *OneAPIModelCatalog {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	token, policy := filepath.Join(dir, "token"), filepath.Join(dir, "protocols.json")
	if err := os.WriteFile(token, []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy, []byte(`{"messages":["messages-model","dual-model"],"responses":["responses-model","dual-model"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c := NewOneAPIModelCatalog(server.URL+"/v1/models", token, policy, ca)
	return c
}

const oneAPIFixture = `{"data":[{"id":"messages-model"},{"id":"responses-model"},{"id":"dual-model"},{"id":"unknown-model"}]}`

func TestOneAPICatalogFilter(t *testing.T) {
	models := []oneAPIModel{{ID: "a"}, {ID: "b"}, {ID: "both"}, {ID: "unknown"}, {ID: "a"}}
	policy := oneAPIProtocols{Messages: []string{"a", "both", "not-in-api"}, Responses: []string{"b", "both"}}
	for protocol, want := range map[string][]string{"messages": {"a", "both"}, "responses": {"b", "both"}} {
		got := filterOneAPIModels(models, policy, protocol)
		var ids []string
		for _, m := range got {
			ids = append(ids, m.ID)
			if m.Default || m.Thinking != nil || m.Label != m.ID {
				t.Fatal("invented a model capability")
			}
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("%s: %v", protocol, ids)
		}
	}
	metadata := []oneAPIModel{{ID: "a", Endpoints: []string{"openai"}}, {ID: "b", Endpoints: []string{}}, {ID: "new", Endpoints: []string{"anthropic", "openai-response"}}}
	for _, protocol := range []string{"messages", "responses"} {
		got := filterOneAPIModels(metadata, policy, protocol)
		if len(got) != 1 || got[0].ID != "new" {
			t.Fatal("Chat-only or explicitly incompatible model included")
		}
	}
}

func TestOneAPICatalogCacheRefreshAndFileRotation(t *testing.T) {
	var calls atomic.Int32
	var lastToken atomic.Value
	c := oneAPITestCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		lastToken.Store(r.Header.Get("Authorization"))
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Error("unexpected request")
		}
		fmt.Fprint(w, oneAPIFixture)
	})
	get := func(protocol string, force bool) OneAPIModelResult {
		t.Helper()
		result, err := c.Models(context.Background(), protocol, force)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if get("messages", false).Cached {
		t.Fatal("cold response marked cached")
	}
	if !get("responses", false).Cached || calls.Load() != 1 {
		t.Fatal("protocols do not share fetch")
	}
	if get("messages", true).Cached || calls.Load() != 2 {
		t.Fatal("force did not refresh")
	}
	c.mu.Lock()
	c.storedAt = time.Now().Add(-6 * time.Minute)
	c.mu.Unlock()
	get("messages", false)
	if calls.Load() != 3 {
		t.Fatal("expired cache served")
	}
	if err := os.WriteFile(c.tokenFile, []byte("rotated-fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	get("messages", false)
	if calls.Load() != 4 || lastToken.Load() != "Bearer rotated-fixture-token" {
		t.Fatal("token rotation reused the previous credential/catalog")
	}
	if err := os.WriteFile(c.protocolsFile, []byte(`{"messages":["messages-model"],"responses":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	got := get("messages", false)
	if len(got.Models) != 1 || got.Models[0].ID != "messages-model" || calls.Load() != 4 {
		t.Fatal("policy updates need a restart")
	}
	got.Models[0].ID = "changed-by-caller"
	if get("messages", false).Models[0].ID != "messages-model" {
		t.Fatal("caller mutated cache")
	}
}

func TestOneAPICatalogFailuresArePrivateAndNotCached(t *testing.T) {
	for _, body := range []string{`private-body`, `{"data":[]}`, `{}`, `{"data":[{"id":12}]}`, `{"data":[{"id":" bad "}]}`, `{"data":[{"id":"bad\nname"}]}`, strings.Repeat("x", oneAPICatalogLimit+1)} {
		t.Run(fmt.Sprintf("body-%d-%d", len(body), body[0]), func(t *testing.T) {
			var fail atomic.Bool
			fail.Store(true)
			c := oneAPITestCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				if fail.Load() {
					fmt.Fprint(w, body)
				} else {
					fmt.Fprint(w, oneAPIFixture)
				}
			})
			_, err := c.Models(context.Background(), "responses", false)
			if err != errOneAPIFetch {
				t.Fatalf("unexpected error: %v", err)
			}
			fail.Store(false)
			if _, err = c.Models(context.Background(), "responses", false); err != nil {
				t.Fatal("failed request was cached")
			}
		})
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var fail atomic.Bool
			c := oneAPITestCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				if fail.Load() {
					w.WriteHeader(status)
					fmt.Fprint(w, "private-body")
				} else {
					fmt.Fprint(w, oneAPIFixture)
				}
			})
			if _, err := c.Models(context.Background(), "messages", false); err != nil {
				t.Fatal(err)
			}
			fail.Store(true)
			if _, err := c.Models(context.Background(), "messages", true); err != errOneAPIFetch {
				t.Fatal("upstream error leaked")
			}
			if _, err := c.Models(context.Background(), "messages", false); err != errOneAPIFetch {
				t.Fatal("revoked or failed catalog still served")
			}
		})
	}
}

func TestOneAPICatalogConfigurationSafety(t *testing.T) {
	if NewOneAPIModelCatalog("", "", "", "") != nil {
		t.Fatal("empty configuration must preserve upstream behavior")
	}
	for _, name := range []string{"http", "userinfo", "query", "token permissions", "token symlink", "empty token", "token newline", "invalid policy", "invalid protocol"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			c := oneAPITestCatalog(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, oneAPIFixture) })
			protocol := "messages"
			switch name {
			case "http":
				c.endpoint = strings.Replace(c.endpoint, "https:", "http:", 1)
			case "userinfo":
				c.endpoint = strings.Replace(c.endpoint, "https://", "https://fixture@", 1)
			case "query":
				c.endpoint += "?token=fixture"
			case "token permissions":
				if err := os.Chmod(c.tokenFile, 0644); err != nil {
					t.Fatal(err)
				}
			case "token symlink":
				link := c.tokenFile + "-link"
				if err := os.Symlink(c.tokenFile, link); err != nil {
					t.Skip(err)
				}
				c.tokenFile = link
			case "empty token":
				if err := os.WriteFile(c.tokenFile, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "token newline":
				if err := os.WriteFile(c.tokenFile, []byte("fixture\nheader"), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid policy":
				if err := os.WriteFile(c.protocolsFile, []byte("{bad"), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid protocol":
				protocol = "openai-chat"
			}
			if _, err := c.Models(context.Background(), protocol, false); err != errOneAPIConfig || calls.Load() != 0 {
				t.Fatal("unsafe configuration sent a request")
			}
		})
	}
}

func TestOneAPICatalogRejectsUntrustedTLSAndInvalidCA(t *testing.T) {
	c := oneAPITestCatalog(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, oneAPIFixture) })
	untrusted := NewOneAPIModelCatalog(c.endpoint, c.tokenFile, c.protocolsFile, "")
	if _, err := untrusted.Models(context.Background(), "messages", false); err != errOneAPIFetch {
		t.Fatal("untrusted certificate accepted")
	}
	invalid := NewOneAPIModelCatalog(c.endpoint, c.tokenFile, c.protocolsFile, c.tokenFile)
	if _, err := invalid.Models(context.Background(), "messages", false); err != errOneAPIConfig {
		t.Fatal("invalid CA file accepted")
	}
}

func TestOneAPICatalogNoRedirect(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	c := oneAPITestCatalog(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	if _, err := c.Models(context.Background(), "messages", false); err != errOneAPIFetch || redirected.Load() != 0 {
		t.Fatal("credential followed a redirect")
	}
}

func TestOneAPICatalogConcurrentRefresh(t *testing.T) {
	var calls atomic.Int32
	c := oneAPITestCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(150 * time.Millisecond)
		fmt.Fprint(w, oneAPIFixture)
	})
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := c.Models(context.Background(), "messages", true)
			if err != nil || len(got.Models) != 2 {
				t.Error("concurrent refresh failed")
			}
		}()
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent refresh made %d requests", calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Models(ctx, "messages", true); err != errOneAPIFetch {
		t.Fatal("cancellation ignored")
	}
	data, _ := json.Marshal(c)
	if strings.Contains(string(data), "fixture-token") {
		t.Fatal("credential exposed by serialization")
	}
}
