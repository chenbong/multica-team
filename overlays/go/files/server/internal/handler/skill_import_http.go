package handler

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const skillImportProxyEnv = "MULTICA_SKILL_IMPORT_PROXY"

var errSkillImportProxyConfig = errors.New("invalid MULTICA_SKILL_IMPORT_PROXY; configure an absolute HTTP(S) proxy URL with a host and a valid port")

func isSkillImportGitHubHost(host string) bool {
	switch strings.ToLower(host) {
	case "api.github.com", "raw.githubusercontent.com", "codeload.github.com", "github.com":
		return true
	default:
		return false
	}
}

func isSkillImportGitHubAuthHost(host string) bool {
	return strings.EqualFold(host, "api.github.com") || strings.EqualFold(host, "raw.githubusercontent.com")
}

func parseSkillImportProxy(raw string) (*url.URL, error) {
	proxy, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || proxy.Hostname() == "" || (proxy.Scheme != "http" && proxy.Scheme != "https") ||
		proxy.Opaque != "" || (proxy.Path != "" && proxy.Path != "/") || proxy.RawQuery != "" || proxy.ForceQuery || proxy.Fragment != "" {
		// Never return the parser error: it may quote the proxy credentials.
		return nil, errSkillImportProxyConfig
	}
	if port := proxy.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errSkillImportProxyConfig
		}
	}
	return proxy, nil
}

// skillImportHTTPClient preserves the caller's timeout, redirects, cookies and
// transport settings. Only GitHub import traffic uses the dedicated proxy;
// other hosts keep the original transport, including its environment proxy.
func skillImportHTTPClient(client *http.Client) *http.Client {
	raw := strings.TrimSpace(os.Getenv(skillImportProxyEnv))
	if raw == "" && strings.TrimSpace(os.Getenv("GITHUB_TOKEN")) == "" {
		return client
	}
	if _, configured := client.Transport.(*skillImportHTTPTransport); configured {
		return client
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var configErr error
	var github *http.Transport
	if raw != "" {
		var proxy *url.URL
		proxy, configErr = parseSkillImportProxy(raw)
		if configErr == nil {
			if transport, ok := base.(*http.Transport); ok {
				github = transport.Clone()
				github.Proxy = http.ProxyURL(proxy)
			} else {
				configErr = errors.New("MULTICA_SKILL_IMPORT_PROXY requires an HTTP transport for GitHub skill imports")
			}
		}
	}
	copy := *client
	copy.Transport = &skillImportHTTPTransport{base: base, github: github, configErr: configErr}
	return &copy
}

type skillImportHTTPTransport struct {
	base      http.RoundTripper
	github    *http.Transport
	configErr error
}

func (t *skillImportHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Go may copy Authorization to subdomains on redirects. A GitHub token
	// must stay within the existing exact API/raw host allowlist.
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" &&
		!isSkillImportGitHubAuthHost(req.URL.Hostname()) && req.Header.Get("Authorization") == "Bearer "+token {
		req = req.Clone(req.Context())
		req.Header.Del("Authorization")
	}
	if !isSkillImportGitHubHost(req.URL.Hostname()) {
		return t.base.RoundTrip(req)
	}
	if t.configErr != nil {
		return nil, t.configErr
	}
	transport := t.base
	if t.github != nil {
		transport = t.github
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return nil, sanitizeSkillImportNetworkError(err)
	}
	resp.Body = skillImportHTTPBody{ReadCloser: resp.Body}
	return resp, nil
}

func (t *skillImportHTTPTransport) CloseIdleConnections() {
	if t.github != nil {
		t.github.CloseIdleConnections()
	}
}

type skillImportHTTPBody struct{ io.ReadCloser }

func (b skillImportHTTPBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		err = sanitizeSkillImportNetworkError(err)
	}
	return n, err
}

// The cause remains available to errors.Is/As, while log/API messages never
// include a transport error's proxy URL, credentials, or dial address.
type skillImportNetworkError struct{ cause error }

func (e *skillImportNetworkError) Unwrap() error { return e.cause }

func (e *skillImportNetworkError) Error() string {
	if errors.Is(e.cause, context.Canceled) {
		return "GitHub skill import request canceled"
	}
	var netErr net.Error
	if errors.Is(e.cause, context.DeadlineExceeded) || errors.As(e.cause, &netErr) && netErr.Timeout() {
		return "GitHub skill import request timed out; check server connectivity and MULTICA_SKILL_IMPORT_PROXY, then retry"
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(e.cause, &certErr) {
		return "GitHub skill import TLS certificate verification failed; check the server's trusted CA certificates"
	}
	var dnsErr *net.DNSError
	if errors.As(e.cause, &dnsErr) {
		return "GitHub skill import DNS lookup failed; check server DNS and MULTICA_SKILL_IMPORT_PROXY, then retry"
	}
	return "GitHub skill import network request failed; check server connectivity and MULTICA_SKILL_IMPORT_PROXY, then retry"
}

func sanitizeSkillImportNetworkError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errSkillImportProxyConfig) {
		return errSkillImportProxyConfig
	}
	var safe *skillImportNetworkError
	if errors.As(err, &safe) {
		return safe
	}
	return &skillImportNetworkError{cause: err}
}

type skillImportHTTPStatusError struct {
	StatusCode  int
	GitHub      bool
	RateLimited bool
}

func (e *skillImportHTTPStatusError) Error() string {
	prefix := fmt.Sprintf("HTTP %d", e.StatusCode)
	if !e.GitHub {
		return prefix
	}
	switch {
	case e.RateLimited:
		return prefix + ": GitHub rate limit exceeded; retry later or configure GITHUB_TOKEN on the server"
	case e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden:
		return prefix + ": GitHub access denied; check GITHUB_TOKEN and repository permissions"
	case e.StatusCode >= 500:
		return prefix + ": GitHub source temporarily unavailable; retry later"
	default:
		return prefix
	}
}

func skillImportHTTPStatusFailure(status int, headers http.Header, github bool) error {
	return &skillImportHTTPStatusError{
		StatusCode: status,
		GitHub:     github,
		RateLimited: github && (status == http.StatusTooManyRequests ||
			status == http.StatusForbidden && (headers.Get("X-RateLimit-Remaining") == "0" || headers.Get("Retry-After") != "")),
	}
}

func isSkillImportNotFound(err error) bool {
	var status *skillImportHTTPStatusError
	return errors.As(err, &status) && status.StatusCode == http.StatusNotFound
}

// skillImportHTTPErrorResponse classifies upstream failures without confusing
// GitHub authorization with the user's authorization to this API.
func skillImportHTTPErrorResponse(err error) (int, string, bool) {
	if errors.Is(err, errSkillImportProxyConfig) {
		return http.StatusInternalServerError, errSkillImportProxyConfig.Error(), true
	}
	var safe *skillImportNetworkError
	githubNetwork := errors.As(err, &safe)
	var netErr net.Error
	if githubNetwork && (errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout()) {
		return http.StatusGatewayTimeout, sanitizeSkillImportNetworkError(err).Error(), true
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusBadGateway, "skill import request canceled", true
	}
	var status *skillImportHTTPStatusError
	if errors.As(err, &status) && status.GitHub {
		switch {
		case status.RateLimited:
			return http.StatusTooManyRequests, err.Error(), true
		case status.StatusCode >= 500:
			return http.StatusServiceUnavailable, err.Error(), true
		default:
			return http.StatusBadGateway, err.Error(), true
		}
	}
	if githubNetwork {
		return http.StatusBadGateway, err.Error(), true
	}
	return 0, "", false
}
