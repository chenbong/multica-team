package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const ducxInstaller = "https://baidu-cc-client.bj.bcebos.com/baidu-cx/install.sh"
const ducxDefaultModel = "gpt-6-sol"
const ducxOutputLimit = 256 * 1024

// Preparation and registration must agree even before the shell reloads PATH.
func installedBaiduExecutable(home, name string) string {
	product := ""
	switch name {
	case "ducc":
		product = "baidu-cc"
	case "ducx":
		product = "baidu-cx"
	default:
		return ""
	}
	for _, path := range []string{
		filepath.Join(home, "."+product, product, "bin", name),
		filepath.Join(home, ".comate", product, "bin", name),
	} {
		if s, err := os.Stat(path); err == nil && s.Mode().IsRegular() && s.Mode()&0111 != 0 {
			return path
		}
	}
	return ""
}

type ducxSetupOps struct {
	find    func(string) string
	install func(context.Context, string, func(string)) error
	run     func(context.Context, string, string, []string) ([]byte, error)
}

func defaultDucxSetupOps() ducxSetupOps {
	return ducxSetupOps{
		find: func(home string) string {
			if path, err := exec.LookPath("ducx"); err == nil {
				return path
			}
			return installedBaiduExecutable(home, "ducx")
		},
		install: func(ctx context.Context, home string, progress func(string)) error {
			return installBaiduRuntime(ctx, home, "ducx", ducxInstaller, progress)
		},
		run: runDucxSetupCommand,
	}
}

type ducxBoundedOutput struct {
	bytes.Buffer
	truncated bool
}

func (b *ducxBoundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := ducxOutputLimit - b.Len()
	if n > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func runDucxSetupCommand(ctx context.Context, executable, username string, args []string) ([]byte, error) {
	if executable == "" || !duccSafeName.MatchString(username) {
		return nil, errors.New("ducx_invalid_identity_or_executable")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	commandArgs := append([]string{"--username", username}, args...)
	// The wrapper recognizes `config model` only as the leading subcommand.
	// It writes HOME-local settings without authenticating. Doctor still needs
	// the explicit account so another user's default login is never selected.
	if len(args) == 3 && args[0] == "config" && args[1] == "model" {
		commandArgs = args
	}
	cmd := exec.CommandContext(ctx, executable, commandArgs...)
	// Installer shell edits are not inherited. Include sibling helpers without
	// changing this process's PATH or requiring a shell configuration reload.
	commandPath := filepath.Dir(executable)
	if inheritedPath := os.Getenv("PATH"); inheritedPath != "" {
		commandPath += string(os.PathListSeparator) + inheritedPath
	}
	cmd.Env = append(cmd.Environ(), "PATH="+commandPath)
	configureDuccProcess(cmd)
	var out ducxBoundedOutput
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("ducx_command_timed_out")
		}
		return nil, errors.New("ducx_command_failed")
	}
	if out.truncated {
		return nil, errors.New("ducx_output_too_large")
	}
	return out.Bytes(), nil
}

// Doctor checks provider configuration, not a paid model request or JWT signature.
// Its overall status may be a warning solely because an unrelated CDN is blocked.
func ducxProviderAuthReady(raw []byte) bool {
	for offset := bytes.IndexByte(raw, '{'); offset >= 0 && offset < len(raw); {
		var report struct {
			Checks map[string]struct {
				Status string `json:"status"`
			} `json:"checks"`
		}
		if json.NewDecoder(bytes.NewReader(raw[offset:])).Decode(&report) == nil && report.Checks != nil {
			return report.Checks["auth.credentials"].Status == "ok"
		}
		next := bytes.IndexByte(raw[offset+1:], '{')
		if next < 0 {
			break
		}
		offset += next + 1
	}
	return false
}

func readDucxSettings(home, name string) ([]byte, error) {
	dir := filepath.Join(home, ".baidu-cx")
	if s, err := os.Lstat(dir); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil || !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("ducx_unsafe_config_directory")
	}
	p := filepath.Join(dir, name)
	before, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !before.Mode().IsRegular() || before.Size() > 64*1024 {
		return nil, errors.New("ducx_unsafe_config_file")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, errors.New("ducx_config_unreadable")
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("ducx_config_changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return nil, errors.New("ducx_config_unreadable")
	}
	return data, nil
}

func configuredDucxModel(home string) (string, error) {
	data, err := readDucxSettings(home, "user.json")
	if err != nil {
		return "", err
	}
	var user struct {
		Model string `json:"model"`
	}
	if data != nil {
		if json.Unmarshal(data, &user) != nil {
			return "", errors.New("ducx_invalid_user_config")
		}
		if strings.TrimSpace(user.Model) != "" {
			return user.Model, nil
		}
	}
	data, err = readDucxSettings(home, "config.toml")
	if err != nil {
		return "", err
	}
	var config struct {
		Model string `toml:"model"`
	}
	if data != nil && toml.Unmarshal(data, &config) != nil {
		return "", errors.New("ducx_invalid_native_config")
	}
	return strings.TrimSpace(config.Model), nil
}

func prepareDucxRuntime(ctx context.Context, home, username string, progress func(string), ops ducxSetupOps) error {
	if !duccSafeName.MatchString(username) {
		return errors.New("ducx_invalid_username")
	}
	lock := filepath.Join(home, ".multica-ducx-setup-lock")
	if info, err := os.Lstat(lock); err == nil && info.IsDir() && time.Since(info.ModTime()) > 10*time.Minute {
		// A full preparation is bounded below ten minutes; never remove lock contents.
		_ = os.Remove(lock)
	}
	if err := os.Mkdir(lock, 0700); err != nil {
		return errors.New("ducx_setup_locked")
	}
	defer os.Remove(lock)
	executable := ops.find(home)
	if executable == "" {
		if err := ops.install(ctx, home, progress); err != nil {
			return errors.New("ducx_install_failed")
		}
		executable = ops.find(home)
		if executable == "" {
			return errors.New("ducx_executable_not_found_after_install")
		}
		emitDuccProgress(progress, "ducx 安装完成，无需手动 source shell 配置。")
	} else {
		emitDuccProgress(progress, "本机已安装 ducx，保留现有版本。")
	}
	// This is the same owner-resolved credential already managed for ducc.
	if _, err := readDuccCredential(home, username); err != nil {
		return errors.New("ducx_shared_credential_unavailable")
	}
	model, err := configuredDucxModel(home)
	if err != nil {
		return err
	}
	if model == "" {
		emitDuccProgress(progress, "正在设置 ducx 默认模型 gpt-6-sol（最多等待 45 秒）...")
		if _, err := ops.run(ctx, executable, username, []string{"config", "model", ducxDefaultModel}); err != nil {
			return errors.New("ducx_model_configuration_failed")
		}
		if model, err = configuredDucxModel(home); err != nil || model != ducxDefaultModel {
			return errors.New("ducx_model_configuration_not_applied")
		}
	} else {
		emitDuccProgress(progress, "ducx 已有模型配置，保留原值。")
	}
	emitDuccProgress(progress, "正在检查当前账号的 ducx 认证配置（最多等待 45 秒）...")
	data, err := ops.run(ctx, executable, username, []string{"doctor", "--json"})
	if err != nil {
		return errors.New("ducx_auth_check_failed")
	}
	if !ducxProviderAuthReady(data) {
		return errors.New("ducx_auth_not_ready_or_unsupported")
	}
	emitDuccProgress(progress, "ducx 认证配置检查通过；未发送模型请求，平台指定的智能体模型仍优先。")
	return nil
}
