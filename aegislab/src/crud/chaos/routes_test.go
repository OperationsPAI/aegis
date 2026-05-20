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
