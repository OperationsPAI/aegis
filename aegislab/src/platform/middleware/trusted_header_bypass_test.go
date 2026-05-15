package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// bypassSvc accepts any user token and returns fixed Claims. Implements the full
// Service interface by embedding noopMiddlewareService for the parts not needed
// by JWTAuth.
type bypassSvc struct {
	noopMiddlewareService
}

func (bypassSvc) VerifyToken(_ context.Context, _ string) (*crypto.Claims, error) {
	return &crypto.Claims{
		UserID:   123,
		Username: "dev-user",
		Email:    "dev@example.com",
		IsActive: true,
		IsAdmin:  false,
		Roles:    []string{"viewer"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}, nil
}

func (bypassSvc) VerifyServiceToken(_ context.Context, _ string) (*crypto.ServiceClaims, error) {
	return nil, errors.New("service tokens not supported in bypass mode")
}

// TestTrustedHeaderAuth_DevJWTBypass verifies that when AEGIS_DEV_JWT_BYPASS=true
// and no X-Aegis-Signature header is present, TrustedHeaderAuth falls through to
// JWTAuth and authenticates a request bearing only "Authorization: Bearer <jwt>".
func TestTrustedHeaderAuth_DevJWTBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Wire the trusted_header_key so the middleware doesn't reject on empty key
	// (the bypass path runs before the key check, but let's be accurate to
	// production shape — bypass exits early before key is consulted).
	viper.Set("gateway.trusted_header_key", "test-signing-key")
	t.Cleanup(func() { viper.Set("gateway.trusted_header_key", "") })

	t.Setenv("AEGIS_DEV_JWT_BYPASS", "true")

	w := httptest.NewRecorder()
	c, router := gin.CreateTestContext(w)

	// Inject a stub verifier that accepts any token.
	injectSvc := InjectService(bypassSvc{})

	// Build a minimal handler that asserts the user was populated.
	var capturedUID int
	handler := func(c *gin.Context) {
		uid, ok := c.Get(consts.CtxKeyUserID)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "user_id not set"})
			return
		}
		capturedUID = uid.(int)
		c.Status(http.StatusOK)
	}

	router.GET("/test", injectSvc, TrustedHeaderAuth(), handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer some-valid-token")
	// Deliberately omit X-Aegis-Signature to trigger bypass path.
	c.Request = req

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("AEGIS_DEV_JWT_BYPASS: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedUID != 123 {
		t.Fatalf("expected user_id=123, got %d", capturedUID)
	}
}

// TestTrustedHeaderAuth_DevJWTBypass_DisabledByDefault verifies that without
// AEGIS_DEV_JWT_BYPASS, a request with only an Authorization header and no
// trusted-header signature is rejected.
func TestTrustedHeaderAuth_DevJWTBypass_DisabledByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	viper.Set("gateway.trusted_header_key", "test-signing-key")
	t.Cleanup(func() { viper.Set("gateway.trusted_header_key", "") })

	// Ensure bypass is off regardless of ambient environment.
	if err := os.Unsetenv("AEGIS_DEV_JWT_BYPASS"); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)

	router.GET("/test", InjectService(bypassSvc{}), TrustedHeaderAuth(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer some-valid-token")
	// No X-Aegis-Signature → must be rejected by trusted-header check.
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no bypass: expected 401, got %d: %s", w.Code, w.Body.String())
	}
}
