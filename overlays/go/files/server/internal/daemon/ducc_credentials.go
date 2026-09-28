package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const duccInstaller = "https://baidu-cc-client.bj.bcebos.com/baidu-cc/install.sh"

var duccSafeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

type duccSync struct {
	Daemon          string `json:"daemon_id"`
	Name            string `json:"name"`
	ClientVersion   string `json:"client_version"`
	Phase           string `json:"phase"`
	Installed       bool   `json:"installed"`
	State           string `json:"state"`
	Error           string `json:"error_code"`
	Credential      string `json:"credential,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
	ExpectedVersion int64  `json:"expected_version"`
}
type duccReply struct {
	Username   string `json:"username"`
	Enabled    bool   `json:"enabled"`
	Action     string `json:"action"`
	RequestID  string `json:"request_id"`
	Version    int64  `json:"version"`
	Credential string `json:"credential"`
}

// No redirects, response bodies, tokens or subprocess output enter errors/logs.
func duccExchange(ctx context.Context, c *Client, req duccSync) (duccReply, error) {
	var reply duccReply
	raw, err := json.Marshal(req)
	if err != nil {
		return reply, errors.New("sync_encode_failed")
	}
	r, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/daemon/ducc/sync", bytes.NewReader(raw))
	if err != nil {
		return reply, errors.New("sync_url_invalid")
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	c.setIdentityHeaders(r)
	hc := *c.client
	hc.Timeout = 20 * time.Second
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(r)
	if err != nil {
		return reply, errors.New("sync_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return reply, fmt.Errorf("sync_http_%d", resp.StatusCode)
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 32*1024)).Decode(&reply) != nil || !duccSafeName.MatchString(reply.Username) {
		return duccReply{}, errors.New("sync_response_invalid")
	}
	return reply, nil
}

func duccExecutable(home string) string {
	if path, err := exec.LookPath("ducc"); err == nil {
		return path
	}
	for _, path := range []string{filepath.Join(home, ".baidu-cc/baidu-cc/bin/ducc"), filepath.Join(home, ".comate/baidu-cc/bin/ducc")} {
		if s, e := os.Stat(path); e == nil && !s.IsDir() && s.Mode()&0111 != 0 {
			return path
		}
	}
	return ""
}

func duccCredentialDir(home, username string) (string, error) {
	if !duccSafeName.MatchString(username) {
		return "", errors.New("invalid_username")
	}
	for _, dir := range []string{filepath.Join(home, ".comate"), filepath.Join(home, ".comate/login-user")} {
		s, e := os.Lstat(dir)
		if os.IsNotExist(e) {
			if e = os.Mkdir(dir, 0700); e != nil {
				return "", errors.New("credential_directory_unavailable")
			}
		} else if e != nil || !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("unsafe_credential_directory")
		}
	}
	return filepath.Join(home, ".comate/login-user"), nil
}

func readDuccCredential(home, username string) ([]byte, error) {
	if !duccSafeName.MatchString(username) {
		return nil, errors.New("invalid_username")
	}
	for _, dir := range []string{filepath.Join(home, ".comate"), filepath.Join(home, ".comate/login-user")} {
		s, e := os.Lstat(dir)
		if e != nil {
			return nil, e
		}
		if !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe_credential_directory")
		}
	}
	path := filepath.Join(home, ".comate/login-user", username)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > 16384 {
		return nil, errors.New("invalid_credential_file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("credential_changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(data) == 0 || len(data) > 16384 {
		return nil, errors.New("invalid_credential_file")
	}
	return data, nil
}

func writeDuccCredential(home, username string, data []byte) error {
	if len(data) == 0 || len(data) > 16384 {
		return errors.New("invalid_credential_file")
	}
	dir, err := duccCredentialDir(home, username)
	if err != nil {
		return err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return errors.New("credential_permissions_failed")
	}
	temp, err := os.CreateTemp(dir, ".multica-credential-*")
	if err != nil {
		return errors.New("credential_write_failed")
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(0600); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil || closeErr != nil {
		return errors.New("credential_write_failed")
	}
	// link(2) publishes a complete file and fails if any existing credential won.
	if err = os.Link(temp.Name(), filepath.Join(dir, username)); err != nil {
		return errors.New("credential_conflict")
	}
	return nil
}

type duccBoundedOutput struct{ bytes.Buffer }

func (b *duccBoundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 16384 {
		end := n
		if end > 16384-b.Len() {
			end = 16384 - b.Len()
		}
		b.Buffer.Write(p[:end])
	}
	return n, nil
}

// The CLI is the authority on usable provider authentication. The result is
// not advertised as independent JWT signature or account-identity validation.
func checkDuccAuth(ctx context.Context, executable, username string) bool {
	if executable == "" || !duccSafeName.MatchString(username) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "--username", username, "auth", "status")
	var out duccBoundedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return false
	}
	var status struct {
		LoggedIn bool   `json:"loggedIn"`
		Method   string `json:"authMethod"`
	}
	return json.Unmarshal(out.Bytes(), &status) == nil && status.LoggedIn && status.Method == "api_key_helper"
}

func installDucc(ctx context.Context) error {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return errors.New("unsupported_os")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.New("install_failed")
	}
	lock := filepath.Join(home, ".multica-ducc-install-lock")
	if info, e := os.Stat(lock); e == nil && time.Since(info.ModTime()) > 10*time.Minute {
		_ = os.Remove(lock)
	}
	if err = os.Mkdir(lock, 0700); err != nil {
		return errors.New("install_failed")
	}
	defer os.Remove(lock)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", duccInstaller, nil)
	hc := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if r.URL.Scheme != "https" {
			return errors.New("insecure_redirect")
		}
		return nil
	}}
	resp, err := hc.Do(req)
	if err != nil {
		return errors.New("install_failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("install_failed")
	}
	script, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(script) > 1024*1024 {
		return errors.New("install_failed")
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", "-s")
	cmd.Stdin = bytes.NewReader(script)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return errors.New("install_failed")
	}
	return nil
}

func prepareDuccOnce(ctx context.Context, c *Client, daemonID, version string, allowInstall bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.New("home_unavailable")
	}
	name, _ := os.Hostname()
	executable := duccExecutable(home)
	req := duccSync{Daemon: daemonID, Name: name, ClientVersion: version, Phase: "poll", State: "unknown", Installed: executable != ""}
	reply, err := duccExchange(ctx, c, req)
	if err != nil {
		return err
	}
	if !reply.Enabled && reply.RequestID == "" {
		return nil
	}
	// Do not read any local credential before resolving the server-owned identity.
	req.Installed = executable != ""
	data, readErr := readDuccCredential(home, reply.Username)
	if os.IsNotExist(readErr) {
		req.State = "missing"
	} else if readErr != nil {
		req.State = "invalid"
		req.Error = "credential_invalid"
	} else {
		req.State = "present"
	}
	reply, err = duccExchange(ctx, c, req)
	if err != nil {
		return err
	}
	if reply.Action == "install" && allowInstall {
		req.State = "installing"
		req.Phase = "report"
		_, _ = duccExchange(ctx, c, req)
		if err = installDucc(ctx); err != nil {
			req.State = "install_failed"
			req.Error = "install_failed"
			_, _ = duccExchange(ctx, c, req)
			return errors.New("install_failed")
		}
		executable = duccExecutable(home)
		if executable == "" {
			return errors.New("install_failed")
		}
		// Registration probes this process's PATH; installer shell edits are not inherited.
		dir := filepath.Dir(executable)
		_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		req.Installed = true
		req.State = "missing"
		if len(data) > 0 {
			req.State = "present"
		}
		req.Phase = "poll"
		reply, err = duccExchange(ctx, c, req)
		if err != nil {
			return err
		}
	}
	req.RequestID = reply.RequestID
	req.ExpectedVersion = reply.Version
	switch reply.Action {
	case "import":
		if !checkDuccAuth(ctx, executable, reply.Username) {
			req.State = "invalid"
			req.Error = "validation_failed"
			req.Phase = "report"
			_, _ = duccExchange(ctx, c, req)
			return errors.New("validation_failed")
		}
		latest, e := readDuccCredential(home, reply.Username)
		if e != nil || !bytes.Equal(data, latest) {
			return errors.New("credential_conflict")
		}
		req.Phase = "upload"
		req.State = "ready"
		req.Credential = string(data)
		_, err = duccExchange(ctx, c, req)
	case "download":
		req.Phase = "download"
		reply, err = duccExchange(ctx, c, req)
		if err != nil {
			return err
		}
		if err = writeDuccCredential(home, reply.Username, []byte(reply.Credential)); err != nil {
			return err
		}
		req.Credential = ""
		req.Phase = "report"
		req.State = "ready"
		req.Error = ""
		if !checkDuccAuth(ctx, executable, reply.Username) {
			req.State = "invalid"
			req.Error = "validation_failed"
		}
		_, err = duccExchange(ctx, c, req)
	}
	return err
}

// PrepareDucc is used before daemon startup, so an absent runtime cannot block
// the authenticated preparation path. Existing daemons also poll for imports.
func PrepareDucc(ctx context.Context, serverURL, token, daemonID, version string) error {
	c := NewClient(strings.TrimRight(serverURL, "/"))
	c.SetToken(token)
	return prepareDuccOnce(ctx, c, daemonID, version, true)
}

func (d *Daemon) duccCredentialLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := prepareDuccOnce(ctx, d.client, d.cfg.DaemonID, d.cfg.CLIVersion, true); err != nil {
			d.logger.Debug("ducc credential preparation deferred", "code", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
