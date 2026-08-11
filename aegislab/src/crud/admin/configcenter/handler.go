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

// List returns every entry under a configcenter namespace
//
//	@Summary		List config entries
//	@Description	List all config entries (with merged layer info) under a namespace
//	@Tags			Config Center
//	@ID				list_config_entries
//	@Produce		json
//	@Security		BearerAuth
//	@Param			namespace	path		string						true	"Config namespace"
//	@Success		200			{object}	map[string][]EntryResp		"Config entries listed successfully"
//	@Failure		401			{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500			{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/config/{namespace} [get]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Get returns a single config entry
//
//	@Summary		Get config entry
//	@Description	Get the merged value and source layer for a single config key
//	@Tags			Config Center
//	@ID				get_config_entry
//	@Produce		json
//	@Security		BearerAuth
//	@Param			namespace	path		string						true	"Config namespace"
//	@Param			key			path		string						true	"Config key"
//	@Success		200			{object}	EntryResp					"Config entry retrieved successfully"
//	@Failure		401			{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		404			{object}	dto.GenericResponse[any]	"Config entry not found"
//	@Failure		500			{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/config/{namespace}/{key} [get]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Set writes a config entry into the dynamic layer
//
//	@Summary		Set config entry
//	@Description	Write or overwrite a config entry value in the dynamic layer; requires system admin
//	@Tags			Config Center
//	@ID				set_config_entry
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			namespace	path		string						true	"Config namespace"
//	@Param			key			path		string						true	"Config key"
//	@Param			request		body		SetReq						true	"Config value and audit reason"
//	@Success		204			{object}	dto.GenericResponse[any]	"Config entry written successfully"
//	@Failure		400			{object}	dto.GenericResponse[any]	"Invalid request"
//	@Failure		401			{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		403			{object}	dto.GenericResponse[any]	"Forbidden config key"
//	@Failure		500			{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/config/{namespace}/{key} [put]
//	@x-api-type		{"portal":"true","admin":"true"}
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

// Delete removes a config entry from the dynamic layer
//
//	@Summary		Delete config entry
//	@Description	Remove a config entry from the dynamic layer; requires system admin
//	@Tags			Config Center
//	@ID				delete_config_entry
//	@Produce		json
//	@Security		BearerAuth
//	@Param			namespace	path		string						true	"Config namespace"
//	@Param			key			path		string						true	"Config key"
//	@Success		204			{object}	dto.GenericResponse[any]	"Config entry deleted successfully"
//	@Failure		401			{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500			{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/config/{namespace}/{key} [delete]
//	@x-api-type		{"portal":"true","admin":"true"}
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
// Watch streams namespace change events as SSE
//
//	@Summary		Watch config namespace
//	@Description	Server-Sent Events stream of every change under the given namespace; emits `change` and `ping` events
//	@Tags			Config Center
//	@ID				watch_config_namespace
//	@Produce		text/event-stream
//	@Security		BearerAuth
//	@Param			namespace	path		string						true	"Config namespace"
//	@Success		200			{string}	string						"SSE stream of config changes"
//	@Failure		401			{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		500			{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/config/{namespace}/watch [get]
//	@x-api-type		{"portal":"true","admin":"true"}
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
// History returns the audit history for a config entry
//
//	@Summary		Get config entry history
//	@Description	Return the audit-log history for a single config key; currently returns 501 until the audit query lands
//	@Tags			Config Center
//	@ID				get_config_entry_history
//	@Produce		json
//	@Security		BearerAuth
//	@Param			namespace	path		string						true	"Config namespace"
//	@Param			key			path		string						true	"Config key"
//	@Success		200			{object}	dto.GenericResponse[any]	"Config history retrieved successfully"
//	@Failure		401			{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		501			{object}	dto.GenericResponse[any]	"Config history not implemented"
//	@Router			/api/v2/config/{namespace}/{key}/history [get]
//	@x-api-type		{"portal":"true","admin":"true"}
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
