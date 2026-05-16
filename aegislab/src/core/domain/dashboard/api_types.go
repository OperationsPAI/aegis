package dashboard

import (
	"time"

	execution "aegis/core/domain/execution"
	injection "aegis/core/domain/injection"
	project "aegis/crud/iam/project"
	trace "aegis/crud/observability/trace"
)

// DashboardCounts is the KPI-tile payload for a single project. All counts
// are point-in-time totals; "tasks_running" is the count of tasks currently
// in the Running state.
type DashboardCounts struct {
	InjectionsTotal int64 `json:"injections_total"`
	ExecutionsTotal int64 `json:"executions_total"`
	TasksRunning    int64 `json:"tasks_running"`
	TracesTotal     int64 `json:"traces_total"`
}

// DashboardResp is the fan-in payload returned by
// GET /api/v2/projects/{project_id}/dashboard. It composes existing
// per-resource DTOs rather than inventing new shapes.
type DashboardResp struct {
	Project          project.ProjectDetailResp `json:"project"`
	Counts           DashboardCounts           `json:"counts"`
	RecentInjections []injection.InjectionResp `json:"recent_injections"`
	RecentExecutions []execution.ExecutionResp `json:"recent_executions"`
	RecentTraces     []trace.TraceResp         `json:"recent_traces"`
	UpdatedAt        time.Time                 `json:"updated_at"`
}
