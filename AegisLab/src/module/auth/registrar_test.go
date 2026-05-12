package auth

import (
	"aegis/platform/framework"
	"aegis/platform/model"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRoutesPublicRegistrar(t *testing.T) {
	handler := &Handler{}
	r := RoutesPublic(handler)

	if r.Audience != framework.AudiencePublic {
		t.Fatalf("expected audience %q, got %q", framework.AudiencePublic, r.Audience)
	}
	if r.Name != "auth.public" {
		t.Fatalf("expected name %q, got %q", "auth.public", r.Name)
	}
	if r.Register == nil {
		t.Fatal("Register function must not be nil")
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v2")
	r.Register(group)

	routes := engine.Routes()
	expected := map[string]string{
		"/api/v2/auth/login":           "POST",
		"/api/v2/auth/register":        "POST",
		"/api/v2/auth/refresh":         "POST",
		"/api/v2/auth/logout":          "POST",
		"/api/v2/auth/change-password": "POST",
		"/api/v2/auth/profile":         "GET",
	}
	for path, method := range expected {
		found := false
		for _, ri := range routes {
			if ri.Path == path && ri.Method == method {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected route %s %s to be registered", method, path)
		}
	}
}

func TestRoutesSDKRegistrar(t *testing.T) {
	handler := &Handler{}
	r := RoutesSDK(handler)

	if r.Audience != framework.AudienceSDK {
		t.Fatalf("expected audience %q, got %q", framework.AudienceSDK, r.Audience)
	}
	if r.Name != "auth.sdk" {
		t.Fatalf("expected name %q, got %q", "auth.sdk", r.Name)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v2")
	r.Register(group)

	found := false
	for _, ri := range engine.Routes() {
		if ri.Path == "/api/v2/auth/api-key/token" && ri.Method == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected POST /api/v2/auth/api-key/token to be registered")
	}
}

func TestRoutesPortalRegistrar(t *testing.T) {
	handler := &Handler{}
	r := RoutesPortal(handler)

	if r.Audience != framework.AudiencePortal {
		t.Fatalf("expected audience %q, got %q", framework.AudiencePortal, r.Audience)
	}
	if r.Name != "auth.portal" {
		t.Fatalf("expected name %q, got %q", "auth.portal", r.Name)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v2")
	r.Register(group)

	expected := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v2/api-keys"},
		{"POST", "/api/v2/api-keys"},
		{"GET", "/api/v2/api-keys/:id"},
		{"DELETE", "/api/v2/api-keys/:id"},
		{"POST", "/api/v2/api-keys/:id/rotate"},
		{"POST", "/api/v2/api-keys/:id/disable"},
		{"POST", "/api/v2/api-keys/:id/enable"},
		{"POST", "/api/v2/api-keys/:id/revoke"},
	}
	for _, e := range expected {
		found := false
		for _, ri := range engine.Routes() {
			if ri.Path == e.path && ri.Method == e.method {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected route %s %s to be registered", e.method, e.path)
		}
	}
}

func TestPermissionsRegistrar(t *testing.T) {
	p := Permissions()
	if p.Module != "auth" {
		t.Fatalf("expected module %q, got %q", "auth", p.Module)
	}
	if p.Rules == nil {
		t.Fatal("Rules must not be nil (use empty slice)")
	}
}

func TestRoleGrantsRegistrar(t *testing.T) {
	rg := RoleGrants()
	if rg.Module != "auth" {
		t.Fatalf("expected module %q, got %q", "auth", rg.Module)
	}
	if rg.Grants == nil {
		t.Fatal("Grants must not be nil (use empty map)")
	}
}

func TestMigrationsRegistrar(t *testing.T) {
	m := Migrations()
	if m.Module != "auth" {
		t.Fatalf("expected module %q, got %q", "auth", m.Module)
	}
	if len(m.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(m.Entities))
	}
	if _, ok := m.Entities[0].(*model.APIKey); !ok {
		t.Fatalf("expected *model.APIKey, got %T", m.Entities[0])
	}
}
