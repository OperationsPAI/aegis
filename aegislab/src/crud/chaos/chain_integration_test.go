package chaos

import (
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/platform/middleware"
	"aegis/platform/testutil"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// signTrustedHeaders reproduces the gateway-side HMAC over the canonical
// trusted-header field order (see middleware.TrustedHeaderAuth). It lets the
// test forge a request that authenticates as a specific identity, exactly as
// the L7 gateway would after authenticating a user.
func signTrustedHeaders(key, userID, email, roles, aud, jti, username, isActive, isAdmin, authType, apiKeyID, apiKeyScopes, taskID string) string {
	canonical := strings.Join([]string{
		userID, email, roles, aud, jti,
		username, isActive, isAdmin, authType,
		apiKeyID, apiKeyScopes, taskID,
	}, "|")
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

// chaosChainEngine builds the REAL /v1beta auth chain from routes.go
// (RequireServiceAccount -> NewChaosAuthFromEnv -> requireChaosPrincipal) in
// the open/fallthrough mode (no static bearer) that exposes the user-JWT path,
// fronting a stub 200 handler.
func chaosChainEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	viper.Set("gateway.trusted_header_key", "test-thk")
	t.Cleanup(func() { viper.Set("gateway.trusted_header_key", "") })
	// No static bearer + require_bearer=false -> NewChaosAuthFromEnv falls
	// through to TrustedHeaderAuth, the exact fail-open scenario the fix closes.
	t.Setenv("CHAOS_INBOUND_BEARER", "")
	t.Setenv("CHAOS_REQUIRE_BEARER", "false")

	db := testutil.NewSQLiteGormDB(t)
	resolve := func(string) (*rsa.PublicKey, error) { return nil, http.ErrNoCookie }

	r := gin.New()
	r.Use(middleware.RequireServiceAccount(db, resolve, "chaos-client"))
	auth := r.Group("", NewChaosAuthFromEnv(), requireChaosPrincipal())
	auth.GET("/v1beta/capabilities", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

// TestChaosChain_RejectsOrdinaryUser is the end-to-end-ish proof that the
// production middleware composition closes the exposure: a fully-authenticated
// ordinary (non-admin) user reaches the chain via the trusted-header path and
// is rejected with 403, while an admin passes and an anonymous caller is 401.
func TestChaosChain_RejectsOrdinaryUser(t *testing.T) {
	const thk = "test-thk"

	t.Run("ordinary user -> 403", func(t *testing.T) {
		r := chaosChainEngine(t)
		sig := signTrustedHeaders(thk, "42", "u@x", "", "", "", "user42", "1", "0", "user", "", "", "")
		req := httptest.NewRequest(http.MethodGet, "/v1beta/capabilities", nil)
		req.Header.Set("X-Aegis-User-Id", "42")
		req.Header.Set("X-Aegis-User-Email", "u@x")
		req.Header.Set("X-Aegis-Username", "user42")
		req.Header.Set("X-Aegis-Is-Active", "1")
		req.Header.Set("X-Aegis-Is-Admin", "0")
		req.Header.Set("X-Aegis-Auth-Type", "user")
		req.Header.Set("X-Aegis-Signature", sig)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("ordinary user: expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("admin -> 200", func(t *testing.T) {
		r := chaosChainEngine(t)
		sig := signTrustedHeaders(thk, "1", "admin@x", "", "", "", "admin", "1", "1", "user", "", "", "")
		req := httptest.NewRequest(http.MethodGet, "/v1beta/capabilities", nil)
		req.Header.Set("X-Aegis-User-Id", "1")
		req.Header.Set("X-Aegis-User-Email", "admin@x")
		req.Header.Set("X-Aegis-Username", "admin")
		req.Header.Set("X-Aegis-Is-Active", "1")
		req.Header.Set("X-Aegis-Is-Admin", "1")
		req.Header.Set("X-Aegis-Auth-Type", "user")
		req.Header.Set("X-Aegis-Signature", sig)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("admin: expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("anonymous -> 401", func(t *testing.T) {
		r := chaosChainEngine(t)
		req := httptest.NewRequest(http.MethodGet, "/v1beta/capabilities", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous: expected 401, got %d: %s", w.Code, w.Body.String())
		}
	})
}
