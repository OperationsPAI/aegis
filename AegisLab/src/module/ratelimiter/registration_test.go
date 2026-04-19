package ratelimiter

import (
	"context"
	"testing"

	"aegis/framework"
)

func TestRoutesRegistrar(t *testing.T) {
	svc := &stubHandlerService{}
	h := NewHandler(svc)
	reg := Routes(h)

	if reg.Audience != framework.AudiencePortal {
		t.Errorf("expected audience %q, got %q", framework.AudiencePortal, reg.Audience)
	}
	if reg.Name != "ratelimiter" {
		t.Errorf("expected name %q, got %q", "ratelimiter", reg.Name)
	}
	if reg.Register == nil {
		t.Error("Register closure must not be nil")
	}
}

func TestPermissionsRegistrar(t *testing.T) {
	reg := Permissions()
	if reg.Module != "ratelimiter" {
		t.Errorf("expected module %q, got %q", "ratelimiter", reg.Module)
	}
	if reg.Rules != nil {
		t.Errorf("expected nil rules for ratelimiter, got %v", reg.Rules)
	}
}

func TestRoleGrantsRegistrar(t *testing.T) {
	reg := RoleGrants()
	if reg.Module != "ratelimiter" {
		t.Errorf("expected module %q, got %q", "ratelimiter", reg.Module)
	}
	if reg.Grants != nil {
		t.Errorf("expected nil grants for ratelimiter, got %v", reg.Grants)
	}
}

func TestMigrationsRegistrar(t *testing.T) {
	reg := Migrations()
	if reg.Module != "ratelimiter" {
		t.Errorf("expected module %q, got %q", "ratelimiter", reg.Module)
	}
	if reg.Entities != nil {
		t.Errorf("expected nil entities for ratelimiter, got %v", reg.Entities)
	}
}

func TestHandlerServiceInterface(t *testing.T) {
	var _ HandlerService = &stubHandlerService{}
	svc := &stubHandlerService{}
	h := NewHandler(svc)
	if h.service == nil {
		t.Fatal("handler service must not be nil")
	}
}

func TestAsHandlerService(t *testing.T) {
	var hs HandlerService = AsHandlerService(&Service{})
	if hs == nil {
		t.Fatal("AsHandlerService must return non-nil HandlerService")
	}
}

type stubHandlerService struct{}

func (s *stubHandlerService) GC(context.Context) (int, int, error) { return 0, 0, nil }
func (s *stubHandlerService) List(context.Context) (*RateLimiterListResp, error) {
	return &RateLimiterListResp{}, nil
}
func (s *stubHandlerService) Reset(context.Context, string) error { return nil }
