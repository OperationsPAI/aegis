package cluster

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal contributes the cluster-status aggregator's portal route.
// The page is gated only by trusted-header auth — every authenticated
// portal user can see cluster health, matching the existing
// /system/health convention. No SDK scope guard here.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "cluster",
		Register: func(v2 *gin.RouterGroup) {
			cluster := v2.Group("/cluster", middleware.TrustedHeaderAuth())
			cluster.GET("/status", handler.GetClusterStatus)
		},
	}
}
