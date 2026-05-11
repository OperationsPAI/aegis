package ssoclient

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aegis/dto"
	"aegis/infra/jwtkeys"
	"aegis/utils"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

const testKid = "test-kid"

func newSSOMock(t *testing.T, priv *rsa.PrivateKey) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	checkHits := &atomic.Int32{}
	regHits := &atomic.Int32{}

	r := gin.New()
	r.POST("/token", func(c *gin.Context) {
		require.NoError(t, c.Request.ParseForm())
		require.Equal(t, "client_credentials", c.Request.PostFormValue("grant_type"))
		require.Equal(t, "aegis-backend", c.Request.PostFormValue("client_id"))
		claims := jwt.MapClaims{
			"iss":        "sso-test",
			"sub":        "service:aegis-backend",
			"aud":        []string{"aegis-sso"},
			"exp":        time.Now().Add(time.Hour).Unix(),
			"iat":        time.Now().Unix(),
			"token_type": "service",
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = testKid
		signed, err := tok.SignedString(priv)
		require.NoError(t, err)
		c.JSON(http.StatusOK, gin.H{
			"access_token": signed,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	r.POST("/v1/check", func(c *gin.Context) {
		checkHits.Add(1)
		var body map[string]any
		require.NoError(t, c.ShouldBindJSON(&body))
		require.Equal(t, "Bearer ", c.GetHeader("Authorization")[:7])
		// Allow only user 42 + injection.create.
		allowed := body["user_id"].(float64) == 42 && body["permission"] == "injection.create"
		dto.SuccessResponse(c, gin.H{"allowed": allowed, "reason": "test"})
	})
	r.POST("/v1/permissions:register", func(c *gin.Context) {
		regHits.Add(1)
		raw, _ := io.ReadAll(c.Request.Body)
		var req map[string]any
		require.NoError(t, json.Unmarshal(raw, &req))
		require.Equal(t, "aegis-backend", req["service"])
		perms, _ := req["permissions"].([]any)
		require.NotEmpty(t, perms)
		dto.SuccessResponse(c, gin.H{"registered": len(perms)})
	})

	return httptest.NewServer(r), checkHits, regHits
}

func newTestClient(t *testing.T, baseURL string, pub *rsa.PublicKey) *Client {
	t.Helper()
	verifier := jwtkeys.NewVerifierWithKeys(map[string]*rsa.PublicKey{testKid: pub})
	return NewClient(Config{
		BaseURL:      baseURL,
		ClientID:     "aegis-backend",
		ClientSecret: "secret",
	}, verifier)
}

func TestCheck_CacheMissThenHit(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv, checkHits, _ := newSSOMock(t, priv)
	defer srv.Close()

	c := newTestClient(t, srv.URL, &priv.PublicKey)
	params := CheckParams{UserID: 42, Permission: "injection.create", ScopeType: "aegis.project", ScopeID: "7"}

	ok, err := c.Check(context.Background(), params)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int32(1), checkHits.Load())

	// Second call hits the LRU.
	ok2, err := c.Check(context.Background(), params)
	require.NoError(t, err)
	require.True(t, ok2)
	require.Equal(t, int32(1), checkHits.Load(), "second Check should not reach the server")

	// A negative result also caches.
	denied, err := c.Check(context.Background(), CheckParams{UserID: 99, Permission: "injection.create"})
	require.NoError(t, err)
	require.False(t, denied)
	require.Equal(t, int32(2), checkHits.Load())
}

func TestVerifyToken_RejectsForged(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv, _, _ := newSSOMock(t, priv)
	defer srv.Close()
	c := newTestClient(t, srv.URL, &priv.PublicKey)

	good, _, err := utils.GenerateToken(42, "alice", "alice@x.com", true, false, []string{"user"}, priv, testKid)
	require.NoError(t, err)
	claims, err := c.VerifyToken(context.Background(), good)
	require.NoError(t, err)
	require.Equal(t, 42, claims.UserID)

	forged, _, err := utils.GenerateToken(42, "alice", "alice@x.com", true, false, []string{"admin"}, other, testKid)
	require.NoError(t, err)
	_, err = c.VerifyToken(context.Background(), forged)
	require.Error(t, err)
}

func TestRegisterPermissions_PostsCorrectBody(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv, _, regHits := newSSOMock(t, priv)
	defer srv.Close()

	c := newTestClient(t, srv.URL, &priv.PublicKey)
	err = c.RegisterPermissions(context.Background(), []PermissionSpec{
		{Name: "injection.create", DisplayName: "Create injection", ScopeType: "aegis.project"},
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), regHits.Load())
}

func TestDoJSON_ErrorEnvelopePropagates(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	r := gin.New()
	r.POST("/token", func(c *gin.Context) {
		claims := jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = testKid
		signed, _ := tok.SignedString(priv)
		c.JSON(http.StatusOK, gin.H{"access_token": signed, "expires_in": 3600})
	})
	r.POST("/v1/check", func(c *gin.Context) {
		dto.ErrorResponse(c, http.StatusForbidden, "denied for test")
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	c := newTestClient(t, srv.URL, &priv.PublicKey)
	_, err = c.Check(context.Background(), CheckParams{UserID: 1, Permission: "x"})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "denied for test"))
}
