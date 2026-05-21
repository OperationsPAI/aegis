package chaos

import (
	"testing"

	"aegis/platform/jwtkeys"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestRoutes_NoGinPanic guards against the reviewer's B1/B2: Gin
// `findWildcard` panics if any path segment contains more than one
// wildcard character. Wiring the registrar against a fresh gin.Engine
// triggers route-tree validation at registration time, so a panic
// here means a step-1 binary couldn't even boot.
func TestRoutes_NoGinPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/v1beta")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	Routes(&Handler{}, db, &jwtkeys.Verifier{}).Register(g)
}

// TestRoutes_Phase2eEndpointsMounted asserts the four phase 2e endpoints
// are wired with the (method, path) shape the SDK expects. A mismatch here
// turns into 404s in 2e-cli.
func TestRoutes_Phase2eEndpointsMounted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	Routes(&Handler{}, db, &jwtkeys.Verifier{}).Register(r.Group("/v1beta"))

	want := map[string]bool{
		"POST /v1beta/guided/resolve":               false,
		"POST /v1beta/guided/apply-next":            false,
		"GET /v1beta/systems/:sys/candidates":       false,
		"DELETE /v1beta/injections/by-task/:taskID": false,
	}
	for _, rt := range r.Routes() {
		if _, ok := want[rt.Method+" "+rt.Path]; ok {
			want[rt.Method+" "+rt.Path] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("route not mounted: %s", key)
		}
	}
}
