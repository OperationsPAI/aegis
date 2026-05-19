package chaos

import "time"

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
type ChaosCreateInjectionReq struct {
	PointID        string         `json:"point_id"                  example:"step5acart11111"`
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
