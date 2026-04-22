package chaossystem

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"aegis/config"
	"aegis/dto"
	"aegis/model"
)

// CreateChaosSystemReq represents the request to create a new chaos system.
type CreateChaosSystemReq struct {
	Name           string `json:"name" binding:"required"`
	DisplayName    string `json:"display_name" binding:"required"`
	NsPattern      string `json:"ns_pattern" binding:"required"`
	ExtractPattern string `json:"extract_pattern" binding:"required"`
	AppLabelKey    string `json:"app_label_key"`
	Count          int    `json:"count" binding:"required,min=1"`
	Description    string `json:"description"`
	IsBuiltin      bool   `json:"is_builtin"`
}

// UpdateChaosSystemReq represents the request to update a chaos system.
//
// Status is a pointer so "unset" is distinguishable from the zero value
// (disabled). Only 0/1 are accepted via this endpoint; -1 (CommonDeleted) is
// reserved for DeleteSystem, which owns the tombstone transition.
type UpdateChaosSystemReq struct {
	DisplayName    *string `json:"display_name"`
	NsPattern      *string `json:"ns_pattern"`
	ExtractPattern *string `json:"extract_pattern"`
	AppLabelKey    *string `json:"app_label_key"`
	Count          *int    `json:"count" binding:"omitempty,min=1"`
	Description    *string `json:"description"`
	Status         *int    `json:"status,omitempty" binding:"omitempty,oneof=0 1"`
}

