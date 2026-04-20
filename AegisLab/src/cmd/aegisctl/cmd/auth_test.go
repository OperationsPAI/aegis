package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/config"
	"aegis/cmd/aegisctl/output"
)

func TestAuthLoginWithPasswordStdinSavesContextAndPrintsJSON(t *testing.T) {
	resetAuthLoginState(t)
	prev := passwordLoginFunc
	t.Cleanup(func() {
		passwordLoginFunc = prev
	})

	t.Setenv("HOME", t.TempDir())
	cfg = &config.Config{Contexts: map[string]config.Context{
		"bootstrap": {DefaultProject: "pair_diagnosis"},
	}}
	flagOutput = "json"
	authLoginServer = "http://127.0.0.1:8082"
	authLoginContext = "bootstrap"
	authLoginUsername = "admin"
	authLoginPasswordStdin = true
	authLoginCmd.SetIn(strings.NewReader("bootstrap-secret\n"))

	expiresAt := time.Date(2026, 4, 20, 12, 30, 0, 0, time.UTC)
	passwordLoginFunc = func(server, username, password string) (*client.LoginResult, error) {
		if server != authLoginServer {
			t.Fatalf("unexpected server: %q", server)
		}
		if username != "admin" {
			t.Fatalf("unexpected username: %q", username)
		}
		if password != "bootstrap-secret" {
			t.Fatalf("unexpected password: %q", password)
		}
		return &client.LoginResult{
			Token:     "jwt-token",
			ExpiresAt: expiresAt,
			AuthType:  "password",
			Username:  username,
		}, nil
	}

	stdout, stderr, err := captureCommandOutput(func() error {
		return authLoginCmd.RunE(authLoginCmd, nil)
	})
	if err != nil {
		t.Fatalf("auth login returned error: %v", err)
	}
	if strings.Contains(stderr, "bootstrap-secret") {
		t.Fatal("password leaked to stderr")
	}

	var payload authLoginJSONResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode json output: %v\noutput=%q", err, stdout)
	}
	if payload.Context != "bootstrap" || payload.Server != authLoginServer || payload.Username != "admin" || payload.AuthType != "password" {
		t.Fatalf("unexpected json payload: %+v", payload)
	}
	if payload.ExpiresAt != expiresAt.Format(time.RFC3339) {
		t.Fatalf("unexpected expires_at: %q", payload.ExpiresAt)
	}

	savedCfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	ctx := savedCfg.Contexts["bootstrap"]
	if ctx.Server != authLoginServer || ctx.Token != "jwt-token" || ctx.AuthType != "password" {
		t.Fatalf("unexpected saved context: %+v", ctx)
	}
	if ctx.DefaultProject != "pair_diagnosis" {
		t.Fatalf("default project should be preserved, got %q", ctx.DefaultProject)
	}
	if savedCfg.CurrentContext != "bootstrap" {
		t.Fatalf("unexpected current context: %q", savedCfg.CurrentContext)
	}
}

func TestAuthLoginWithPasswordFile(t *testing.T) {
	resetAuthLoginState(t)
	prev := passwordLoginFunc
	t.Cleanup(func() {
		passwordLoginFunc = prev
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg = &config.Config{Contexts: map[string]config.Context{}}
	flagOutput = "json"
	authLoginServer = "http://127.0.0.1:8082"
	authLoginUsername = "bootstrap"

	passwordFile := filepath.Join(home, "password.txt")
	if err := os.WriteFile(passwordFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	authLoginPasswordFile = passwordFile

	passwordLoginFunc = func(server, username, password string) (*client.LoginResult, error) {
		if password != "from-file" {
			t.Fatalf("unexpected password: %q", password)
		}
		return &client.LoginResult{
			Token:     "jwt-file",
			ExpiresAt: time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
			AuthType:  "password",
			Username:  username,
		}, nil
	}

	_, _, err := captureCommandOutput(func() error {
		return authLoginCmd.RunE(authLoginCmd, nil)
	})
	if err != nil {
		t.Fatalf("auth login returned error: %v", err)
	}

	savedCfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if savedCfg.Contexts["default"].Token != "jwt-file" {
		t.Fatalf("expected token to be saved, got %+v", savedCfg.Contexts["default"])
	}
}

func TestAuthLoginInvalidCredentialsDoNotLeakPassword(t *testing.T) {
	resetAuthLoginState(t)
	prev := passwordLoginFunc
	t.Cleanup(func() {
		passwordLoginFunc = prev
	})

	t.Setenv("HOME", t.TempDir())
	cfg = &config.Config{Contexts: map[string]config.Context{}}
	flagOutput = "json"
	authLoginServer = "http://127.0.0.1:8082"
	authLoginUsername = "admin"
	t.Setenv("AEGIS_PASSWORD", "env-secret")

	passwordLoginFunc = func(server, username, password string) (*client.LoginResult, error) {
		if password != "env-secret" {
			t.Fatalf("unexpected password: %q", password)
		}
		return nil, &client.APIError{StatusCode: 401, Message: "invalid username or password"}
	}

	_, stderr, err := captureCommandOutput(func() error {
		return authLoginCmd.RunE(authLoginCmd, nil)
	})
	if err == nil {
		t.Fatal("expected auth login to fail")
	}
	if !strings.Contains(err.Error(), "invalid username or password") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "env-secret") || strings.Contains(stderr, "env-secret") {
		t.Fatal("password leaked in command output")
	}
}

func captureCommandOutput(fn func() error) (stdout, stderr string, err error) {
	origStdout := os.Stdout
	origStderr := os.Stderr
	origQuiet := output.Quiet

	stdoutR, stdoutW, pipeErr := os.Pipe()
	if pipeErr != nil {
		return "", "", pipeErr
	}
	stderrR, stderrW, pipeErr := os.Pipe()
	if pipeErr != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return "", "", pipeErr
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW
	output.Quiet = false

	err = fn()

	_ = stdoutW.Close()
	_ = stderrW.Close()
	os.Stdout = origStdout
	os.Stderr = origStderr
	output.Quiet = origQuiet

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	_, _ = io.Copy(&stdoutBuf, stdoutR)
	_, _ = io.Copy(&stderrBuf, stderrR)
	_ = stdoutR.Close()
	_ = stderrR.Close()

	return strings.TrimSpace(stdoutBuf.String()), strings.TrimSpace(stderrBuf.String()), err
}

func resetAuthLoginState(t *testing.T) {
	t.Helper()
	flagServer = ""
	flagToken = ""
	flagProject = ""
	flagOutput = ""
	flagRequestTimeout = 0
	flagQuiet = false
	flagDryRun = false
	cfg = nil

	authLoginServer = ""
	authLoginKeyID = ""
	authLoginKeySecret = ""
	authLoginUsername = ""
	authLoginPasswordFile = ""
	authLoginPasswordStdin = false
	authLoginContext = ""
	authLoginCmd.SetIn(bytes.NewReader(nil))
}
