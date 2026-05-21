package chaos

import (
	"time"

	guidedcli "aegis/platform/chaos"
)

// ChaosSystemUpsertReq is the request body for PUT /v1beta/systems/{sys}.
type ChaosSystemUpsertReq struct {
	NsPattern               string `json:"ns_pattern"                         example:"otel-demo"`
	AppLabelKey             string `json:"app_label_key"                      example:"app.kubernetes.io/name"`
	Enabled                 *bool  `json:"enabled,omitempty"`
	MaxConcurrentInjections int    `json:"max_concurrent_injections,omitempty" example:"5"`
}

// ChaosSystemResp is the persisted System row returned by upsert / get.
type ChaosSystemResp struct {
	Name                    string    `json:"name"`
	NsPattern               string    `json:"ns_pattern"`
	AppLabelKey             string    `json:"app_label_key"`
	Enabled                 bool      `json:"enabled"`
	MaxConcurrentInjections int       `json:"max_concurrent_injections"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// ChaosCreateInjectionReq is the request body for POST /v1beta/injections.
// Namespace is the concrete kubernetes namespace the CR is applied to —
// always pool-allocated by the caller (e.g. otel-demo0), never the
// system-name carried in the catalog Point's target.namespace.
type ChaosCreateInjectionReq struct {
	PointID        string         `json:"point_id"                  example:"0123456789abcdef"`
	Namespace      string         `json:"namespace"                 example:"otel-demo0"`
	Params         map[string]any `json:"params"`
	IdempotencyKey string         `json:"idempotency_key"           example:"client-1700000000"`
	CallerMetadata map[string]any `json:"caller_metadata,omitempty"`
	ExecutorPin    string         `json:"executor_pin,omitempty"`
}

// ChaosInjectionResp is the persisted Injection row returned by create / get /
// destroy.
type ChaosInjectionResp struct {
	ID                 string         `json:"id"`
	BatchID            *string        `json:"batch_id,omitempty"`
	PointID            string         `json:"point_id"`
	Params             map[string]any `json:"params"`
	IdempotencyKey     string         `json:"idempotency_key"`
	ExecutorName       string         `json:"executor_name"`
	ExecutorHandle     string         `json:"executor_handle"`
	Status             string         `json:"status"`
	Groundtruth        map[string]any `json:"groundtruth,omitempty"`
	Diagnostics        map[string]any `json:"diagnostics,omitempty"`
	CallerMetadata     map[string]any `json:"caller_metadata,omitempty"`
	DestroyedAt        *time.Time     `json:"destroyed_at,omitempty"`
	DestroyError       string         `json:"destroy_error,omitempty"`
	Ts                 time.Time      `json:"ts"`
	StartedAt          *time.Time     `json:"started_at,omitempty"`
	FinishedAt         *time.Time     `json:"finished_at,omitempty"`
	WebhookAttemptedAt *time.Time     `json:"webhook_attempted_at,omitempty"`
	WebhookError       string         `json:"webhook_error,omitempty"`
}

// ChaosCapabilityResp is one Capability catalog entry.
type ChaosCapabilityResp struct {
	Name               string         `json:"name"`
	TargetSchema       map[string]any `json:"target_schema"`
	ParamSchema        map[string]any `json:"param_schema"`
	ObservableContract map[string]any `json:"observable_contract"`
	Status             string         `json:"status"`
	CreatedAt          time.Time      `json:"created_at"`
}

// ChaosImportPointsReq is the manifest envelope POSTed to
// /v1beta/systems/{sys}/points/import.
type ChaosImportPointsReq struct {
	APIVersion string                        `json:"apiVersion" example:"aegis-chaos/v1beta"`
	Kind       string                        `json:"kind"       example:"PointManifest"`
	Metadata   ChaosImportPointsReqMetadata  `json:"metadata"`
	Spec       ChaosImportPointsReqSpec      `json:"spec"`
}

type ChaosImportPointsReqMetadata struct {
	System       string `json:"system"`
	Service      string `json:"service"`
	Instance     string `json:"instance"`
	ChartVersion string `json:"chart_version"`
}

type ChaosImportPointsReqSpec struct {
	ReplaceScope string                       `json:"replace_scope" example:"service"`
	Points       []ChaosImportPointsReqEntry  `json:"points"`
}

type ChaosImportPointsReqEntry struct {
	Capability     string         `json:"capability"`
	Target         map[string]any `json:"target"`
	ParamOverrides map[string]any `json:"param_overrides,omitempty"`
}

// ChaosImportPointsResp summarises the result of an import (or its dry-run).
type ChaosImportPointsResp struct {
	Upserted   int      `json:"upserted"`
	Superseded int      `json:"superseded"`
	DryRun     bool     `json:"dry_run"`
	PointIDs   []string `json:"point_ids"`
}

// ChaosCreateBatchChildReq is one child entry inside a batch submission.
type ChaosCreateBatchChildReq struct {
	PointID        string         `json:"point_id"                  example:"0123456789abcdef"`
	Namespace      string         `json:"namespace"                 example:"otel-demo0"`
	Params         map[string]any `json:"params"`
	IdempotencyKey string         `json:"idempotency_key"           example:"client-1700000000-c0"`
	CallerMetadata map[string]any `json:"caller_metadata,omitempty"`
	ExecutorPin    string         `json:"executor_pin,omitempty"`
}

// ChaosCreateInjectionBatchReq is the request body for
// POST /v1beta/injection-batches. Each child carries its own idempotency_key;
// the batch-level key gates re-submission of the whole envelope.
type ChaosCreateInjectionBatchReq struct {
	BatchIdempotencyKey string                     `json:"batch_idempotency_key" binding:"required" example:"batch-1700000000"`
	BatchCallerMetadata map[string]any             `json:"batch_caller_metadata,omitempty"`
	Children            []ChaosCreateBatchChildReq `json:"children"               binding:"required"`
}

// ChaosInjectionBatchResp is the persisted Batch row returned by create / get /
// destroy, together with all known children at read time.
type ChaosInjectionBatchResp struct {
	ID                  string               `json:"id"`
	IdempotencyKey      string               `json:"idempotency_key"`
	AggregatedStatus    string               `json:"aggregated_status"`
	BatchCallerMetadata map[string]any       `json:"batch_caller_metadata,omitempty"`
	Ts                  time.Time            `json:"ts"`
	StartedAt           *time.Time           `json:"started_at,omitempty"`
	FinishedAt          *time.Time           `json:"finished_at,omitempty"`
	WebhookAttemptedAt  *time.Time           `json:"webhook_attempted_at,omitempty"`
	WebhookError        string               `json:"webhook_error,omitempty"`
	Children            []ChaosInjectionResp `json:"children"`
}

// ChaosPointResp is a row in the /v1beta/systems/{sys}/points listing.
type ChaosPointResp struct {
	ID             string         `json:"id"`
	SystemName     string         `json:"system_name"`
	ServiceID      *int64         `json:"service_id,omitempty"`
	ServiceName    string         `json:"service_name,omitempty"`
	CapabilityName string         `json:"capability_name"`
	Target         map[string]any `json:"target"`
	ParamOverrides map[string]any `json:"param_overrides,omitempty"`
	Source         string         `json:"source"`
	Status         string         `json:"status"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// ChaosListPointsResp is the paged listing returned by GET /v1beta/systems/{sys}/points.
type ChaosListPointsResp struct {
	Points []ChaosPointResp `json:"points"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// ChaosExportPointsResp is the envelope returned by
// GET /v1beta/systems/{sys}/points/export — one PointManifest per
// (service, instance, chart_version) tuple, ready to re-import.
type ChaosExportPointsResp struct {
	Manifests []PointManifest `json:"manifests"`
}

// ChaosGuidedResolveReq is the request body for POST /v1beta/guided/resolve.
// The current GuidedConfig is passed through as-is; the server walks one step
// of the guided state machine and returns the next required field along with
// the (canonicalised) config it derived.
type ChaosGuidedResolveReq struct {
	Config guidedcli.GuidedConfig `json:"config"`
}

// ChaosGuidedResolveResp wraps the guidedcli response in the same JSON shape
// the in-process callers used. Fields mirror guidedcli.GuidedResponse so the
// CLI replacement is a transparent swap.
type ChaosGuidedResolveResp struct {
	Mode         string                 `json:"mode"`
	Stage        string                 `json:"stage"`
	Config       guidedcli.GuidedConfig `json:"config"`
	Resolved     map[string]any         `json:"resolved,omitempty"`
	Next         []guidedcli.FieldSpec  `json:"next,omitempty"`
	Preview      *guidedcli.Preview     `json:"preview,omitempty"`
	ApplyPayload map[string]any         `json:"apply_payload,omitempty"`
	Result       map[string]any         `json:"result,omitempty"`
	CanApply     bool                   `json:"can_apply"`
	Warnings     []string               `json:"warnings,omitempty"`
	Errors       []string               `json:"errors,omitempty"`
	Resources    map[string]any         `json:"resources,omitempty"`
	Meta         map[string]any         `json:"meta,omitempty"`
}

// ChaosGuidedApplyNextReq is the request body for POST /v1beta/guided/apply-next.
// `value` is the raw user selection for the single next-step field that the
// preceding /resolve call surfaced; the server re-derives the resolve response
// from `current`, applies the value, and returns the updated config.
type ChaosGuidedApplyNextReq struct {
	Current guidedcli.GuidedConfig `json:"current"`
	Value   string                 `json:"value" binding:"required"`
}

// ChaosGuidedApplyNextResp is the merged GuidedConfig the caller should pass
// to the next /resolve call.
type ChaosGuidedApplyNextResp struct {
	Config guidedcli.GuidedConfig `json:"config"`
}

// ChaosSystemCandidatesResp enumerates every reachable
// (chaos_type, target...) GuidedConfig for the named system, with one entry
// per leaf in the guided enumeration tree. Numeric param grids (latency_ms,
// loss_pct, ...) are NOT expanded.
type ChaosSystemCandidatesResp struct {
	System     string                   `json:"system"`
	Namespace  string                   `json:"namespace"`
	Candidates []guidedcli.GuidedConfig `json:"candidates"`
}

// ChaosTaskInjectionRef is the resolved (injection_id, is_batch) pair returned
// by DELETE /v1beta/injections/by-task/{taskID}. The same shape `Children`-less
// reply that the singleton/batch DELETE endpoints emit is returned via the
// payload pointers — exactly one of `injection` or `batch` is populated.
type ChaosTaskInjectionRef struct {
	TaskID    string                   `json:"task_id"`
	IsBatch   bool                     `json:"is_batch"`
	Injection *ChaosInjectionResp      `json:"injection,omitempty"`
	Batch     *ChaosInjectionBatchResp `json:"batch,omitempty"`
}
