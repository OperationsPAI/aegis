package sso

import (
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterAdminRoutes mounts the spec-§5 `/v1/*` admin REST surface on the
// SSO process's gin engine. Routes auth via the standard JWTAuth middleware
// — JWTAuth accepts both user tokens and service tokens; per-route gating
// (system admin or service token) is enforced inside each handler.
func RegisterAdminRoutes(engine *gin.Engine, handler *AdminHandler) {
	v1 := engine.Group("/v1", middleware.JWTAuth())
	{
		users := v1.Group("/users")
		{
			users.GET("/:id", handler.GetUser)
			users.GET("/:id/grants", handler.ListUserGrants)
		}
		v1.POST("/users/batch", handler.GetUsersBatch)
		v1.POST("/users/list", handler.ListUsers)

		v1.POST("/check", handler.Check)
		v1.POST("/check/batch", handler.CheckBatch)

		v1.POST("/permissions/register", handler.RegisterPermissions)

		grants := v1.Group("/grants")
		{
			grants.POST("", handler.Grant)
			grants.DELETE("", handler.Revoke)
		}

		v1.GET("/scopes/:scope_type/:scope_id/users", handler.ListScopeUsers)
	}
}
