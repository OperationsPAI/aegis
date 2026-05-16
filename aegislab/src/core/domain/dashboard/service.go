package dashboard

import (
	"context"
	"fmt"
	"time"

	execution "aegis/core/domain/execution"
	injection "aegis/core/domain/injection"
	task "aegis/core/domain/task"
	project "aegis/crud/iam/project"
	trace "aegis/crud/observability/trace"
	"aegis/platform/consts"
	"aegis/platform/dto"
)

const recentLimit = consts.PageSizeSmall

// Narrow collaborator surfaces — we only need the list endpoints from the
// per-resource services. Defining them locally keeps the dashboard module
// out of the import-cycle danger zone and makes test doubles trivial.

type projectReader interface {
	GetProjectDetail(context.Context, int) (*project.ProjectDetailResp, error)
}

type injectionLister interface {
	ListProjectInjections(context.Context, *injection.ListInjectionReq, int) (*dto.ListResp[injection.InjectionResp], error)
}

type executionLister interface {
	ListProjectExecutions(context.Context, *execution.ListExecutionReq, int) (*dto.ListResp[execution.ExecutionResp], error)
}

type traceLister interface {
	ListTraces(context.Context, *trace.ListTraceReq) (*dto.ListResp[trace.TraceResp], error)
}

type taskLister interface {
	List(context.Context, *task.ListTaskReq) (*dto.ListResp[dto.TaskResp], error)
}

type Service struct {
	projects   projectReader
	injections injectionLister
	executions executionLister
	traces     traceLister
	tasks      taskLister
}

func NewService(
	projects project.HandlerService,
	injections injection.HandlerService,
	executions execution.HandlerService,
	traces trace.HandlerService,
	tasks task.HandlerService,
) *Service {
	return &Service{
		projects:   projects,
		injections: injections,
		executions: executions,
		traces:     traces,
		tasks:      tasks,
	}
}

func (s *Service) GetProjectDashboard(ctx context.Context, projectID int) (*DashboardResp, error) {
	proj, err := s.projects.GetProjectDetail(ctx, projectID)
	if err != nil {
		return nil, err
	}

	injectionPage, err := s.injections.ListProjectInjections(ctx, newPaginatedInjectionReq(), projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list project injections: %w", err)
	}

	executionPage, err := s.executions.ListProjectExecutions(ctx, newPaginatedExecutionReq(), projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list project executions: %w", err)
	}

	tracePage, err := s.traces.ListTraces(ctx, newPaginatedTraceReq(projectID))
	if err != nil {
		return nil, fmt.Errorf("failed to list project traces: %w", err)
	}

	tasksRunningTotal, err := s.countRunningTasks(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to count running tasks: %w", err)
	}

	return &DashboardResp{
		Project: *proj,
		Counts: DashboardCounts{
			InjectionsTotal: paginationTotal(injectionPage),
			ExecutionsTotal: paginationTotal(executionPage),
			TasksRunning:    tasksRunningTotal,
			TracesTotal:     paginationTotal(tracePage),
		},
		RecentInjections: itemsOrEmpty(injectionPage),
		RecentExecutions: itemsOrEmpty(executionPage),
		RecentTraces:     itemsOrEmpty(tracePage),
		UpdatedAt:        time.Now().UTC(),
	}, nil
}

func (s *Service) countRunningTasks(ctx context.Context, projectID int) (int64, error) {
	running := consts.TaskRunning
	req := &task.ListTaskReq{
		PaginationReq: dto.PaginationReq{Page: 1, Size: consts.PageSizeTiny},
		ProjectID:     projectID,
		State:         consts.GetTaskStateName(running),
	}
	if err := req.Validate(); err != nil {
		return 0, err
	}
	page, err := s.tasks.List(ctx, req)
	if err != nil {
		return 0, err
	}
	return paginationTotal(page), nil
}

func newPaginatedInjectionReq() *injection.ListInjectionReq {
	req := &injection.ListInjectionReq{
		PaginationReq: dto.PaginationReq{Page: 1, Size: recentLimit},
	}
	_ = req.Validate()
	return req
}

func newPaginatedExecutionReq() *execution.ListExecutionReq {
	req := &execution.ListExecutionReq{
		PaginationReq: dto.PaginationReq{Page: 1, Size: recentLimit},
	}
	_ = req.Validate()
	return req
}

func newPaginatedTraceReq(projectID int) *trace.ListTraceReq {
	req := &trace.ListTraceReq{
		PaginationReq: dto.PaginationReq{Page: 1, Size: recentLimit},
		ProjectID:     projectID,
	}
	_ = req.Validate()
	return req
}

func paginationTotal[T any](page *dto.ListResp[T]) int64 {
	if page == nil || page.Pagination == nil {
		return 0
	}
	return page.Pagination.Total
}

func itemsOrEmpty[T any](page *dto.ListResp[T]) []T {
	if page == nil || page.Items == nil {
		return []T{}
	}
	return page.Items
}
