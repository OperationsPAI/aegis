package chaos

import (
	"testing"

	"github.com/gin-gonic/gin"
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
	Routes(&Handler{}).Register(g)
}
