package team

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "team",
		Register: func(v2 *gin.RouterGroup) {
			teams := v2.Group("/teams", middleware.TrustedHeaderAuth())
			{
				teams.POST("", middleware.RequireTeamCreate, handler.CreateTeam)
				teams.GET("", middleware.RequireTeamRead, handler.ListTeams)

				teamAdmin := teams.Group("/:team_id", middleware.RequireTeamAdminAccess)
				{
					teamAdmin.PATCH("", handler.UpdateTeam)
					teamAdmin.DELETE("", handler.DeleteTeam)

					teamManagement := teamAdmin.Group("/members")
					teamManagement.POST("", handler.AddTeamMember)
					teamManagement.DELETE("/:user_id", handler.RemoveTeamMember)
					teamManagement.PATCH("/:user_id/role", handler.UpdateTeamMemberRole)
				}

				teamMember := teams.Group("", middleware.RequireTeamMemberAccess)
				{
					teamMember.GET("/:team_id", handler.GetTeamDetail)
					teamMember.GET("/:team_id/members", handler.ListTeamMembers)
					teamMember.GET("/:team_id/projects", handler.ListTeamProjects)
				}
			}
		},
	}
}
