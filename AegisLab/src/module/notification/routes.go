package notification

import (
	"aegis/framework"
	"aegis/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortal contributes the notification SSE endpoint to the portal
// audience. The route was previously registered in router/portal.go and
// is removed from that central file in the same change.
func RoutesPortal(handler *Handler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "notification.portal",
		Register: func(v2 *gin.RouterGroup) {
			notifications := v2.Group("/notifications", middleware.JWTAuth())
			{
				notifications.GET("/stream", handler.GetStream)
			}
		},
	}
}
