package chaossystem

import (
	"aegis/platform/consts"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes registers all chaossystem endpoints once. The four read paths
// that the portal's InjectionCreate wizard needs (list / get /
// inject-candidates) stay open to any authenticated caller — systems are
// a platform-wide catalog with no per-project ownership to key RBAC off.
// The remaining reads (metadata / chart / prerequisites) and all writes
// keep their RBAC gates, matching the prior Admin audience contract.
func Routes(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "chaossystem",
		Register: func(v2 *gin.RouterGroup) {
			systems := v2.Group("/systems")
			{
				// Open reads — needed by the portal injection wizard.
				systems.GET("", handler.ListSystems)
				systems.GET("/:id", handler.GetSystem)
				systems.GET("/by-name/:name/inject-candidates", handler.ListInjectCandidates)

				// Operator-facing reads — keep system_read gate.
				systemRead := systems.Group("", middleware.RequireSystemRead)
				{
					systemRead.GET("/:id/metadata", handler.ListMetadata)
					systemRead.GET("/by-name/:name/chart", handler.GetSystemChart)
					systemRead.GET("/by-name/:name/prerequisites", handler.ListPrerequisites)
				}

				systemConfigure := systems.Group("", middleware.RequireSystemConfigure)
				{
					systemConfigure.POST("", handler.CreateSystem)
					systemConfigure.POST("/onboard", handler.OnboardSystem)
					systemConfigure.PUT("/:id", handler.UpdateSystem)
					systemConfigure.POST("/:id/metadata", handler.UpsertMetadata)
					systemConfigure.POST("/reseed", handler.ReseedSystems)
					systemConfigure.POST("/by-name/:name/prerequisites/:id/mark", handler.MarkPrerequisite)
				}

				systems.GET("/by-name/:name/export-seed",
					middleware.RequireSystemRead, handler.ExportSeed)

				systems.DELETE("/:id", middleware.RequirePermission(consts.PermSystemManage), handler.DeleteSystem)
			}
		},
	}
}
