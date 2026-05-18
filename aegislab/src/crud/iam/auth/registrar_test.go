package auth

import (
	"aegis/platform/framework"
	"aegis/platform/model"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRoutesRegistrar(t *testing.T) {
	handler := &Handler{}
	r := Routes(handler)

	if r.Audience != framework.AudiencePublic {
		t.Fatalf("expected audience %q, got %q", framework.AudiencePublic, r.Audience)
	}
	if r.Name != "auth" {
		t.Fatalf("expected name %q, got %q", "auth", r.Name)
	}
	if r.Register == nil {
		t.Fatal("Register function must not be nil")
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v2")
	r.Register(group)

	type key struct {
		method string
		path   string
	}
	expected := []key{
		{"POST", "/api/v2/auth/login"},
		{"POST", "/api/v2/auth/register"},
		{"POST", "/api/v2/auth/refresh"},
		{"POST", "/api/v2/auth/api-key/token"},
		{"POST", "/api/v2/auth/logout"},
		{"POST", "/api/v2/auth/change-password"},
		{"GET", "/api/v2/auth/profile"},
		{"GET", "/api/v2/api-keys"},
		{"POST", "/api/v2/api-keys"},
		{"GET", "/api/v2/api-keys/:id"},
		{"DELETE", "/api/v2/api-keys/:id"},
		{"POST", "/api/v2/api-keys/:id/rotate"},
		{"POST", "/api/v2/api-keys/:id/disable"},
		{"POST", "/api/v2/api-keys/:id/enable"},
		{"POST", "/api/v2/api-keys/:id/revoke"},
	}
	counts := make(map[key]int)
	for _, ri := range engine.Routes() {
		counts[key{ri.Method, ri.Path}]++
	}
	for _, e := range expected {
		switch counts[e] {
		case 0:
			t.Errorf("expected route %s %s to be registered", e.method, e.path)
		case 1:
			// good
		default:
			t.Errorf("route %s %s registered %d times", e.method, e.path, counts[e])
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
