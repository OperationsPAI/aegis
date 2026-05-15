package project

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal contributes the project module's portal HTTP routes under
// /projects/* to the framework route registry. Project-scoped SDK endpoints
// under /projects/:project_id/* (injection/execution/task/trace operations)
// are owned by those modules and will migrate in their own Phase-4 PRs.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "project",
		Register: func(v2 *gin.RouterGroup) {
			projects := v2.Group("/projects", middleware.TrustedHeaderAuth())
			{
				projectRead := projects.Group("", middleware.RequireProjectRead)
				{
					projectRead.GET("/:project_id", handler.GetProjectDetail)
					projectRead.GET("", handler.ListProjects)
				}

				projects.POST("", middleware.RequireProjectCreate, handler.CreateProject)
				projects.PATCH("/:project_id", middleware.RequireProjectUpdate, handler.UpdateProject)
				projects.PATCH("/:project_id/labels", middleware.RequireProjectUpdate, handler.ManageProjectCustomLabels)
				projects.DELETE("/:project_id", middleware.RequireProjectDelete, handler.DeleteProject)
			}
		},
	}
}
