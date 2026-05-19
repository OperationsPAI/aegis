package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// TestAudienceChainEnforcedAtBoot verifies that every non-public audience
// gets TrustedHeaderAuth prepended automatically: a registrar that does
// NOT attach its own auth still rejects unauthenticated callers. This
// catches the "forgot auth on a new route" class of bug at boot.
func TestAudienceChainEnforcedAtBoot(t *testing.T) {
	// TrustedHeaderAuth reads the signing key lazily; set a dummy one
	// so it reaches the canonical-string mismatch path (401) instead of
	// the "key empty" path (also 401, but for a different reason).
	viper.Set("gateway.trusted_header_key", "test-key")
	t.Cleanup(func() { viper.Set("gateway.trusted_header_key", "") })

	gin.SetMode(gin.TestMode)

	cases := []struct {
		audience framework.Audience
		path     string
	}{
		{framework.AudiencePortal, "/portal-probe"},
		{framework.AudienceSDK, "/sdk-probe"},
		{framework.AudienceAdmin, "/admin-probe"},
	}

	var registrars []framework.RouteRegistrar
	for _, c := range cases {
		c := c
		registrars = append(registrars, framework.RouteRegistrar{
			Audience: c.audience,
			Name:     "probe." + string(c.audience),
			Register: func(g *gin.RouterGroup) {
				g.GET(c.path, func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
			},
		})
	}

	engine := NewForTest(&Handlers{}, nil, registrars...)

	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v2"+c.path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("audience=%s path=%s: expected 401 from prepended TrustedHeaderAuth, got %d",
				c.audience, c.path, w.Code)
		}
	}
}

// TestAudienceChainPublicLetsAnonymousThrough verifies that public-audience
// registrars do NOT get any chain prepended — the auth module owns its own
// /auth/login etc. gating.
func TestAudienceChainPublicLetsAnonymousThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registrar := framework.RouteRegistrar{
		Audience: framework.AudiencePublic,
		Name:     "probe.public",
		Register: func(g *gin.RouterGroup) {
			g.GET("/public-probe", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
		},
	}

	engine := NewForTest(&Handlers{}, nil, registrar)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/public-probe", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("public audience should not gate on auth: expected 200, got %d", w.Code)
	}
}

// TestAudienceChainSkipDefault verifies the escape hatch: a registrar
// that sets SkipDefaultChain=true is responsible for its own auth and
// the framework does not prepend.
func TestAudienceChainSkipDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registrar := framework.RouteRegistrar{
		Audience:         framework.AudiencePortal,
		Name:             "probe.skip",
		SkipDefaultChain: true,
		Register: func(g *gin.RouterGroup) {
			g.GET("/skip-probe", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
		},
	}

	engine := NewForTest(&Handlers{}, nil, registrar)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/skip-probe", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("SkipDefaultChain should bypass framework chain: expected 200, got %d", w.Code)
	}
}
