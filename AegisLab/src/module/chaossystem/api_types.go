package chaossystem

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
}

// UpdateChaosSystemReq represents the request to update a chaos system.
type UpdateChaosSystemReq struct {
	DisplayName    *string `json:"display_name"`
	NsPattern      *string `json:"ns_pattern"`
	ExtractPattern *string `json:"extract_pattern"`
	AppLabelKey    *string `json:"app_label_key"`
	Count          *int    `json:"count" binding:"omitempty,min=1"`
	Description    *string `json:"description"`
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

// NewChaosSystemResp creates a ChaosSystemResp from a system model.
func NewChaosSystemResp(s *model.System) *ChaosSystemResp {
	return &ChaosSystemResp{
		ID:             s.ID,
		Name:           s.Name,
		DisplayName:    s.DisplayName,
		NsPattern:      s.NsPattern,
		ExtractPattern: s.ExtractPattern,
		AppLabelKey:    s.AppLabelKey,
		Count:          s.Count,
		Description:    s.Description,
		IsBuiltin:      s.IsBuiltin,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
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