// ChaosSystemResp represents a chaos system in API responses.
type ChaosSystemResp struct {
	ID             int       `json:"id"`
	Name           string    `json:"name"`
	DisplayName    string    `json:"display_name"`
	NsPattern      string    `json:"ns_pattern"`
	ExtractPattern string    `json:"extract_pattern"`
	AppLabelKey    string    `json:"app_label_key"`
	Count          int       `json:"count"`
	Description    string    `json:"description"`
	IsBuiltin      bool      `json:"is_builtin"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ListChaosSystemReq represents the request to list chaos systems.
type ListChaosSystemReq struct {
	dto.PaginationReq
}

// NewChaosSystemResp builds the HTTP response payload from the Viper-backed
// aggregate view. ID / CreatedAt / UpdatedAt come from the anchor
// `injection.system.<name>.count` DynamicConfig row, which is guaranteed to
// exist for any system that has ever been seeded (count is the first field
// created for each system).
func NewChaosSystemResp(view *systemView) *ChaosSystemResp {
	if view == nil {
		return nil
	}
	return &ChaosSystemResp{
		ID:             view.ID,
		Name:           view.Cfg.System,
		DisplayName:    view.Cfg.DisplayName,
		NsPattern:      view.Cfg.NsPattern,
		ExtractPattern: view.Cfg.ExtractPattern,
		AppLabelKey:    view.Cfg.AppLabelKey,
		Count:          view.Cfg.Count,
		Description:    view.Description,
		IsBuiltin:      view.Cfg.IsBuiltin,
		CreatedAt:      view.CreatedAt,
		UpdatedAt:      view.UpdatedAt,
	}
}

// systemView is the server-side aggregate returned to the HTTP layer. It is a
// composite of the Viper-backed ChaosSystemConfig and the DynamicConfig row
// metadata (id/timestamps/description) so response timestamps and IDs remain
// stable across restarts.
type systemView struct {
	ID          int
	Cfg         config.ChaosSystemConfig
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// newSystemView assembles a view from the anchor DynamicConfig row and the
// in-memory ChaosSystemConfig snapshot.
func newSystemView(anchor *model.DynamicConfig, cfg config.ChaosSystemConfig) *systemView {
	return &systemView{
		ID:          anchor.ID,
		Cfg:         cfg,
		Description: anchor.Description,
		CreatedAt:   anchor.CreatedAt,
		UpdatedAt:   anchor.UpdatedAt,
	}
}

// UpsertSystemMetadataReq represents a single metadata upsert request.
type UpsertSystemMetadataReq struct {
	MetadataType string          `json:"metadata_type" binding:"required"`
	ServiceName  string          `json:"service_name" binding:"required"`
	Data         json.RawMessage `json:"data" binding:"required"`
}

// TopologyServiceReq is the high-level service topology payload used when a
// benchmark system is onboarded over HTTP without pre-baked code metadata.
type TopologyServiceReq struct {
	Name      string   `json:"name" binding:"required"`
	Namespace string   `json:"namespace,omitempty"`
	Pods      []string `json:"pods,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// BulkUpsertSystemMetadataReq represents a bulk metadata upsert request.
type BulkUpsertSystemMetadataReq struct {
	Items    []UpsertSystemMetadataReq `json:"items,omitempty"`
	Services []TopologyServiceReq      `json:"services,omitempty"`
}

func (req *BulkUpsertSystemMetadataReq) Validate() error {
	if len(req.Items) == 0 && len(req.Services) == 0 {
		return fmt.Errorf("either items or services must be provided")
	}
	for i, item := range req.Items {
		if strings.TrimSpace(item.MetadataType) == "" {
			return fmt.Errorf("items[%d].metadata_type is required", i)
		}
		if strings.TrimSpace(item.ServiceName) == "" {
			return fmt.Errorf("items[%d].service_name is required", i)
		}
		if len(item.Data) == 0 {
			return fmt.Errorf("items[%d].data is required", i)
		}
	}
	for i, svc := range req.Services {
		if strings.TrimSpace(svc.Name) == "" {
			return fmt.Errorf("services[%d].name is required", i)
		}
	}
	return nil
}

// ReseedSystemReq drives POST /api/v2/systems/reseed — propagate data.yaml
// bumps (chart version, chart name, new container_version rows, dynamic_config
// default drift) to the running DB + etcd. See issue #105.
//
// Defaults to dry-run (Apply=false) as a safety net so a mis-click never
// writes. `Env` picks prod|staging when `DataPath` is the initial_data root
// (or omitted — the handler falls back to the configured
// initialization.data_path).
type ReseedSystemReq struct {
	Name           string `json:"name,omitempty"`            // optional filter: only reseed the named system
	Env            string `json:"env,omitempty"`             // prod | staging
	DataPath       string `json:"data_path,omitempty"`       // optional override for initialization.data_path
	Apply          bool   `json:"apply,omitempty"`           // false (default) = dry-run; true = actually write
	DryRun         bool   `json:"dry_run,omitempty"`         // informational: client can set true to force dry run even if apply=true
	ResetOverrides bool   `json:"reset_overrides,omitempty"` // true = stomp user etcd overrides that differ from new default
}

// SystemChartResp returns the chart source for a system's active pedestal
// ContainerVersion. Consumers (aegisctl pedestal chart install) use this to
// resolve where to pull the chart tgz from when no --tgz override is given.
type SystemChartResp struct {
	SystemName  string         `json:"system_name"`
	ChartName   string         `json:"chart_name"`
	Version     string         `json:"version"`
	RepoURL     string         `json:"repo_url"`
	RepoName    string         `json:"repo_name"`
	LocalPath   string         `json:"local_path,omitempty"`
	ValueFile   string         `json:"value_file,omitempty"`
	Values      map[string]any `json:"values,omitempty"`
	Checksum    string         `json:"checksum,omitempty"`
	PedestalTag string         `json:"pedestal_tag"`
}

// SystemPrerequisiteResp is one system prerequisite in API responses (issue
// #115). Spec is the raw JSON payload the seed loader stored; its shape is
// dictated by Kind (e.g. kind=helm -> {chart,namespace,version}).
type SystemPrerequisiteResp struct {
	ID         int             `json:"id"`
	SystemName string          `json:"system_name"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Spec       json.RawMessage `json:"spec"`
	Status     string          `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// NewSystemPrerequisiteResp builds the API payload from a DB row.
func NewSystemPrerequisiteResp(m *model.SystemPrerequisite) *SystemPrerequisiteResp {
	raw := json.RawMessage(m.SpecJSON)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	return &SystemPrerequisiteResp{
		ID:         m.ID,
		SystemName: m.SystemName,
		Kind:       m.Kind,
		Name:       m.Name,
		Spec:       raw,
		Status:     m.Status,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

// MarkPrerequisiteReq is the POST body for marking a prerequisite
// reconciled/failed. Status must be one of the
// model.SystemPrerequisiteStatus* constants. Reason is free-form text stored
// nowhere today but reserved for future audit / error capture.
type MarkPrerequisiteReq struct {
	Status string `json:"status" binding:"required,oneof=pending reconciled failed"`
	Reason string `json:"reason,omitempty"`
}

// SystemMetadataResp represents system metadata in API responses.
type SystemMetadataResp struct {
	ID           int             `json:"id"`
	SystemName   string          `json:"system_name"`
	MetadataType string          `json:"metadata_type"`
	ServiceName  string          `json:"service_name"`
	Data         json.RawMessage `json:"data"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// NewSystemMetadataResp creates a SystemMetadataResp from a metadata model.
func NewSystemMetadataResp(m *model.SystemMetadata) *SystemMetadataResp {
	return &SystemMetadataResp{
		ID:           m.ID,
		SystemName:   m.SystemName,
		MetadataType: m.MetadataType,
		ServiceName:  m.ServiceName,
		Data:         json.RawMessage(m.Data),
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}
