package project

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal contributes the project module's portal HTTP routes to
// the framework route registry. SDK routes under /projects/:project_id/*
// are owned by injection/execution and stay with those modules.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "project",
		Register: func(v2 *gin.RouterGroup) {
			projects := v2.Group("/projects", middleware.JWTAuth())
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
