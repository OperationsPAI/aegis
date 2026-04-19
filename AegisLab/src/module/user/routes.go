package user

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesAdmin(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceAdmin,
		Name:     "user.admin",
		Register: func(v2 *gin.RouterGroup) {
			users := v2.Group("/users", middleware.JWTAuth())
			{
				roles := users.Group("/:user_id/roles")
				{
					roles.POST("/:role_id", middleware.RequireUserAssign, handler.AssignRole)
					roles.DELETE("/:role_id", middleware.RequireUserAssign, handler.RemoveRole)
				}

				projects := users.Group("/:user_id/projects")
				{
					projects.POST("/:project_id/roles/:role_id", middleware.RequireUserAssign, handler.AssignProject)
					projects.DELETE("/:project_id", middleware.RequireUserAssign, handler.RemoveProject)
				}

				permissions := users.Group("/:user_id/permissions")
				{
					permissions.POST("/assign", middleware.RequireUserAssign, handler.AssignPermissions)
					permissions.POST("/remove", middleware.RequireUserAssign, handler.RemovePermissions)
				}

				containers := users.Group("/:user_id/containers")
				{
					containers.POST("/:container_id/roles/:role_id", middleware.RequireUserAssign, handler.AssignContainer)
					containers.DELETE("/:container_id", middleware.RequireUserAssign, handler.RemoveContainer)
				}

				datasets := users.Group("/:user_id/datasets")
				{
					datasets.POST("/:dataset_id/roles/:role_id", middleware.RequireUserAssign, handler.AssignDataset)
					datasets.DELETE("/:dataset_id", middleware.RequireUserAssign, handler.RemoveDataset)
				}

				userRead := users.Group("", middleware.RequireUserRead)
				{
					userRead.GET("", handler.ListUsers)
					userRead.GET("/:user_id/detail", middleware.RequireAdminOrUserOwnership, handler.GetUserDetail)
				}

				users.POST("", middleware.RequireUserCreate, handler.CreateUser)
				users.PATCH("/:user_id", middleware.RequireUserUpdate, handler.UpdateUser)
				users.DELETE("/:user_id", middleware.RequireUserDelete, handler.DeleteUser)
			}
		},
	}
}
