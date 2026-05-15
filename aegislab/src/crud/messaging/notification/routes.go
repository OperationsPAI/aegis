package notification

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RoutesPortalInbox mounts the per-user inbox CRUD + SSE that backs
// aegis-ui's NotificationBell / InboxPage.
func RoutesPortalInbox(handler *InboxHandler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "notification.portal.inbox",
		Register: func(v2 *gin.RouterGroup) {
			inbox := v2.Group("/inbox", middleware.TrustedHeaderAuth(), middleware.RequireHumanUserAuth())
			{
				inbox.GET("", handler.List)
				inbox.GET("/unread-count", handler.UnreadCount)
				inbox.GET("/stream", handler.InboxStream)
				inbox.POST("/read-all", handler.MarkAllRead)
				inbox.POST("/:id/read", handler.MarkRead)
				inbox.POST("/:id/archive", handler.Archive)
				inbox.GET("/subscriptions", handler.ListSubscriptions)
				inbox.PUT("/subscriptions", handler.SetSubscription)
			}
		},
	}
}

// RoutesSDKIngestion mounts the cross-service publish endpoint. In
// the monolith it gives producers an out-of-process alternative; in
// the standalone notification microservice it is the only producer
// entrypoint. Service tokens authenticate via JWTAuth — RBAC scoping
// (which services are allowed to publish which categories) is a
// follow-up.
func RoutesSDKIngestion(handler *InboxHandler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudienceSDK,
		Name:     "notification.sdk.ingestion",
		Register: func(v2 *gin.RouterGroup) {
			events := v2.Group("/events", middleware.TrustedHeaderAuth())
			{
				events.POST(":publish", handler.PublishEvent)
			}
		},
	}
}
