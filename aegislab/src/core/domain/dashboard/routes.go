package dashboard

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal contributes the dashboard aggregator's portal HTTP route.
// The underlying per-resource list services already enforce read-side RBAC,
// so project_read is the necessary and sufficient gate here.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "dashboard",
		Register: func(v2 *gin.RouterGroup) {
			projects := v2.Group("/projects", middleware.TrustedHeaderAuth(), middleware.RequireProjectRead)
			projects.GET("/:project_id/dashboard", handler.GetProjectDashboard)
		},
	}
}
