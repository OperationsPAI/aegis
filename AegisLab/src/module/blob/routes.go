package blob

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal mounts the human-portal-facing blob endpoints.
// Authentication is JWT human-user; per-bucket ACL is enforced by the
// handler.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "blob.portal",
		Register: func(v2 *gin.RouterGroup) {
			g := v2.Group("/blob", middleware.JWTAuth())
			{
				g.POST("/buckets/:bucket/presign-put", handler.PresignPut)
				g.POST("/buckets/:bucket/presign-get", handler.PresignGet)
				g.GET("/buckets/:bucket/objects/:key", handler.InlineGet)
				g.HEAD("/buckets/:bucket/objects/:key", handler.Stat)
				g.DELETE("/buckets/:bucket/objects/:key", handler.Delete)
				g.GET("/buckets/:bucket/objects", handler.List)
			}

			// /raw/:token is auth-free (the HMAC token IS the auth).
			// Mounted under /blob so it sits alongside the rest of the
			// surface but bypasses JWTAuth.
			v2.GET("/blob/raw/:token", handler.Raw)
			v2.PUT("/blob/raw/:token", handler.Raw)
		},
	}
}

// RoutesSDK mounts the cross-service blob endpoints (same handlers,
// service-token auth). Producers calling the standalone aegis-blob
// binary land here.
func RoutesSDK(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "blob.sdk",
		Register: func(v2 *gin.RouterGroup) {
			g := v2.Group("/blob", middleware.JWTAuth())
			{
				g.POST("/buckets/:bucket/presign-put", handler.PresignPut)
				g.POST("/buckets/:bucket/presign-get", handler.PresignGet)
				g.HEAD("/buckets/:bucket/objects/:key", handler.Stat)
				g.DELETE("/buckets/:bucket/objects/:key", handler.Delete)
				g.GET("/buckets/:bucket/objects", handler.List)
			}
		},
	}
}
