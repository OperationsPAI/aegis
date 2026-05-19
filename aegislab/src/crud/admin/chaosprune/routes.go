package chaosprune

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes is the chaosprune module's portal route registrar. The whole
// surface is admin-gated.
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "chaosprune",
		Register: func(v2 *gin.RouterGroup) {
			admin := v2.Group("/admin/chaos",
				middleware.TrustedHeaderAuth(),
				middleware.RequireSystemAdmin(),
			)
			admin.POST("/prune", handler.Prune)
		},
	}
}
