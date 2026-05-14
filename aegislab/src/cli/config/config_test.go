package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAfterLoadDoesNotStripStoredCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Simulate an operator-edited file with username/password fields.
	rawYAML := []byte(`current-context: default
contexts:
  default:
    server: http://localhost:18082
    token: old-jwt
    auth-type: password
    username: admin
    password: stored-secret
    default-project: pair_diagnosis
    token-expiry: 2026-04-30T19:22:29Z
`)
	cfgDir := filepath.Join(home, ".aegisctl")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), rawYAML, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Mimic the auth login save path: refresh token only.
	ctx := loaded.Contexts["default"]
	ctx.Token = "new-jwt"
	ctx.TokenExpiry = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	loaded.Contexts["default"] = ctx
	if err := SaveConfig(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := reloaded.Contexts["default"]
	if got.Username != "admin" || got.Password != "stored-secret" {
		t.Fatalf("stored credentials lost on save: %+v", got)
	}
	if got.Token != "new-jwt" {
		t.Fatalf("token not refreshed: %+v", got)
	}
}
