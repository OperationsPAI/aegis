package cmd

import (
	"encoding/json"

	"aegis/cmd/aegisctl/client"
)

// injectWaitEvent is a normalized view of a TraceStreamEvent used by the
// regression runner (see regression.go). SSEEvent carries the raw SSE frame
// name ("update" or "end"); "update" frames also populate EventName + Payload.
type injectWaitEvent struct {
	// SSEEvent is the raw SSE event type ("update" or "end").
	SSEEvent string
	// EventName is the trace-stream `event_name` (e.g. "fault.injection.started").
	// Only populated for "update" frames.
	EventName string
	// Payload is the decoded payload. The shape depends on EventName:
	//   - fault.injection.started → string (CRD name)
	//   - datapack.build.succeed  → string
	//   - datapack.result.collection → map (with "datapack" / "job_name" keys)
	Payload any
}

// parseTraceSSEEvent converts a raw client.SSEEvent into an injectWaitEvent.
func parseTraceSSEEvent(e client.SSEEvent) injectWaitEvent {
	out := injectWaitEvent{SSEEvent: e.Event}
	if e.Event == "end" {
		return out
	}
	// "update" events carry a JSON-encoded TraceStreamEvent.
	var data struct {
		EventName string          `json:"event_name"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(e.Data), &data); err != nil {
		return out
	}
	out.EventName = data.EventName
	if len(data.Payload) > 0 {
		// Try string first, then generic.
		var s string
		if err := json.Unmarshal(data.Payload, &s); err == nil {
			out.Payload = s
			return out
		}
		var generic any
		if err := json.Unmarshal(data.Payload, &generic); err == nil {
			out.Payload = generic
		}
	}
	return out
}
