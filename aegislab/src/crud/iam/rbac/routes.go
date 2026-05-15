package rbac

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

func RoutesAdmin(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceAdmin,
		Name:     "rbac.admin",
		Register: func(v2 *gin.RouterGroup) {
			roles := v2.Group("/roles", middleware.TrustedHeaderAuth())
			{
				permissions := roles.Group("/:role_id/permissions")
				{
					permissions.POST("/assign", middleware.RequireRoleGrant, handler.AssignRolePermissions)
					permissions.POST("/remove", middleware.RequireRoleRevoke, handler.RemoveRolePermissions)
				}

				users := roles.Group("/:role_id/users")
				{
					users.GET("", middleware.RequireRoleRead, handler.ListUsersFromRole)
				}

				roleRead := roles.Group("", middleware.RequireRoleRead)
				{
					roleRead.GET("/:role_id", handler.GetRole)
					roleRead.GET("", handler.ListRoles)
				}

				roles.POST("", middleware.RequireRoleCreate, handler.CreateRole)
				roles.PATCH("/:role_id", middleware.RequireRoleUpdate, handler.UpdateRole)
				roles.DELETE("/:role_id", middleware.RequireRoleDelete, handler.DeleteRole)
			}

			permissions := v2.Group("/permissions", middleware.TrustedHeaderAuth())
			{
				roles := permissions.Group("/:permission_id/roles")
				{
					roles.GET("", middleware.RequirePermissionRead, handler.ListRolesFromPermission)
				}

				permRead := permissions.Group("", middleware.RequirePermissionRead)
				{
					permRead.GET("", handler.ListPermissions)
					permRead.GET("/:permission_id", handler.GetPermission)
				}
			}

			resources := v2.Group("/resources", middleware.TrustedHeaderAuth())
			{
				resourceRead := resources.Group("", middleware.RequirePermissionRead)
				{
					permissions := resourceRead.Group("/:resource_id/permissions")
					{
						permissions.GET("", handler.ListResourcePermissions)
					}

					resourceRead.GET("/:resource_id", handler.GetResource)
					resourceRead.GET("", handler.ListResources)
				}
			}
		},
	}
}
