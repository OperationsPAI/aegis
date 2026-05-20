package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aegis/cli/client"
	"aegis/cli/config"
	"aegis/platform/consts"
)

func TestShouldRefreshAt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name     string
		expiry   time.Time
		want     bool
	}{
		{"zero expiry never refreshes", time.Time{}, false},
		{"expired in past", now.Add(-time.Minute), true},
		{"within threshold", now.Add(2 * time.Minute), true},
		{"exactly at threshold", now.Add(5 * time.Minute), true},
		{"just outside threshold", now.Add(5*time.Minute + time.Second), false},
		{"far in future", now.Add(time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRefreshAt(tc.expiry, 5*time.Minute, now)
			if got != tc.want {
				t.Fatalf("shouldRefreshAt(%v) = %v, want %v", tc.expiry, got, tc.want)
			}
		})
	}
}

func makeJWT(t *testing.T, exp int64) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, _ := json.Marshal(map[string]any{"exp": exp, "sub": "test"})
	payload := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + payload + ".signature-not-verified"
}

func TestParseJWTExp(t *testing.T) {
	want := int64(1_900_000_000)
	got := client.ParseJWTExp(makeJWT(t, want))
	if got.Unix() != want {
		t.Fatalf("ParseJWTExp = %d, want %d", got.Unix(), want)
	}

	if !client.ParseJWTExp("not-a-jwt").IsZero() {
		t.Fatal("ParseJWTExp on garbage should be zero")
	}
	if !client.ParseJWTExp("").IsZero() {
		t.Fatal("ParseJWTExp on empty should be zero")
	}
	noExp := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`)) + ".sig"
	if !client.ParseJWTExp(noExp).IsZero() {
		t.Fatal("ParseJWTExp on token without exp should be zero")
	}
}

// TestMaybeRefreshToken_RotatesAndPersists drives the full transparent refresh
// path: stale token near expiry -> fake /api/v2/auth/refresh issues a rotated
// token -> config is rewritten atomically -> flagToken now holds the new value
// -> a second call against the same server returns the rotated token.
func TestMaybeRefreshToken_RotatesAndPersists(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	staleExp := time.Now().Add(30 * time.Second).Unix()
	staleToken := makeJWT(t, staleExp)
	rotatedToken := makeJWT(t, time.Now().Add(24*time.Hour).Unix())
	rotatedExpiry := time.Now().Add(24 * time.Hour).Truncate(time.Second)

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != consts.APIPathAuthRefresh {
			t.Fatalf("unexpected refresh path: %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		seen = append(seen, auth)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    0,
			"message": "ok",
			"data": map[string]any{
				"token":      rotatedToken,
				"expires_at": rotatedExpiry.Format(time.RFC3339),
			},
		})
	}))
	defer srv.Close()

	prevCfg, prevToken, prevServer := cfg, flagToken, flagServer
	t.Cleanup(func() { cfg, flagToken, flagServer = prevCfg, prevToken, prevServer })

	cfg = &config.Config{
		CurrentContext: "default",
		Contexts: map[string]config.Context{
			"default": {
				Server:      srv.URL,
				Token:       staleToken,
				TokenExpiry: time.Unix(staleExp, 0),
			},
		},
	}
	flagToken = staleToken
	flagServer = srv.URL

	maybeRefreshToken()

	if flagToken != rotatedToken {
		t.Fatalf("flagToken not rotated: got %q want %q", flagToken, rotatedToken)
	}
	if got := cfg.Contexts["default"].Token; got != rotatedToken {
		t.Fatalf("in-memory cfg token not rotated: %q", got)
	}

	// Verify atomic write landed on disk.
	cfgPath := filepath.Join(tmpHome, ".aegisctl", "config.yaml")
	persisted, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if want := rotatedToken; !contains(string(persisted), want) {
		t.Fatalf("persisted config missing rotated token; got:\n%s", persisted)
	}

	// Second invocation: token is now fresh, so no second refresh call.
	maybeRefreshToken()
	if len(seen) != 1 {
		t.Fatalf("expected exactly one refresh call, got %d (%v)", len(seen), seen)
	}
	if seen[0] != "Bearer "+staleToken {
		t.Fatalf("refresh did not bear stale token: %q", seen[0])
	}
}

// TestMaybeRefreshToken_FailureKeepsStaleToken verifies that a 5xx / network
// failure does not destroy the cached token — the server-side 401 is what
// must surface, not a silently-swallowed CLI error.
func TestMaybeRefreshToken_FailureKeepsStaleToken(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	staleExp := time.Now().Add(30 * time.Second).Unix()
	staleToken := makeJWT(t, staleExp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintln(w, `{"code":500,"message":"boom"}`)
	}))
	defer srv.Close()

	prevCfg, prevToken, prevServer := cfg, flagToken, flagServer
	t.Cleanup(func() { cfg, flagToken, flagServer = prevCfg, prevToken, prevServer })

	cfg = &config.Config{
		CurrentContext: "default",
		Contexts: map[string]config.Context{
			"default": {Server: srv.URL, Token: staleToken, TokenExpiry: time.Unix(staleExp, 0)},
		},
	}
	flagToken = staleToken
	flagServer = srv.URL

	maybeRefreshToken()

	if flagToken != staleToken {
		t.Fatalf("flagToken should be unchanged on refresh failure, got %q", flagToken)
	}
	if got := cfg.Contexts["default"].Token; got != staleToken {
		t.Fatalf("cfg token should be unchanged on refresh failure, got %q", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
