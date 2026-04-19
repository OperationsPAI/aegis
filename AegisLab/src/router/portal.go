package router

import "github.com/gin-gonic/gin"

// Portal routes are now contributed by module registrars. The central router
// keeps this hook as a no-op compatibility shim while Phase 4 migrations land.
func SetupPortalV2Routes(v2 *gin.RouterGroup, handlers *Handlers) {
	_, _ = v2, handlers
}
