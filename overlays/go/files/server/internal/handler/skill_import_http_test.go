package handler

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSkillImportHTTP_UnconfiguredPreservesClient(t *testing.T) {
	t.Setenv(skillImportProxyEnv, "")
	t.Setenv("GITHUB_TOKEN", "")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 17 * time.Second, Transport: transport, Jar: jar}
	if got := skillImportHTTPClient(client); got != client {
		t.Fatal("unset proxy must preserve the original client and transport")
	}
	t.Setenv(skillImportProxyEnv, " \t ")
	if skillImportHTTPClient(client) != client {
		t.Fatal("blank proxy must preserve the original client")
	}
}

func TestSkillImportHTTP_ProxyRoutesOnlyExactGitHubHosts(t *testing.T) {
	for _, legacyProxy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_proxy_%t", legacyProxy), func(t *testing.T) {
			dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Fixture-Route", "dedicated")
			}))
			t.Cleanup(dedicated.Close)
			original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Fixture-Route", "original")
			}))
			t.Cleanup(original.Close)
			t.Setenv(skillImportProxyEnv, dedicated.URL)
			legacyURL, _ := url.Parse(original.URL)
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.Proxy = nil
			if legacyProxy {
				base.Proxy = http.ProxyURL(legacyURL)
			} else {
				base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					if addr == strings.TrimPrefix(dedicated.URL, "http://") {
						return (&net.Dialer{}).DialContext(ctx, network, addr)
					}
					return (&net.Dialer{}).DialContext(ctx, network, original.Listener.Addr().String())
				}
			}
			base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			base.ResponseHeaderTimeout = 3 * time.Second
			jar, _ := cookiejar.New(nil)
			redirectErr := errors.New("fixture redirect policy")
			client := &http.Client{Timeout: 17 * time.Second, Transport: base, Jar: jar,
				CheckRedirect: func(*http.Request, []*http.Request) error { return redirectErr }}
			global := http.DefaultTransport
			configured := skillImportHTTPClient(client)
			t.Cleanup(configured.CloseIdleConnections)
			t.Cleanup(base.CloseIdleConnections)
			transport := configured.Transport.(*skillImportHTTPTransport)
			if configured.Timeout != client.Timeout || configured.Jar != jar || configured.CheckRedirect(nil, nil) != redirectErr ||
				client.Transport != base || http.DefaultTransport != global || transport.base != base {
				t.Fatal("client settings or shared transport changed")
			}
			if transport.github.TLSClientConfig.InsecureSkipVerify || transport.github.TLSClientConfig.MinVersion != tls.VersionTLS12 ||
				transport.github.ResponseHeaderTimeout != base.ResponseHeaderTimeout {
				t.Fatal("TLS verification or transport settings changed")
			}
			for _, tc := range []struct{ host, route string }{
				{"api.github.com", "dedicated"}, {"raw.githubusercontent.com", "dedicated"}, {"codeload.github.com", "dedicated"},
				{"github.com", "dedicated"}, {"API.GITHUB.COM", "dedicated"}, {"service.internal.invalid", "original"},
				{"clawhub.ai", "original"}, {"skills.sh", "original"}, {"github.com.evil.invalid", "original"},
				{"api.github.com.evil.invalid", "original"}, {"child.raw.githubusercontent.com", "original"}, {"www.github.com", "original"},
			} {
				resp, err := configured.Get("http://" + tc.host + "/fixture")
				if err != nil {
					t.Fatalf("fixture request for %s: %v", tc.host, err)
				}
				resp.Body.Close()
				if got := resp.Header.Get("X-Fixture-Route"); got != tc.route {
					t.Fatalf("%s route = %s, want %s", tc.host, got, tc.route)
				}
			}
			if skillImportHTTPClient(configured) != configured {
				t.Fatal("client wrapped more than once")
			}
		})
	}
}

