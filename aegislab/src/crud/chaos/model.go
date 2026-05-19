package chaos

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// JSONMap is a generic JSON column persisted as a MySQL JSON field.
type JSONMap map[string]any

func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

func (m *JSONMap) Scan(v any) error {
	if v == nil {
		*m = nil
		return nil
	}
	b, ok := v.([]byte)
	if !ok {
		if s, ok := v.(string); ok {
			b = []byte(s)
		} else {
			return errors.New("chaos.JSONMap: unsupported scan type")
		}
	}
	if len(b) == 0 {
		*m = nil
		return nil
	}
	return json.Unmarshal(b, m)
}

type System struct {
	Name                    string    `gorm:"primaryKey;size:64"                                       json:"name"`
	NsPattern               string    `gorm:"size:128;not null"                                        json:"ns_pattern"`
	AppLabelKey             string    `gorm:"size:64;not null"                                         json:"app_label_key"`
	Enabled                 bool      `gorm:"not null;default:true"                                    json:"enabled"`
	MaxConcurrentInjections int       `gorm:"not null;default:5"                                       json:"max_concurrent_injections"`
	CreatedAt               time.Time `gorm:"autoCreateTime"                                           json:"created_at"`
	UpdatedAt               time.Time `gorm:"autoUpdateTime"                                           json:"updated_at"`
}

func (System) TableName() string { return "chaos_systems" }

type Service struct {
	ID            int64     `gorm:"primaryKey;autoIncrement"                                               json:"id"`
	SystemName    string    `gorm:"size:64;not null;uniqueIndex:uq_service_identity,priority:1"            json:"system_name"`
	Name          string    `gorm:"size:128;not null;uniqueIndex:uq_service_identity,priority:2"           json:"name"`
	Instance      string    `gorm:"size:128;not null;default:'default';uniqueIndex:uq_service_identity,priority:3" json:"instance"`
	ChartVersion  string    `gorm:"size:128;not null;uniqueIndex:uq_service_identity,priority:4"           json:"chart_version"`
	Status        string    `gorm:"size:16;not null;default:'active';index:idx_service_status,priority:2"  json:"status"`
	Metadata      JSONMap   `gorm:"type:json"                                                              json:"metadata,omitempty"`
	DiscoveredAt  time.Time `gorm:"not null"                                                               json:"discovered_at"`
	LastSeenAt    time.Time `gorm:"not null"                                                               json:"last_seen_at"`
}

func (Service) TableName() string { return "chaos_services" }

// ImportLock serialises :import across concurrent helm hooks per
// (system, service, instance). ADR-0011.
type ImportLock struct {
	SystemName  string     `gorm:"size:64;primaryKey"               json:"system_name"`
	ServiceName string     `gorm:"size:128;primaryKey"              json:"service_name"`
	Instance    string     `gorm:"size:128;primaryKey"              json:"instance"`
	LockedBy    string     `gorm:"size:64"                          json:"locked_by,omitempty"`
	LockedAt    *time.Time `                                        json:"locked_at,omitempty"`
}

func (ImportLock) TableName() string { return "chaos_import_locks" }

type Capability struct {
	Name               string    `gorm:"primaryKey;size:64"                       json:"name"`
	TargetSchema       JSONMap   `gorm:"type:json;not null"                       json:"target_schema"`
	ParamSchema        JSONMap   `gorm:"type:json;not null"                       json:"param_schema"`
	ObservableContract JSONMap   `gorm:"type:json;not null"                       json:"observable_contract"`
	Status             string    `gorm:"size:16;not null;default:'experimental'"  json:"status"`
	CreatedAt          time.Time `gorm:"autoCreateTime"                           json:"created_at"`
}

func (Capability) TableName() string { return "chaos_capabilities" }

type Point struct {
	ID             string    `gorm:"primaryKey;size:16"                                                json:"id"`
	SystemName     string    `gorm:"size:64;not null;index:idx_point_sys_status,priority:1"            json:"system_name"`
	ServiceID      *int64    `gorm:"index:idx_point_svc_status,priority:1"                             json:"service_id,omitempty"`
	CapabilityName string    `gorm:"size:64;not null"                                                  json:"capability_name"`
	Target         JSONMap   `gorm:"type:json;not null"                                                json:"target"`
	ParamOverrides JSONMap   `gorm:"type:json"                                                         json:"param_overrides,omitempty"`
	Source         string    `gorm:"size:32;not null"                                                  json:"source"`
	Status         string    `gorm:"size:16;not null;default:'active';index:idx_point_sys_status,priority:2;index:idx_point_svc_status,priority:2" json:"status"`
	CreatedAt      time.Time `gorm:"autoCreateTime"                                                    json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"                                                    json:"updated_at"`
}

