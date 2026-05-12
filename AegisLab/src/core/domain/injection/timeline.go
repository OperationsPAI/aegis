package injection

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/httpx"
	"aegis/platform/model"

	"github.com/gin-gonic/gin"
)

// InjectionTimelineWindow describes a single phase of the experiment
// timeline (pre/fault/recover/post).
type InjectionTimelineWindow struct {
	Start *time.Time `json:"start,omitempty"`
	End   *time.Time `json:"end,omitempty"`
}

// InjectionTimelineEvent describes one task lifecycle transition observed
// for the injection (fault.start/end, build.start/end, algo.start/end).
type InjectionTimelineEvent struct {
	TS     time.Time `json:"ts"`
	Kind   string    `json:"kind"`
	TaskID string    `json:"task_id,omitempty"`
	Label  string    `json:"label,omitempty"`
}

// InjectionTimelineResp is the response for the timeline endpoint.
type InjectionTimelineResp struct {
	Pre     InjectionTimelineWindow  `json:"pre"`
	Fault   InjectionTimelineWindow  `json:"fault"`
	Recover InjectionTimelineWindow  `json:"recover"`
	Post    InjectionTimelineWindow  `json:"post"`
	Events  []InjectionTimelineEvent `json:"events"`
}

// GetTimeline composes the four-window experiment timeline + per-task
// lifecycle events for an injection. Pre/fault/recover/post derive from
// the FaultInjection row's own timestamps; events come from the parent
// FaultInjection task and its descendants (BuildDatapack, RunAlgorithm).
func (s *Service) GetTimeline(_ context.Context, id int) (*InjectionTimelineResp, error) {
	injection, err := s.repo.loadInjection(id)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return nil, fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get injection: %w", err)
	}

	resp := &InjectionTimelineResp{Events: []InjectionTimelineEvent{}}
	if injection.StartTime != nil {
		preStart := injection.StartTime.Add(-time.Duration(injection.PreDuration) * time.Minute)
		resp.Pre = InjectionTimelineWindow{Start: tptr(preStart), End: injection.StartTime}
	}
	resp.Fault = InjectionTimelineWindow{Start: injection.StartTime, End: injection.EndTime}
	if injection.EndTime != nil {
		recoverEnd := injection.EndTime.Add(time.Duration(consts.FixedAbnormalWindowMinutes) * time.Minute)
		resp.Recover = InjectionTimelineWindow{Start: injection.EndTime, End: tptr(recoverEnd)}
		resp.Post = InjectionTimelineWindow{Start: tptr(recoverEnd), End: nil}
	}

	if injection.TaskID == nil {
		return resp, nil
	}

	tasks, err := s.repo.listTimelineTasks(*injection.TaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to list timeline tasks: %w", err)
	}

	events := make([]InjectionTimelineEvent, 0, len(tasks)*2)
	for i := range tasks {
		t := &tasks[i]
		kind, ok := timelineEventKind(t.Type)
		if !ok {
			continue
		}
		events = append(events, InjectionTimelineEvent{
			TS:     t.CreatedAt,
			Kind:   kind + ".start",
			TaskID: t.ID,
			Label:  consts.GetTaskTypeName(t.Type),
		})
		if isTaskTerminal(t.State) {
			events = append(events, InjectionTimelineEvent{
				TS:     t.UpdatedAt,
				Kind:   kind + ".end",
				TaskID: t.ID,
				Label:  consts.GetTaskTypeName(t.Type),
			})
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS.Before(events[j].TS) })
	resp.Events = events
	return resp, nil
}

// GetInjectionTimeline returns the four-window experiment timeline plus
// per-task lifecycle events for an injection.
//
//	@Summary		Injection timeline
//	@Description	Return pre/fault/recover/post windows and task lifecycle events for an injection
//	@Tags			Injections
//	@ID				get_injection_timeline
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int																true	"Injection ID"
//	@Success		200	{object}	dto.GenericResponse[InjectionTimelineResp]	"Timeline returned"
//	@Failure		400	{object}	dto.GenericResponse[any]										"Invalid request"
//	@Failure		401	{object}	dto.GenericResponse[any]										"Authentication required"
//	@Failure		403	{object}	dto.GenericResponse[any]										"Permission denied"
//	@Failure		404	{object}	dto.GenericResponse[any]										"Injection not found"
//	@Failure		500	{object}	dto.GenericResponse[any]										"Internal server error"
//	@Router			/api/v2/injections/{id}/timeline [get]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) GetInjectionTimeline(c *gin.Context) {
	id, ok := parsePositiveID(c, consts.URLPathID, "injection ID")
	if !ok {
		return
	}
	resp, err := h.service.GetTimeline(c.Request.Context(), id)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

func tptr(t time.Time) *time.Time {
	return &t
}

func timelineEventKind(t consts.TaskType) (string, bool) {
	switch t {
	case consts.TaskTypeFaultInjection:
		return "fault", true
	case consts.TaskTypeBuildDatapack:
		return "build", true
	case consts.TaskTypeRunAlgorithm:
		return "algo", true
	default:
		return "", false
	}
}

func isTaskTerminal(state consts.TaskState) bool {
	return state == consts.TaskCompleted || state == consts.TaskError || state == consts.TaskCancelled
}

// listTimelineTasks fetches the FaultInjection task plus all of its
// descendant BuildDatapack/RunAlgorithm tasks for the timeline view.
func (r *Repository) listTimelineTasks(rootTaskID string) ([]model.Task, error) {
	var tasks []model.Task
	if err := r.db.
		Where("(id = ? OR parent_task_id = ?) AND status != ?", rootTaskID, rootTaskID, consts.CommonDeleted).
		Find(&tasks).Error; err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return tasks, nil
	}
	parentIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t.ID != rootTaskID {
			parentIDs = append(parentIDs, t.ID)
		}
	}
	if len(parentIDs) == 0 {
		return tasks, nil
	}
	var grandchildren []model.Task
	if err := r.db.
		Where("parent_task_id IN ? AND status != ?", parentIDs, consts.CommonDeleted).
		Find(&grandchildren).Error; err != nil {
		return nil, err
	}
	return append(tasks, grandchildren...), nil
}