func TestSkillImportHTTP_InvalidProxyIsSafeAndHostScoped(t *testing.T) {
	for i, raw := range []string{"http://fixture-user:fixture-secret@proxy.invalid:bad", "http://fixture-user:fixture-secret@proxy.invalid:65536",
		"http://fixture-user:fixture-secret@", "socks5://fixture-user:fixture-secret@proxy.invalid", "proxy.invalid", "http://proxy.invalid/fixture-secret", "http://proxy.invalid?fixture-secret"} {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			t.Setenv(skillImportProxyEnv, raw)
			legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			t.Cleanup(legacy.Close)
			legacyURL, _ := url.Parse(legacy.URL)
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.Proxy = http.ProxyURL(legacyURL)
			t.Cleanup(base.CloseIdleConnections)
			client := skillImportHTTPClient(&http.Client{Transport: base})
			_, err := client.Get("https://raw.githubusercontent.com/fixture/repo/main/SKILL.md")
			if !errors.Is(err, errSkillImportProxyConfig) {
				t.Fatalf("invalid proxy error = %v", err)
			}
			for _, secret := range []string{raw, "fixture-user", "fixture-secret", "proxy.invalid"} {
				if strings.Contains(fmt.Sprintf("%+v", err), secret) {
					t.Fatalf("proxy information leaked: %v", err)
				}
			}
			resp, err := client.Get("http://service.internal.invalid/fixture")
			if err != nil {
				t.Fatalf("invalid dedicated proxy affected another host: %v", err)
			}
			resp.Body.Close()
		})
	}
}

func TestSkillImportHTTP_RedirectCannotLeakGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-github-token")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Hostname() == "raw.githubusercontent.com" {
			if r.Header.Get("Authorization") != "Bearer fixture-github-token" {
				t.Error("raw host lost its GitHub token")
			}
			http.Redirect(w, r, "http://child.raw.githubusercontent.com/fixture", http.StatusTemporaryRedirect)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("GitHub token reached an unapproved redirect host")
		}
	}))
	t.Cleanup(proxy.Close)
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("dedicated_%t", configured), func(t *testing.T) {
			t.Setenv(skillImportProxyEnv, "")
			if configured {
				t.Setenv(skillImportProxyEnv, proxy.URL)
			}
			legacyURL, _ := url.Parse(proxy.URL)
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.Proxy = http.ProxyURL(legacyURL)
			t.Cleanup(base.CloseIdleConnections)
			client := skillImportHTTPClient(&http.Client{Transport: base})
			t.Cleanup(client.CloseIdleConnections)
			req, _ := http.NewRequest(http.MethodGet, "http://raw.githubusercontent.com/fixture", nil)
			req.Header.Set("Authorization", "Bearer fixture-github-token")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if req.Header.Get("Authorization") != "Bearer fixture-github-token" {
				t.Fatal("original request was modified")
			}
		})
	}
}

func TestSkillImportHTTP_TLSVerificationRemainsEnabled(t *testing.T) {
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(proxy.Close)
	t.Setenv(skillImportProxyEnv, proxy.URL)
	client := skillImportHTTPClient(&http.Client{Timeout: time.Second})
	t.Cleanup(client.CloseIdleConnections)
	_, err := client.Get("https://api.github.com/fixture")
	if err == nil {
		t.Fatal("untrusted HTTPS proxy certificate was accepted")
	}
	if strings.Contains(err.Error(), proxy.URL) || !strings.Contains(err.Error(), "TLS certificate verification failed") {
		t.Fatalf("unsafe or inaccurate TLS error: %v", err)
	}
}

