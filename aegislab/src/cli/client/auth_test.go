package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aegis/cli/internal/cli/clierr"
)

func TestCanonicalAPIKeyString(t *testing.T) {
	got := canonicalAPIKeyString(
		"post",
		"/api/v2/auth/api-key/token",
		"1713333333",
		"abc123",
		"body_hash",
	)

	want := "POST\n/api/v2/auth/api-key/token\n1713333333\nabc123\nbody_hash"
	if got != want {
		t.Fatalf("canonical string mismatch:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestBuildAPIKeyHeaders(t *testing.T) {
	headers, err := buildAPIKeyHeaders(
		"pk_demo",
		"ks_demo",
		time.Unix(1713333333, 0).UTC(),
		"/api/v2/auth/api-key/token",
	)
	if err != nil {
		t.Fatalf("buildAPIKeyHeaders returned error: %v", err)
	}

	if headers["X-Key-Id"] != "pk_demo" {
		t.Fatalf("unexpected key id header: %q", headers["X-Key-Id"])
	}
	if headers["X-Timestamp"] != "1713333333" {
		t.Fatalf("unexpected timestamp header: %q", headers["X-Timestamp"])
	}
	if headers["X-Nonce"] == "" {
		t.Fatal("expected nonce header to be set")
	}
	if len(headers["X-Signature"]) != 64 {
		t.Fatalf("unexpected signature length: %d", len(headers["X-Signature"]))
	}
}

func TestPrepareAPIKeyTokenDebug(t *testing.T) {
	debugInfo, err := PrepareAPIKeyTokenDebug(
		"pk_demo",
		"ks_demo",
		time.Unix(1713333333, 0).UTC(),
		"abc123",
	)
	if err != nil {
		t.Fatalf("PrepareAPIKeyTokenDebug returned error: %v", err)
	}

	if debugInfo.Method != "POST" {
		t.Fatalf("unexpected method: %q", debugInfo.Method)
	}
	if debugInfo.Path != "/api/v2/auth/api-key/token" {
		t.Fatalf("unexpected path: %q", debugInfo.Path)
	}
	if debugInfo.CanonicalString != "POST\n/api/v2/auth/api-key/token\n1713333333\nabc123\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("unexpected canonical string: %q", debugInfo.CanonicalString)
	}
	if debugInfo.BodySHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("unexpected body hash: %q", debugInfo.BodySHA256)
	}
	if debugInfo.Headers()["X-Signature"] != debugInfo.Signature {
		t.Fatal("signature header mismatch")
	}
}

func TestLoginWithPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != ssoTokenPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != "password" {
			t.Fatalf("unexpected grant_type: %q", got)
		}
		if got := r.PostForm.Get("client_id"); got != ssoCLIClientID {
			t.Fatalf("unexpected client_id: %q", got)
		}
		if got := r.PostForm.Get("username"); got != "bootstrap" {
			t.Fatalf("unexpected username: %q", got)
		}
		if got := r.PostForm.Get("password"); got != "super-secret" {
			t.Fatalf("unexpected password: %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"jwt-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()

	result, err := LoginWithPassword(server.URL, " bootstrap ", "super-secret")
	if err != nil {
		t.Fatalf("LoginWithPassword returned error: %v", err)
	}
	if result.Token != "jwt-token" {
		t.Fatalf("unexpected token: %q", result.Token)
	}
	if result.AuthType != "password" {
		t.Fatalf("unexpected auth type: %q", result.AuthType)
	}
	if result.Username != "bootstrap" {
		t.Fatalf("unexpected username (got %q)", result.Username)
	}
	if d := time.Until(result.ExpiresAt); d < time.Hour-time.Minute || d > time.Hour+time.Minute {
		t.Fatalf("expires_at not roughly +1h: got %s", d)
	}
}

func TestLoginWithPasswordInvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"invalid credentials"}`))
	}))
	defer server.Close()

	_, err := LoginWithPassword(server.URL, "bootstrap", "super-secret")
	if err == nil {
		t.Fatal("expected login error")
	}
	if !strings.Contains(err.Error(), "invalid credentials") || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("unexpected error: %q", err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatal("password leaked into error output")
	}
}

func TestPostWithHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Key-Id"); got != "pk_demo" {
			t.Fatalf("unexpected X-Key-Id header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"message":"ok"}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "", 5*time.Second)
	var resp APIResponse[map[string]any]
	if err := c.PostWithHeaders("/api/v2/auth/api-key/token", map[string]string{
		"X-Key-Id": "pk_demo",
	}, &resp); err != nil {
		t.Fatalf("PostWithHeaders returned error: %v", err)
	}
}

func TestServerErrorsAreSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-500")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":500,"message":"An unexpected error occurred"}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "", 5*time.Second)
	err := c.Get("/api/v2/auth/api-key/token", nil)
	if err == nil {
		t.Fatal("expected server error")
	}

	cliErr, ok := err.(*clierr.CLIError)
	if !ok {
		t.Fatalf("error type = %T, want *clierr.CLIError", err)
	}
	if cliErr.Type != "server" {
		t.Fatalf("cliErr.Type = %q, want server", cliErr.Type)
	}
	if cliErr.ExitCode != exitCodeServer {
		t.Fatalf("cliErr.ExitCode = %d, want %d", cliErr.ExitCode, exitCodeServer)
	}
	if cliErr.RequestID != "req-500" {
		t.Fatalf("cliErr.RequestID = %q, want req-500", cliErr.RequestID)
	}
	if strings.Contains(cliErr.Message, genericServerMessage) {
		t.Fatalf("cliErr.Message leaked generic server message: %q", cliErr.Message)
	}
	if strings.Contains(cliErr.Cause, genericServerMessage) {
		t.Fatalf("cliErr.Cause leaked generic server message: %q", cliErr.Cause)
	}
	if !strings.Contains(cliErr.Message, "request_id=req-500") {
		t.Fatalf("cliErr.Message = %q, want request_id", cliErr.Message)
	}
}
