// Route-wiring smoke test. Constructs the registrars + mounts them on a
// gin engine the same way router.New would. Catches the regression where
// both portal + SDK registrars used to fire on /api/v2/pages/* and gin
// would panic on duplicate handler registration.
package pages

import (
	"testing"

	"aegis/platform/framework"

	"github.com/gin-gonic/gin"
)

func TestRouteRegistration_NoDuplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newServiceForTest(t)
	h := NewHandler(svc)
	rh := NewRenderHandler(svc)

	router := gin.New()
	v2 := router.Group("/api/v2")

	// Mount both registrars exactly as platform/router does.
	registrars := []framework.RouteRegistrar{
		RoutesPortal(h),
		RoutesEngine(rh),
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("gin panicked on route registration: %v", r)
		}
	}()
	for _, r := range registrars {
		if r.BasePath != "" {
			r.Register(router.Group(r.BasePath))
			continue
		}
		r.Register(v2)
	}

	wantPaths := []string{
		"/api/v2/pages",
		"/api/v2/pages/public",
		"/api/v2/pages/:id",
		"/api/v2/pages/:id/upload",
		"/p/:slug",
		"/p/:slug/*filepath",
		"/static/pages/*filepath",
	}
	have := map[string]bool{}
	for _, ri := range router.Routes() {
		have[ri.Path] = true
	}
	for _, p := range wantPaths {
		if !have[p] {
			t.Errorf("route %q not registered; routes: %v", p, router.Routes())
		}
	}
}
