package notification

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	redisgo "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// InboxHandler hosts the per-user inbox endpoints — what the
// `NotificationBell` and `InboxPage` primitives in aegis-ui talk to.
type InboxHandler struct {
	repo      *InboxRepository
	subs      *SubscriptionRepository
	publisher Publisher
	pubsub    PubSubClient
}

// PubSubClient is the minimum interface the per-user SSE stream
// needs. The default in-process implementation wraps
// aegis/infra/redis.Gateway; the standalone service may swap it for a
// distributed alternative later without touching the handler.
type PubSubClient interface {
	Subscribe(ctx context.Context, channel string) (*redisgo.PubSub, error)
}

func NewInboxHandler(repo *InboxRepository, subs *SubscriptionRepository, publisher Publisher, ps PubSubClient) *InboxHandler {
	return &InboxHandler{repo: repo, subs: subs, publisher: publisher, pubsub: ps}
}

func currentUserID(c *gin.Context) (int, bool) {
	return middleware.GetCurrentUserID(c)
}

// List returns paginated inbox notifications for the current user
//
//	@Summary		List inbox notifications
//	@Description	List the current user's inbox notifications with optional category, severity, unread, and cursor filters
//	@Tags			Notification
//	@ID				list_inbox_notifications
//	@Produce		json
//	@Security		BearerAuth
//	@Param			unread_only	query		bool						false	"Return only unread notifications"
//	@Param			category	query		string						false	"Filter by category"
//	@Param			severity	query		string						false	"Filter by severity"
//	@Param			cursor		query		int							false	"Pagination cursor (notification ID)"
//	@Param			limit		query		int							false	"Page size"
//	@Success		200			{object}	ListInboxResp				"Inbox notifications listed successfully"
//	@Failure		401			{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500			{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/inbox [get]
//	@x-api-type		{"portal":"true"}
func (h *InboxHandler) List(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	filter := ListFilter{UserID: uid}
	filter.UnreadOnly = c.Query("unread_only") == "true"
	filter.Category = c.Query("category")
	filter.Severity = c.Query("severity")
	if s := c.Query("cursor"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			filter.Cursor = v
		}
	}
	if s := c.Query("limit"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			filter.Limit = v
		}
	}

	rows, err := h.repo.List(c.Request.Context(), filter)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]InboxItem, len(rows))
	for i := range rows {
		items[i] = ToInboxItem(&rows[i])
	}
	unread, _ := h.repo.UnreadCount(c.Request.Context(), uid)

	resp := ListInboxResp{Items: items, UnreadCount: unread}
	if len(rows) > 0 && len(rows) == filter.Limit {
		resp.NextCursor = strconv.FormatInt(rows[len(rows)-1].ID, 10)
	}
	c.JSON(http.StatusOK, resp)
}

// UnreadCount returns the unread notification count for the current user
//
//	@Summary		Get unread notification count
//	@Description	Return the number of unread inbox notifications for the current user (used by the notification bell)
//	@Tags			Notification
//	@ID				get_inbox_unread_count
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	UnreadCountResp				"Unread count retrieved successfully"
//	@Failure		401	{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500	{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/inbox/unread-count [get]
//	@x-api-type		{"portal":"true"}
func (h *InboxHandler) UnreadCount(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	n, err := h.repo.UnreadCount(c.Request.Context(), uid)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, UnreadCountResp{UnreadCount: n})
}

// MarkRead marks a single inbox notification as read
//
//	@Summary		Mark notification as read
//	@Description	Mark a single inbox notification as read for the current user
//	@Tags			Notification
//	@ID				mark_inbox_notification_read
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int							true	"Notification ID"
//	@Success		204	{object}	dto.GenericResponse[any]	"Notification marked as read"
//	@Failure		400	{object}	dto.GenericResponse[any]	"Invalid notification id"
//	@Failure		401	{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500	{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/inbox/{id}/read [post]
//	@x-api-type		{"portal":"true"}
func (h *InboxHandler) MarkRead(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.repo.MarkRead(c.Request.Context(), uid, id); err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// MarkAllRead marks every (or every category-scoped) inbox notification read
//
//	@Summary		Mark all notifications as read
//	@Description	Mark all inbox notifications (optionally filtered by category) as read for the current user
//	@Tags			Notification
//	@ID				mark_all_inbox_notifications_read
//	@Produce		json
//	@Security		BearerAuth
//	@Param			category	query		string						false	"Restrict to a single category"
//	@Success		200			{object}	map[string]int				"Number of notifications updated"
//	@Failure		401			{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500			{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/inbox/read-all [post]
//	@x-api-type		{"portal":"true"}
func (h *InboxHandler) MarkAllRead(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	category := c.Query("category")
	n, err := h.repo.MarkAllRead(c.Request.Context(), uid, category)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": n})
}

// Archive archives a single inbox notification
//
//	@Summary		Archive notification
//	@Description	Archive a single inbox notification for the current user
//	@Tags			Notification
//	@ID				archive_inbox_notification
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int							true	"Notification ID"
//	@Success		204	{object}	dto.GenericResponse[any]	"Notification archived"
//	@Failure		400	{object}	dto.GenericResponse[any]	"Invalid notification id"
//	@Failure		401	{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500	{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/inbox/{id}/archive [post]
//	@x-api-type		{"portal":"true"}
func (h *InboxHandler) Archive(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.repo.Archive(c.Request.Context(), uid, id); err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// InboxStream is a per-user SSE stream of "you have a new
// notification" events. The client uses it to bump the bell counter
// and prepend new rows without polling. The persisted source of
// truth remains `GET /inbox`; this stream is the freshness layer.
// InboxStream is a per-user SSE stream of new-notification events
//
//	@Summary		Stream inbox notifications
//	@Description	Server-Sent Events stream that pushes a `notification` event whenever a new inbox notification arrives for the current user; emits periodic `ping` heartbeats
//	@Tags			Notification
//	@ID				stream_inbox_notifications
//	@Produce		text/event-stream
//	@Security		BearerAuth
//	@Success		200	{string}	string						"SSE stream of notification events"
//	@Failure		401	{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500	{object}	dto.GenericResponse[any]	"Internal server error"
//	@Failure		503	{object}	dto.GenericResponse[any]	"Pubsub backend not configured"
//	@Router			/api/v2/inbox/stream [get]
//	@x-api-type		{"portal":"true"}
func (h *InboxHandler) InboxStream(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if h.pubsub == nil {
		dto.ErrorResponse(c, http.StatusServiceUnavailable, "pubsub not configured")
		return
	}

	ps, err := h.pubsub.Subscribe(c.Request.Context(), inboxChannelKey(uid))
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = ps.Close() }()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Flush()

	ch := ps.Channel()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			c.Render(-1, sse.Event{Event: "ping", Data: "ok"})
			c.Writer.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			c.Render(-1, sse.Event{Event: "notification", Data: msg.Payload})
			c.Writer.Flush()
		}
	}
}