func (Point) TableName() string { return "chaos_points" }

type ExecutorRecord struct {
	Name            string     `gorm:"primaryKey;size:64"  json:"name"`
	Version         string     `gorm:"size:32"             json:"version,omitempty"`
	Endpoint        string     `gorm:"size:256;not null"   json:"endpoint"`
	SupportedCaps   JSONMap    `gorm:"type:json;not null"  json:"supported_caps"`
	Health          string     `gorm:"size:16"             json:"health,omitempty"`
	LastHeartbeatAt *time.Time `                            json:"last_heartbeat_at,omitempty"`
}

func (ExecutorRecord) TableName() string { return "chaos_executors" }

type InjectionBatch struct {
	ID                  string     `gorm:"primaryKey;size:26"                                       json:"id"`
	IdempotencyKey      string     `gorm:"size:64;not null;uniqueIndex"                             json:"idempotency_key"`
	AggregatedStatus    string     `gorm:"size:16;not null;default:'pending';index:idx_batch_agg_ts,priority:1" json:"aggregated_status"`
	BatchCallerMetadata JSONMap    `gorm:"type:json"                                                json:"batch_caller_metadata,omitempty"`
	Ts                  time.Time  `gorm:"not null;index:idx_batch_agg_ts,priority:2"               json:"ts"`
	StartedAt           *time.Time `                                                                 json:"started_at,omitempty"`
	FinishedAt          *time.Time `                                                                 json:"finished_at,omitempty"`
}

func (InjectionBatch) TableName() string { return "chaos_injection_batches" }

type Injection struct {
	ID              string     `gorm:"primaryKey;size:26"                                       json:"id"`
	BatchID         *string    `gorm:"size:26;index:idx_inj_batch_status,priority:1"            json:"batch_id,omitempty"`
	PointID         string     `gorm:"size:16;not null;index:idx_inj_point"                     json:"point_id"`
	Params          JSONMap    `gorm:"type:json;not null"                                       json:"params"`
	IdempotencyKey  string     `gorm:"size:64;not null;uniqueIndex"                             json:"idempotency_key"`
	ExecutorName    string     `gorm:"size:64;not null"                                         json:"executor_name"`
	ExecutorHandle  string     `gorm:"type:text;not null"                                       json:"executor_handle"`
	Status          string     `gorm:"size:16;not null;index:idx_inj_status_ts,priority:1;index:idx_inj_batch_status,priority:2" json:"status"`
	Groundtruth     JSONMap    `gorm:"type:json"                                                json:"groundtruth,omitempty"`
	Diagnostics     JSONMap    `gorm:"type:json"                                                json:"diagnostics,omitempty"`
	CallerMetadata  JSONMap    `gorm:"type:json"                                                json:"caller_metadata,omitempty"`
	DestroyedAt     *time.Time `                                                                 json:"destroyed_at,omitempty"`
	DestroyError    string     `gorm:"type:text"                                                json:"destroy_error,omitempty"`
	Ts              time.Time  `gorm:"not null;index:idx_inj_status_ts,priority:2"              json:"ts"`
	StartedAt       *time.Time `                                                                 json:"started_at,omitempty"`
	FinishedAt      *time.Time `                                                                 json:"finished_at,omitempty"`
	WebhookAttemptedAt *time.Time `                                                              json:"webhook_attempted_at,omitempty"`
	WebhookError       string     `gorm:"type:text"                                              json:"webhook_error,omitempty"`
}

func (Injection) TableName() string { return "chaos_injections" }

const (
	StatusPending    = "pending"
	StatusRunning    = "running"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"

	AggPending   = "pending"
	AggRunning   = "running"
	AggSucceeded = "succeeded"
	AggPartial   = "partial"
	AggFailed    = "failed"
	AggCancelled = "cancelled"

	PointActive     = "active"
	PointSuperseded = "superseded"
	PointDeprecated = "deprecated"

	ServiceActive  = "active"
	ServiceRetired = "retired"

	CapStable       = "stable"
	CapExperimental = "experimental"
	CapDeprecated   = "deprecated"
)
