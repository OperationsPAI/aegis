package chaos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
)

// sseStreamPollInterval is the DB poll period for the SSE endpoint. The
// reconciler ticks every 5s, so 1s is well within the steady-state lag we
// already accept.
const sseStreamPollInterval = 1 * time.Second

// sseStreamMaxDuration caps the total wall-clock lifetime of a single SSE
// subscription. A misbehaving client that opens a stream and never reads or
// disconnects would otherwise hold the polling goroutine forever.
const sseStreamMaxDuration = 30 * time.Minute

type injectionEvent struct {
	InjectionID string `json:"injection_id"`
	Status      string `json:"status"`
	ExecState   string `json:"exec_state,omitempty"`
	EmittedAt   string `json:"emitted_at"`
	Attempt     int    `json:"attempt"`
}

// StreamInjectionEvents serves the status-transition stream for one
// injection as text/event-stream. The first event is the current row state
// so a late subscriber doesn't miss the initial transition; subsequent
// events are emitted whenever Status (or the executor-derived exec_state
// hint stamped into Diagnostics) changes. The stream closes after a
// terminal status is observed, after the request context is cancelled, or
// after sseStreamMaxDuration elapses.
//
//	@Summary		Stream chaos injection status events (SSE)
//	@Description	Server-Sent Events stream of status transitions for one injection. Terminates with `event: terminal` on terminal status or `event: timeout` after 30 minutes.
//	@Tags			Chaos
//	@ID				chaos_stream_injection_events
//	@Produce		text/event-stream
//	@Security		BearerAuth
//	@Param			id	path		string										true	"Injection id (ULID)"
//	@Success		200	{string}	string										"SSE stream"
//	@Failure		401	{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		404	{object}	dto.GenericResponse[any]					"Injection not found"
//	@Router			/v1beta/injections/{id}/events [get]
func (h *Handler) StreamInjectionEvents(c *gin.Context) {
	id := c.Param("id")
	inj, err := h.Mgr.GetInjection(c.Request.Context(), id)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrInjectionNotFound) {
			code = http.StatusNotFound
		}
		dto.ErrorResponse(c, code, err.Error())
		return
	}

	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx, cancel := context.WithTimeout(c.Request.Context(), sseStreamMaxDuration)
	defer cancel()

	attempt := 0
	emit := func(row *Injection, terminal bool) bool {
		attempt++
		ev := injectionEvent{
			InjectionID: row.ID,
			Status:      row.Status,
			ExecState:   execStateFromDiagnostics(row.Diagnostics),
			EmittedAt:   time.Now().UTC().Format(time.RFC3339Nano),
			Attempt:     attempt,
		}
		payload, mErr := json.Marshal(ev)
		if mErr != nil {
			return false
		}
		if terminal {
			if _, wErr := fmt.Fprintf(w, "event: terminal\ndata: %s\n\n", payload); wErr != nil {
				return false
			}
		} else {
			if _, wErr := fmt.Fprintf(w, "data: %s\n\n", payload); wErr != nil {
				return false
			}
		}
		w.Flush()
		return true
	}

	lastStatus := inj.Status
	lastExec := execStateFromDiagnostics(inj.Diagnostics)
	terminal := isTerminal(inj.Status)
	if !emit(inj, terminal) {
		return
	}
	if terminal {
		return
	}

	ticker := time.NewTicker(sseStreamPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				_, _ = fmt.Fprint(w, "event: timeout\ndata: {}\n\n")
				w.Flush()
			}
			return
		case <-ticker.C:
			row, err := h.Mgr.GetInjection(ctx, id)
			if err != nil {
				return
			}
			curExec := execStateFromDiagnostics(row.Diagnostics)
			if row.Status == lastStatus && curExec == lastExec {
				continue
			}
			lastStatus = row.Status
			lastExec = curExec
			term := isTerminal(row.Status)
			if !emit(row, term) {
				return
			}
			if term {
				return
			}
		}
	}
}

func execStateFromDiagnostics(diag JSONMap) string {
	if diag == nil {
		return ""
	}
	if v, ok := diag["exec_state"].(string); ok {
		return v
	}
	return ""
}
