package notification

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"aegis/dto"
	"aegis/middleware"

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