func TestSkillImportHTTP_HTTPSUsesDedicatedCONNECTProxy(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fixture-Host", r.Host)
		w.Write([]byte("fixture body"))
	}))
	t.Cleanup(origin.Close)
	proxy := newSkillImportConnectProxy(t, origin.Listener.Addr().String())
	t.Setenv(skillImportProxyEnv, proxy.URL)
	base := origin.Client().Transport.(*http.Transport).Clone()
	// Use the fixture CA and certificate name, with certificate verification
	// enabled. The CONNECT destination must still be the requested GitHub host.
	base.TLSClientConfig.ServerName, _, _ = net.SplitHostPort(origin.Listener.Addr().String())
	client := skillImportHTTPClient(&http.Client{Transport: base, Timeout: 2 * time.Second})
	t.Cleanup(client.CloseIdleConnections)
	for _, host := range []string{"api.github.com", "raw.githubusercontent.com", "codeload.github.com", "github.com"} {
		resp, err := client.Get("https://" + host + "/fixture")
		if err != nil {
			t.Fatalf("HTTPS fixture for %s: %v", host, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "fixture body" || resp.Header.Get("X-Fixture-Host") != host {
			t.Fatalf("incorrect HTTPS response for %s", host)
		}
	}
	base.TLSClientConfig.ServerName = ""
	verify := skillImportHTTPClient(&http.Client{Transport: base, Timeout: 2 * time.Second})
	t.Cleanup(verify.CloseIdleConnections)
	_, err := verify.Get("https://github.com/fixture")
	if err == nil || !strings.Contains(err.Error(), "TLS certificate verification failed") {
		t.Fatalf("origin certificate name was not checked: %v", err)
	}
}

func newSkillImportConnectProxy(t *testing.T, target string) *httptest.Server {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || !isSkillImportGitHubHost(strings.TrimSuffix(r.Host, ":443")) {
			t.Errorf("unexpected fixture CONNECT destination: %s %s", r.Method, r.Host)
			http.Error(w, "fixture destination rejected", http.StatusBadGateway)
			return
		}
		upstream, err := net.DialTimeout("tcp", target, time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n")
		if err := buffered.Flush(); err != nil {
			return
		}
		go func() { _, _ = io.Copy(upstream, buffered) }()
		_, _ = io.Copy(conn, upstream)
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

func TestSkillImportHTTP_NetworkErrorsRetainClassificationWithoutSecrets(t *testing.T) {
	secretURL := "http://fixture-user:fixture-secret@proxy.invalid/fixture"
	for _, tc := range []struct {
		name    string
		cause   error
		status  int
		message string
	}{
		{"network", errors.New("proxyconnect tcp: " + secretURL), http.StatusBadGateway, "network request failed"},
		{"canceled", fmt.Errorf("%s: %w", secretURL, context.Canceled), http.StatusBadGateway, "canceled"},
		{"deadline", fmt.Errorf("%s: %w", secretURL, context.DeadlineExceeded), http.StatusGatewayTimeout, "timed out"},
		{"client_timeout", &net.DNSError{Name: secretURL, IsTimeout: true}, http.StatusGatewayTimeout, "timed out"},
		{"dns", &net.DNSError{Name: secretURL}, http.StatusBadGateway, "DNS lookup failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := sanitizeSkillImportNetworkError(&url.Error{Op: "Get", URL: secretURL, Err: tc.cause})
			status, msg, classified := skillImportHTTPErrorResponse(err)
			if !classified || status != tc.status || !strings.Contains(msg, tc.message) {
				t.Fatalf("response = %d %q %t", status, msg, classified)
			}
			if strings.Contains(msg, "fixture-secret") || strings.Contains(msg, "proxy.invalid") || strings.Contains(msg, "fixture-user") {
				t.Fatalf("proxy error leaked: %s", msg)
			}
			if !errors.Is(err, tc.cause) {
				t.Fatal("original error identity was lost")
			}
		})
	}
}

func TestSkillImportHTTP_StatusErrors(t *testing.T) {
	for _, tc := range []struct {
		status, response int
		headers          http.Header
		message          string
	}{
		{404, 502, nil, "HTTP 404"}, {401, 502, nil, "access denied"}, {403, 502, nil, "repository permissions"},
		{429, 429, nil, "rate limit"}, {503, 503, nil, "retry later"},
		{403, 429, http.Header{"X-Ratelimit-Remaining": {"0"}}, "rate limit"},
		{403, 429, http.Header{"Retry-After": {"fixture-secret"}}, "rate limit"},
	} {
		t.Run(fmt.Sprintf("HTTP_%d_%d", tc.status, tc.response), func(t *testing.T) {
			err := fmt.Errorf("fetch SKILL.md: %w", skillImportHTTPStatusFailure(tc.status, tc.headers, true))
			status, msg, ok := skillImportHTTPErrorResponse(err)
			if !ok || status != tc.response || !strings.Contains(msg, tc.message) || strings.Contains(msg, "fixture-secret") {
				t.Fatalf("response = %d %q %t", status, msg, ok)
			}
			if isSkillImportNotFound(err) != (tc.status == http.StatusNotFound) {
				t.Fatal("only a real HTTP 404 may mean not found")
			}
		})
	}
	legacy := skillImportHTTPStatusFailure(http.StatusForbidden, nil, false)
	if legacy.Error() != "HTTP 403" {
		t.Fatal("non-GitHub error text changed")
	}
	if _, _, ok := skillImportHTTPErrorResponse(legacy); ok {
		t.Fatal("non-GitHub status got GitHub classification")
	}
}

func TestSkillImportHTTP_BodyErrorIsSanitized(t *testing.T) {
	cause := errors.New("fixture-user:fixture-secret@proxy.invalid")
	body := skillImportHTTPBody{ReadCloser: skillImportFailingBody{err: cause}}
	_, err := io.ReadAll(body)
	if !errors.Is(err, cause) || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("body error lost identity or leaked: %v", err)
	}
}

type skillImportFailingBody struct{ err error }

func (b skillImportFailingBody) Read([]byte) (int, error) { return 0, b.err }
func (b skillImportFailingBody) Close() error             { return nil }
