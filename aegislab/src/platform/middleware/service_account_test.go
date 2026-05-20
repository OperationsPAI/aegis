package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/model"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func saTestSetup(t *testing.T) (*gorm.DB, *rsa.PrivateKey, crypto.PublicKeyResolver) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ServiceAccount{}))
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	resolve := crypto.PublicKeyResolver(func(_ string) (*rsa.PublicKey, error) {
		return &priv.PublicKey, nil
	})
	return db, priv, resolve
}

func saMintToken(t *testing.T, priv *rsa.PrivateKey, name string, scopes []string, lifetime time.Duration) string {
	t.Helper()
	tok, _, err := crypto.GenerateServiceAccountToken(name, scopes, lifetime, priv, "test-kid")
	require.NoError(t, err)
	return tok
}

func saRouter(t *testing.T, db *gorm.DB, resolve crypto.PublicKeyResolver, allowed ...string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireServiceAccount(db, resolve, allowed...))
	// Fallback handler used to verify "fall through" behaviour: returns 418
	// so tests can distinguish a successful SA auth (no abort, hits the
	// probe handler) from a fall-through path (probe handler also runs but
	// without SA context flags set).
	r.POST("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"auth_type":  c.GetString(consts.CtxKeyAuthType),
			"is_service": c.GetBool(consts.CtxKeyIsServiceToken),
			"username":   c.GetString(consts.CtxKeyUsername),
		})
	})
	return r
}

func TestRequireServiceAccount_ValidAndAllowed(t *testing.T) {
	db, priv, resolve := saTestSetup(t)
	require.NoError(t, db.Create(&model.ServiceAccount{Name: "chaos-service", Scopes: "chaos.webhook.write"}).Error)
	tok := saMintToken(t, priv, "chaos-service", []string{"chaos.webhook.write"}, time.Hour)
	r := saRouter(t, db, resolve, "chaos-service")

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"auth_type":"service_account"`)
	require.Contains(t, w.Body.String(), `"is_service":true`)
	require.Contains(t, w.Body.String(), `"username":"chaos-service"`)
}

func TestRequireServiceAccount_NameNotAllowed_Returns403(t *testing.T) {
	db, priv, resolve := saTestSetup(t)
	require.NoError(t, db.Create(&model.ServiceAccount{Name: "other-sa", Scopes: ""}).Error)
	tok := saMintToken(t, priv, "other-sa", nil, time.Hour)
	r := saRouter(t, db, resolve, "chaos-service")

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestRequireServiceAccount_Revoked_Returns401(t *testing.T) {
	db, priv, resolve := saTestSetup(t)
	now := time.Now()
	require.NoError(t, db.Create(&model.ServiceAccount{Name: "chaos-service", RevokedAt: &now}).Error)
	tok := saMintToken(t, priv, "chaos-service", nil, time.Hour)
	r := saRouter(t, db, resolve, "chaos-service")

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireServiceAccount_Expired_Returns401(t *testing.T) {
	db, priv, resolve := saTestSetup(t)
	require.NoError(t, db.Create(&model.ServiceAccount{Name: "chaos-service"}).Error)
	// Mint a structurally-valid SA token with exp in the past so jwt
	// validation rejects on expiry rather than on lifetime construction.
	past := time.Now().Add(-time.Hour)
	claims := jwt.MapClaims{
		"sub":       "chaos-service",
		"iss":       consts.JWTIssuerServiceAccount,
		"auth_type": consts.AuthTypeServiceAccount,
		"scopes":    []string{},
		"iat":       past.Add(-time.Hour).Unix(),
		"nbf":       past.Add(-time.Hour).Unix(),
		"exp":       past.Unix(),
	}
	tokenObj := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenObj.Header["kid"] = "test-kid"
	tok, err := tokenObj.SignedString(priv)
	require.NoError(t, err)
	r := saRouter(t, db, resolve, "chaos-service")

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireServiceAccount_NonSABearer_FallsThrough(t *testing.T) {
	db, priv, resolve := saTestSetup(t)
	// User token (different issuer) — must fall through, not 401.
	userTok, _, err := crypto.GenerateToken(7, "alice", "a@x", true, false, nil, priv, "test-kid")
	require.NoError(t, err)
	r := saRouter(t, db, resolve, "chaos-service")

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	// Crucially: ctx flags were NOT stamped — fall-through path.
	require.NotContains(t, w.Body.String(), `"auth_type":"service_account"`)
	require.Contains(t, w.Body.String(), `"is_service":false`)
}

func TestRequireServiceAccount_NonJWTBearer_FallsThrough(t *testing.T) {
	// Raw hex static bearer (dispatcher's CHAOS_OUTBOUND_BEARER fallback) is
	// not a JWT at all. Must fall through to the next auth middleware rather
	// than 401-abort.
	db, _, resolve := saTestSetup(t)
	r := saRouter(t, db, resolve, "chaos-service")

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("Authorization", "Bearer deadbeefcafef00d")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), `"auth_type":"service_account"`)
	require.Contains(t, w.Body.String(), `"is_service":false`)
}

func TestRequireServiceAccount_NoAuth_FallsThrough(t *testing.T) {
	db, _, resolve := saTestSetup(t)
	r := saRouter(t, db, resolve, "chaos-service")

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), `"auth_type":"service_account"`)
}
