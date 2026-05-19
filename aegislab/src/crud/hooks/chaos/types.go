package chaoshooks

import (
	"encoding/json"
	"time"

	"aegis/platform/dto"
)

// CallerMetadata is the opaque-JSON envelope aegis-chaos round-trips
// verbatim from `POST /injections{,/batches}` back to the backend (ADR-0005).
// Backend uses it to rejoin the campaign-side state machine.
//
// Field shape mirrors today's CRD label set + the benchmark/datapack
// annotation payloads `parseAnnotations` reads in
// core/orchestrator/k8s_handler.go — that is the data the campaign step
// originally encoded as K8s labels/annotations when it submitted the
// chaos-mesh CR, and is what the receiver needs to keep BuildDatapack's
// ParentTaskID chain intact.
type CallerMetadata struct {
	TaskID       string `json:"task_id"`
	TaskType     string `json:"task_type"`
	TraceID      string `json:"trace_id"`
	GroupID      string `json:"group_id"`
	ProjectID    int    `json:"project_id"`
	UserID       int    `json:"user_id"`
	ParentTaskID string `json:"parent_task_id,omitempty"`

	Benchmark *dto.ContainerVersionItem `json:"benchmark,omitempty"`
	Datapack  *dto.InjectionItem        `json:"datapack,omitempty"`
}

// SingletonWebhook is the §10.2 singleton payload posted to `/api/v1/hooks/chaos`.
type SingletonWebhook struct {
	InjectionID    string          `json:"injection_id" binding:"required"`
	IdempotencyKey string          `json:"idempotency_key" binding:"required"`
	PointID        string          `json:"point_id"`
	Status         string          `json:"status" binding:"required"` // succeeded | failed | cancelled
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     time.Time       `json:"finished_at"`
	Groundtruth    json.RawMessage `json:"groundtruth"`
	Diagnostics    json.RawMessage `json:"diagnostics"`
	CallerMetadata CallerMetadata  `json:"caller_metadata"`
}

// ChildResult is one child slot inside a batch webhook.
type ChildResult struct {
	InjectionID    string          `json:"injection_id"`
	PointID        string          `json:"point_id"`
	Status         string          `json:"status"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     time.Time       `json:"finished_at"`
	Groundtruth    json.RawMessage `json:"groundtruth"`
	Diagnostics    json.RawMessage `json:"diagnostics_brief"`
	CallerMetadata CallerMetadata  `json:"caller_metadata"`
}

// BatchWebhook is the §10.2 batch payload posted to `/api/v1/hooks/chaos-batch`.
// `AggregatedStatus` is the sticky terminal-state field; ADR-0006 forbids
// re-entry into running/pending.
type BatchWebhook struct {
	BatchID             string          `json:"batch_id" binding:"required"`
	IdempotencyKey      string          `json:"idempotency_key" binding:"required"`
	PrevStatus          string          `json:"prev_status"`
	NewStatus           string          `json:"new_status"`
	AggregatedStatus    string          `json:"aggregated_status" binding:"required"` // succeeded | partial | failed | cancelled
	StartedAt           time.Time       `json:"started_at"`
	FinishedAt          time.Time       `json:"finished_at"`
	ChildResults        []ChildResult   `json:"child_results"`
	BatchCallerMetadata CallerMetadata  `json:"batch_caller_metadata"`
	Extra               json.RawMessage `json:"extra,omitempty"`
}
