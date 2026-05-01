package dto

import (
	"time"

	"aegis/model"
)

// InjectionItem is the shared runtime datapack payload carried across tasks/consumers.
type InjectionItem struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	PreDuration int       `json:"pre_duration"`
	StartTime   time.Time `json:"start_time,omitempty"`
	EndTime     time.Time `json:"end_time,omitempty"`
	// Pedestal is the benchmark system code (e.g. "ts", "hs", "otel-demo") that
	// produced this datapack. Empty when the FaultInjection has no pedestal
	// association (manual uploads) or when the pedestal relation was not preloaded.
	Pedestal string `json:"pedestal,omitempty"`
}

func NewInjectionItem(injection *model.FaultInjection) InjectionItem {
	item := InjectionItem{
		ID:          injection.ID,
		Name:        injection.Name,
		PreDuration: injection.PreDuration,
	}

	if injection.StartTime != nil {
		item.StartTime = *injection.StartTime
	}
	if injection.EndTime != nil {
		item.EndTime = *injection.EndTime
	}
	if injection.Pedestal != nil && injection.Pedestal.Container != nil {
		item.Pedestal = injection.Pedestal.Container.Name
	}

	return item
}
