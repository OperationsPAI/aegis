package sso

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/jwtkeys"
	"aegis/platform/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSATestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ServiceAccount{}))
	return db
}

func newSATestSigner(t *testing.T) *jwtkeys.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return &jwtkeys.Signer{PrivateKey: key, Kid: "test-kid"}
}

func newSARouter(t *testing.T, db *gorm.DB, signer *jwtkeys.Signer) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Bypass middleware.JWTAuth: stamp the gin context with the keys
	// requireAdminOrService consults so the gate treats us as global admin.
	r.Use(func(c *gin.Context) {
		c.Set(consts.CtxKeyIsAdmin, true)
		c.Set(consts.CtxKeyUserID, 1)
		c.Next()
	})

	repo := NewServiceAccountRepository(db)
	svc := NewServiceAccountService(repo, signer)
	h := NewServiceAccountHandler(svc)

	r.POST("/v1/service-accounts/:name/issue", h.Issue)
	r.POST("/v1/service-accounts/:name/revoke", h.Revoke)
	return r
}

func mustSeedSA(t *testing.T, db *gorm.DB, name, scopes string) {
	t.Helper()
	require.NoError(t, db.Create(&model.ServiceAccount{
		Name:   name,
		Scopes: scopes,
	}).Error)
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestServiceAccountIssue_Roundtrip(t *testing.T) {
	db := newSATestDB(t)
	signer := newSATestSigner(t)
	mustSeedSA(t, db, "chaos-service", "chaos.inject.write,chaos.webhook.write")

	r := newSARouter(t, db, signer)
	w := doJSON(t, r, http.MethodPost, "/v1/service-accounts/chaos-service/issue",
		map[string]any{"lifetime_days": 30})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		Data IssueServiceAccountTokenResp `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Data.Token)

	pub := &signer.PrivateKey.PublicKey
	claims, err := crypto.ParseServiceAccountToken(resp.Data.Token, func(string) (*rsa.PublicKey, error) {
		return pub, nil
	})
	require.NoError(t, err)
	require.Equal(t, "chaos-service", claims.Subject)
	require.Equal(t, []string{"chaos.inject.write", "chaos.webhook.write"}, claims.Scopes)
}

func TestServiceAccountRevoke_ThenIssueFails(t *testing.T) {
	db := newSATestDB(t)
	signer := newSATestSigner(t)
	mustSeedSA(t, db, "chaos-service", "chaos.inject.write")

	r := newSARouter(t, db, signer)

	// Pre-revoke issue works.
	w := doJSON(t, r, http.MethodPost, "/v1/service-accounts/chaos-service/issue", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Revoke succeeds with 204.
	w = doJSON(t, r, http.MethodPost, "/v1/service-accounts/chaos-service/revoke", nil)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	// DB row has RevokedAt set.
	repo := NewServiceAccountRepository(db)
	sa, err := repo.GetByName(context.Background(), "chaos-service")
	require.NoError(t, err)
	require.NotNil(t, sa.RevokedAt)

	// Post-revoke issue is rejected with 409.
	w = doJSON(t, r, http.MethodPost, "/v1/service-accounts/chaos-service/issue", nil)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())

	// Re-revoking is also 409.
	w = doJSON(t, r, http.MethodPost, "/v1/service-accounts/chaos-service/revoke", nil)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
}

func TestServiceAccountIssue_UnknownName_404(t *testing.T) {
	db := newSATestDB(t)
	signer := newSATestSigner(t)
	r := newSARouter(t, db, signer)

	w := doJSON(t, r, http.MethodPost, "/v1/service-accounts/no-such/issue", nil)
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}
