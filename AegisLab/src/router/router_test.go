package router

import (
	"aegis/framework"
	"aegis/middleware"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func testRegistrars() []framework.RouteRegistrar {
	return []framework.RouteRegistrar{
		{
			Audience: framework.AudiencePortal,
			Name:     "label.portal",
			Register: func(v2 *gin.RouterGroup) {
				v2.Group("/labels", middleware.JWTAuth())
			},
		},
		{
			Audience: framework.AudienceAdmin,
			Name:     "system.admin",
			Register: func(v2 *gin.RouterGroup) {
				system := v2.Group("/system", middleware.JWTAuth(), middleware.RequireSystemRead)
				system.Group("/audit", middleware.RequireAuditRead).GET("", func(c *gin.Context) {})
				system.Group("/configs").GET("", func(c *gin.Context) {})
				system.Group("/monitor").GET("/info", func(c *gin.Context) {})
				system.GET("/health", func(c *gin.Context) {})
			},
		},
	}
}

func TestRouterSeparatesRouteGroups(t *testing.T) {
	registrars := append(testRegistrars(),
		framework.RouteRegistrar{
			Audience: framework.AudiencePublic,
			Name:     "auth.public",
			Register: func(v2 *gin.RouterGroup) {
				v2.POST("/auth/login", func(c *gin.Context) {})
			},
		},
		framework.RouteRegistrar{
			Audience: framework.AudienceSDK,
			Name:     "auth.sdk",
			Register: func(v2 *gin.RouterGroup) {
				v2.POST("/auth/api-key/token", func(c *gin.Context) {})
			},
		},
		framework.RouteRegistrar{
			Audience: framework.AudiencePortal,
			Name:     "auth.portal",
			Register: func(v2 *gin.RouterGroup) {
				v2.GET("/api-keys", func(c *gin.Context) {})
			},
		},
		framework.RouteRegistrar{
			Audience: framework.AudiencePortal,
			Name:     "test.execution",
			Register: func(v2 *gin.RouterGroup) {
				executions := v2.Group("/executions")
				executions.GET("/labels", func(c *gin.Context) {})
			},
		},
		framework.RouteRegistrar{
			Audience: framework.AudiencePortal,
			Name:     "test.project",
			Register: func(v2 *gin.RouterGroup) {
				projects := v2.Group("/projects")
				projects.GET("/:project_id", func(c *gin.Context) {})
			},
		},
		framework.RouteRegistrar{
			Audience: framework.AudienceSDK,
			Name:     "test.sdk",
			Register: func(v2 *gin.RouterGroup) {
				v2.GET("/sdk/datasets", func(c *gin.Context) {})
			},
		},
	)
	engine := NewForTest(&Handlers{}, nil, registrars...)
	routes := engine.Routes()

	requiredPrefixes := []string{
		"/api/v2/auth",
		"/api/v2/projects",
		"/api/v2/executions",
		"/api/v2/sdk",
		"/api/v2/system/audit",
		"/api/v2/system/configs",
		"/api/v2/system/monitor",
		"/api/v2/system/health",
		"/docs/",
	}

	for _, prefix := range requiredPrefixes {
		if !hasRoutePrefix(routes, prefix) {
			t.Fatalf("expected route prefix %q to be registered", prefix)
		}
	}
}

func TestRouterRegistersModuleContributedRoutes(t *testing.T) {
	engine := New(Params{
		Handlers: &Handlers{},
		Registrars: []framework.RouteRegistrar{
			{
				Audience: framework.AudienceSDK,
				Name:     "test.sdk",
				Register: func(v2 *gin.RouterGroup) {
					v2.GET("/sdk/evaluations", func(c *gin.Context) {})
				},
			},
			{
				Audience: framework.AudienceAdmin,
				Name:     "test.user",
				Register: func(v2 *gin.RouterGroup) {
					v2.GET("/users", func(c *gin.Context) {})
				},
			},
		},
	})

	if !hasRoutePrefix(engine.Routes(), "/api/v2/users") {
		t.Fatalf("expected user route prefix to be registered from module contribution")
	}
	if !hasRoutePrefix(engine.Routes(), "/api/v2/sdk") {
		t.Fatalf("expected sdk route prefix to be registered from module contribution")
	}
}

func hasRoutePrefix(routes []gin.RouteInfo, prefix string) bool {
	for _, route := range routes {
		if len(route.Path) >= len(prefix) && route.Path[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func TestSwaggerDocEndpointServesRegisteredSpec(t *testing.T) {
	engine := NewForTest(&Handlers{}, nil, testRegistrars()...)

	req := httptest.NewRequest(http.MethodGet, "/docs/doc.json", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected swagger doc endpoint status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "/api/v2/auth/login") {
		t.Fatalf("expected swagger doc to include auth login path")
	}
}
