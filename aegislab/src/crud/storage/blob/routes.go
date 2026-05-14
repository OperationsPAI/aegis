package blob

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

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
				g.GET("/buckets", handler.ListBuckets)
				g.POST("/buckets/:bucket/presign-put", handler.PresignPut)
				g.POST("/buckets/:bucket/presign-get", handler.PresignGet)
				g.GET("/buckets/:bucket/objects/:key", handler.InlineGet)
				g.HEAD("/buckets/:bucket/objects/:key", handler.Stat)
				g.DELETE("/buckets/:bucket/objects/:key", handler.Delete)
				g.GET("/buckets/:bucket/objects", handler.List)
				// Driver-level list (storage source-of-truth), distinct
				// from /objects above which queries the metadata DB.
				g.GET("/buckets/:bucket/object-list", handler.ListObjects)
				// Streaming GET that accepts keys-with-slashes
				// (zip streaming, file tree responses). Distinct from
				// /objects/:key which matches single-segment keys only.
				g.GET("/buckets/:bucket/stream/*key", handler.StreamGet)
			}

			// /raw/:token is auth-free (the HMAC token IS the auth).
			// Mounted under /blob so it sits alongside the rest of the
			// surface but bypasses JWTAuth.
			v2.GET("/blob/raw/:token", handler.Raw)
			v2.PUT("/blob/raw/:token", handler.Raw)
		},
	}
}

// RoutesPortal already accepts both human and service tokens (JWTAuth
// without RequireHumanUserAuth), so producers calling the standalone
// aegis-blob binary use the same routes.
