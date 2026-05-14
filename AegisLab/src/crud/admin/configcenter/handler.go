package configcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
)

// Handler hosts the configcenter admin HTTP surface. Mounted by the
// standalone `aegis-configcenter` binary; not wired into the
// monolith.
type Handler struct {
	center *defaultCenter
	audit  AuditWriter
}

func NewHandler(center *defaultCenter, audit AuditWriter) *Handler {
	return &Handler{center: center, audit: audit}
}

// SetReq is the body of PUT/POST writes. Value is held as raw JSON so
// scalars / objects / arrays all flow through unchanged.
type SetReq struct {
	Value  json.RawMessage `json:"value"`
	Reason string          `json:"reason,omitempty"`
}

// EntryResp mirrors Entry for stable wire format.
type EntryResp struct {
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Layer     Layer           `json:"layer"`
}

func (h *Handler) List(c *gin.Context) {
	ns := c.Param("namespace")
	entries, err := h.center.List(c.Request.Context(), ns)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make([]EntryResp, 0, len(entries))
	for _, e := range entries {
		raw, _ := json.Marshal(e.Value)
		resp = append(resp, EntryResp{
			Namespace: e.Namespace,
			Key:       e.Key,
			Value:     raw,
			Layer:     e.Layer,
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": resp})
}

func (h *Handler) Get(c *gin.Context) {
	ns := c.Param("namespace")
	key := c.Param("key")
	raw, layer, err := h.center.Get(c.Request.Context(), ns, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, err.Error())
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, EntryResp{
		Namespace: ns, Key: key, Value: raw, Layer: layer,
	})
}

func (h *Handler) Set(c *gin.Context) {
	ns := c.Param("namespace")
	key := c.Param("key")

	var req SetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Value) == 0 {
		dto.ErrorResponse(c, http.StatusBadRequest, "value required")
		return
	}

	old, _, _ := h.center.Get(c.Request.Context(), ns, key)
	if err := h.center.Set(c.Request.Context(), ns, key, []byte(req.Value)); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrForbiddenKey) {
			status = http.StatusForbidden
		}
		dto.ErrorResponse(c, status, err.Error())
		return
	}

	row := ConfigAudit{
		Namespace: ns,
		KeyPath:   key,
		Action:    string(ActionSet),
		OldValue:  old,
		NewValue:  []byte(req.Value),
		Reason:    req.Reason,
		CreatedAt: time.Now(),
	}
	tagActor(c, &row)
	if err := h.audit.Write(c.Request.Context(), row); err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "audit write failed: "+err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Delete(c *gin.Context) {
	ns := c.Param("namespace")
	key := c.Param("key")

	old, _, _ := h.center.Get(c.Request.Context(), ns, key)
	if err := h.center.Delete(c.Request.Context(), ns, key); err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	row := ConfigAudit{
		Namespace: ns,
		KeyPath:   key,
		Action:    string(ActionDelete),
		OldValue:  old,
		CreatedAt: time.Now(),
	}
	tagActor(c, &row)
	if err := h.audit.Write(c.Request.Context(), row); err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "audit write failed: "+err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// Watch streams every change under a namespace as SSE events. The
// remote configcenterclient subscribes here to drive its in-process
// Bind callers.
func (h *Handler) Watch(c *gin.Context) {
	ns := c.Param("namespace")

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	ch, unsub := h.center.Subscribe(ctx, ns)
	defer unsub()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			c.Render(-1, sse.Event{Event: "ping", Data: "ok"})
			c.Writer.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			c.Render(-1, sse.Event{Event: "change", Data: string(b)})
			c.Writer.Flush()
		}
	}
}

// History returns 501 until the config_audit-backed query lands. The
// route is registered so callers see a well-typed error rather than 404.
func (h *Handler) History(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "config history not implemented"})
}

func tagActor(c *gin.Context, row *ConfigAudit) {
	if uid, ok := middleware.GetCurrentUserID(c); ok {
		row.ActorID = &uid
	}
	if middleware.IsServiceToken(c) {
		// best-effort sub from the claims context
		if v, ok := c.Get("sub"); ok {
			if s, ok := v.(string); ok {
				row.ActorToken = s
			}
		}
	}
}
