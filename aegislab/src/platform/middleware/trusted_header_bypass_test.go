package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aegis/platform/crypto"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

type bypassSvc struct {
	noopMiddlewareService
}

func (bypassSvc) VerifyToken(_ context.Context, _ string) (*crypto.UnifiedClaims, error) {
	return &crypto.UnifiedClaims{
		Typ:      "human",
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

func (bypassSvc) VerifyServiceToken(_ context.Context, _ string) (*crypto.UnifiedClaims, error) {
	return nil, errors.New("service tokens not supported in bypass mode")
}

// TestTrustedHeaderAuth_ServiceDirect verifies that a bearer-bearing request
// without a trusted-header signature falls through to JWTAuth.
func TestTrustedHeaderAuth_ServiceDirect_ValidBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viper.Set("gateway.trusted_header_key", "test-signing-key")
	t.Cleanup(func() { viper.Set("gateway.trusted_header_key", "") })
	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)
	router.GET("/test", InjectService(bypassSvc{}), TrustedHeaderAuth(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer some-valid-token")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("service-direct valid bearer: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

type rejectAllSvc struct {
	noopMiddlewareService
}

func (rejectAllSvc) VerifyToken(_ context.Context, _ string) (*crypto.UnifiedClaims, error) {
	return nil, errors.New("invalid user token")
}

func (rejectAllSvc) VerifyServiceToken(_ context.Context, _ string) (*crypto.UnifiedClaims, error) {
	return nil, errors.New("invalid service token")
}

func TestTrustedHeaderAuth_ServiceDirect_InvalidBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viper.Set("gateway.trusted_header_key", "test-signing-key")
	t.Cleanup(func() { viper.Set("gateway.trusted_header_key", "") })
	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)
	router.GET("/test", InjectService(rejectAllSvc{}), TrustedHeaderAuth(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer forged")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("service-direct invalid bearer: expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTrustedHeaderAuth_NoAuthMaterial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viper.Set("gateway.trusted_header_key", "test-signing-key")
	t.Cleanup(func() { viper.Set("gateway.trusted_header_key", "") })
	w := httptest.NewRecorder()
	_, router := gin.CreateTestContext(w)
	router.GET("/test", InjectService(rejectAllSvc{}), TrustedHeaderAuth(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth: expected 401, got %d: %s", w.Code, w.Body.String())
	}
}