// ---- Subscriptions (preferences) ----

// ListSubscriptions returns the current user's notification subscription preferences
//
//	@Summary		List notification subscriptions
//	@Description	List the current user's per-(category, channel) notification subscription preferences
//	@Tags			Notification
//	@ID				list_inbox_subscriptions
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	map[string][]SubscriptionResp	"Subscriptions listed successfully"
//	@Failure		401	{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		500	{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/api/v2/inbox/subscriptions [get]
//	@x-api-type		{"portal":"true"}
func (h *InboxHandler) ListSubscriptions(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	subs, err := h.subs.ListForUser(c.Request.Context(), uid)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make([]SubscriptionResp, len(subs))
	for i, s := range subs {
		resp[i] = SubscriptionResp{Category: s.Category, Channel: s.Channel, Enabled: s.Enabled}
	}
	c.JSON(http.StatusOK, gin.H{"items": resp})
}

// SetSubscription upserts a single notification subscription preference
//
//	@Summary		Set notification subscription
//	@Description	Upsert the current user's subscription preference for a (category, channel) pair
//	@Tags			Notification
//	@ID				set_inbox_subscription
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		SetSubscriptionReq			true	"Subscription preference"
//	@Success		200		{object}	SubscriptionResp			"Subscription updated successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]	"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/inbox/subscriptions [put]
//	@x-api-type		{"portal":"true"}
func (h *InboxHandler) SetSubscription(c *gin.Context) {
	uid, ok := currentUserID(c)
	if !ok {
		dto.ErrorResponse(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req SetSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	sub := NotificationSubscription{
		UserID:   uid,
		Category: req.Category,
		Channel:  req.Channel,
		Enabled:  req.Enabled,
	}
	if err := h.subs.Set(c.Request.Context(), sub); err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, SubscriptionResp{
		Category: sub.Category, Channel: sub.Channel, Enabled: sub.Enabled,
	})
}

// ---- Cross-service ingestion (the "microservice front door") ----

// PublishEvent accepts a producer's event over HTTP. In the monolith
// nobody hits this — producers use the in-process Publisher
// directly. In the standalone notification microservice this is
// where every producer in the wider system arrives.
//
// Authn for this endpoint is JWT (any service token; the SSO already
// issues those via client_credentials), then a simple role/scope
// gate happens at the router level.
// PublishEvent ingests a producer event over HTTP and fans it out
//
//	@Summary		Publish notification event
//	@Description	Cross-service ingestion endpoint: accept a producer's notification event and fan it out to recipients. Used by the standalone notification microservice; in the monolith producers call the in-process Publisher directly.
//	@Tags			Notification
//	@ID				publish_notification_event
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		PublishEventReq				true	"Notification event"
//	@Success		200		{object}	PublishEventResp			"Event published successfully"
//	@Success		202		{object}	map[string]bool				"Event dropped by dedupe"
//	@Failure		400		{object}	dto.GenericResponse[any]	"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/events:publish [post]
//	@x-api-type		{"portal":"true"}
func (h *InboxHandler) PublishEvent(c *gin.Context) {
	var req PublishEventReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	res, err := h.publisher.Publish(c.Request.Context(), req.ToEvent())
	if err != nil {
		if errors.Is(err, ErrEventDropped) {
			c.JSON(http.StatusAccepted, gin.H{"dropped": true})
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, PublishResultToResp(res))
}

// guard against accidental import-cycle by referencing the package
// internals that the handler depends on
var _ = logrus.New
