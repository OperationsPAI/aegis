package notification

import (
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Routes registers every notification endpoint once. /inbox/* is the
// per-user inbox CRUD + SSE feeding aegis-ui (human-user-only gate);
// /events/:publish is the cross-service publish endpoint (service-token
// or API-key, RBAC scoping TBD per the prior comment).
func Routes(handler *InboxHandler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "notification",
		Register: func(v2 *gin.RouterGroup) {
			inbox := v2.Group("/inbox", middleware.RequireHumanUserAuth())
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

			events := v2.Group("/events")
			{
				events.POST(":publish", handler.PublishEvent)
			}
		},
	}
}
