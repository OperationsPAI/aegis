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
				g.POST("/buckets", handler.CreateBucket)
				g.POST("/buckets/:bucket/presign-put", handler.PresignPut)
				g.POST("/buckets/:bucket/presign-get", handler.PresignGet)
				// *key catch-all routes allow keys with slashes (e.g. a/b/c.txt).
				// The handler trims the leading "/" from c.Param("key").
				g.GET("/buckets/:bucket/objects/*key", handler.InlineGet)
				g.HEAD("/buckets/:bucket/objects/*key", handler.Stat)
				g.DELETE("/buckets/:bucket/objects/*key", handler.Delete)
				g.GET("/buckets/:bucket/objects", handler.List)
				// Driver-level list (storage source-of-truth), distinct
				// from /objects above which queries the metadata DB.
				g.GET("/buckets/:bucket/object-list", handler.ListObjects)
				// StreamGet is now redundant with InlineGet (both accept
				// wildcard keys) but retained for backward compatibility.
				g.GET("/buckets/:bucket/stream/*key", handler.StreamGet)
				// Copy / Move within a bucket.
				g.POST("/buckets/:bucket/copy", handler.CopyObject)
				// Batch delete — returns per-key deleted/failed lists.
				g.POST("/buckets/:bucket/delete-batch", handler.BatchDelete)
				// ZIP streaming — streams selected keys as an archive.
				g.POST("/buckets/:bucket/zip", handler.ZipObjects)
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
