package trace

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"
	task "aegis/module/task"
	"aegis/platform/utils"
)

type GetTraceStreamReq struct {
	LastID string `form:"last_id" binding:"omitempty"`
}

func (req *GetTraceStreamReq) Validate() error {
	if req.LastID == "" {
		req.LastID = "0"
	}
	if req.LastID == "0" {
		return nil
	}
	if len(req.LastID) < 3 || req.LastID[0] == '-' || req.LastID[len(req.LastID)-1] == '-' {
		return fmt.Errorf("invalid last_id format: must be '0' or a valid stream ID (e.g., 1678886400000-0)")
	}
	dashCount := 0
	for _, ch := range req.LastID {
		if ch == '-' {
			dashCount++
		}
	}
	if dashCount != 1 {
		return fmt.Errorf("invalid last_id format: must be '0' or a valid stream ID (e.g., 1678886400000-0)")
	}
	return nil
}

type ListTraceFilters struct {
	TraceType *consts.TraceType
	GroupID   string
	ProjectID int
	State     *consts.TraceState
	Status    *consts.StatusType
}

// ListTraceReq represents the request to list traces.
//
// State accepts either the numeric TraceState (e.g. "1") or the canonical
// name (e.g. "Running"). Binding as string keeps gin from failing with
// `strconv.ParseInt: parsing "Running": invalid syntax` — resolution to the
// numeric consts.TraceState is done in ToFilterOptions.
type ListTraceReq struct {
	dto.PaginationReq
	TraceType *consts.TraceType  `form:"trace_type" binding:"omitempty"`
	GroupID   string             `form:"group_id" binding:"omitempty"`
	ProjectID int                `form:"project_id" binding:"omitempty"`
	State     string             `form:"state" binding:"omitempty"`
	Status    *consts.StatusType `form:"status" binding:"omitempty"`
}

func (req *ListTraceReq) Validate() error {
	if err := req.PaginationReq.Validate(); err != nil {
		return err
	}
	if req.TraceType != nil {
		if _, exists := consts.ValidTraceTypes[*req.TraceType]; !exists {
			return fmt.Errorf("invalid trace type: %d", *req.TraceType)
		}
	}
	if err := validateUUID(req.GroupID); err != nil {
		return err
	}
	if req.ProjectID < 0 {
		return fmt.Errorf("invalid project ID: %d", req.ProjectID)
	}
	if _, err := parseTraceStateParam(req.State); err != nil {
		return err
	}
	return validateStatus(req.Status)
}

func (req *ListTraceReq) ToFilterOptions() *ListTraceFilters {
	state, _ := parseTraceStateParam(req.State)
	return &ListTraceFilters{
		TraceType: req.TraceType,
		GroupID:   req.GroupID,
		ProjectID: req.ProjectID,
		State:     state,
		Status:    req.Status,
	}
}

// parseTraceStateParam accepts either a TraceState numeric string ("1") or
// its canonical name ("Running"). Empty input means "no filter" and returns
// nil without error.
func parseTraceStateParam(raw string) (*consts.TraceState, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	if state := consts.GetTraceStateByName(s); state != nil {
		if _, exists := consts.ValidTraceStates[*state]; !exists {
			return nil, fmt.Errorf("invalid trace state: %s", s)
		}
		return state, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("invalid trace state %q: want a name (e.g. Running) or int", s)
	}
	state := consts.TraceState(n)
	if _, exists := consts.ValidTraceStates[state]; !exists {
		return nil, fmt.Errorf("invalid trace state: %d", n)
	}
	return &state, nil
}

type TraceResp struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	LastEvent   string     `json:"last_event"`
	StartTime   time.Time  `json:"start_time"`
	EndTime     *time.Time `json:"end_time,omitempty"`
	GroupID     string     `json:"group_id"`
	ProjectID   int        `json:"project_id,omitempty"`
	ProjectName string     `json:"project_name,omitempty"`
	LeafNum     int        `json:"leaf_num"`
	State       string     `json:"state"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func NewTraceResp(trace *model.Trace) *TraceResp {
	resp := &TraceResp{
		ID:        trace.ID,
		Type:      consts.GetTraceTypeName(trace.Type),
		LastEvent: trace.LastEvent.String(),
		StartTime: trace.StartTime,
		EndTime:   trace.EndTime,
		GroupID:   trace.GroupID,
		ProjectID: trace.ProjectID,
		LeafNum:   trace.LeafNum,
		State:     consts.GetTraceStateName(trace.State),
		Status:    consts.GetStatusTypeName(trace.Status),
		CreatedAt: trace.CreatedAt,
		UpdatedAt: trace.UpdatedAt,
	}
	if trace.Project != nil {
		resp.ProjectName = trace.Project.Name
	}
	return resp
}

// CancelTraceResp describes the outcome of a best-effort trace cancellation.
// All fields are optional so a partial success still renders cleanly on the
// aegisctl side. See cmd/aegisctl/cmd/trace.go `traceCancelResponseData` for
// the consumer shape.
type CancelTraceResp struct {
	TraceID           string   `json:"trace_id,omitempty"`
	State             string   `json:"state,omitempty"`
	Message           string   `json:"message,omitempty"`
	CancelledTasks    []string `json:"cancelled_tasks,omitempty"`
	DeletedPodChaos   []string `json:"deleted_podchaos,omitempty"`
	RemovedRedisTasks []string `json:"removed_redis_tasks,omitempty"`
}

type TraceDetailResp struct {
	TraceResp

	Tasks []task.TaskResp `json:"tasks"`
}

func NewTraceDetailResp(trace *model.Trace) *TraceDetailResp {
	resp := &TraceDetailResp{
		TraceResp: *NewTraceResp(trace),
		Tasks:     make([]task.TaskResp, 0, len(trace.Tasks)),
	}
	for i := range trace.Tasks {
		resp.Tasks = append(resp.Tasks, *task.NewTaskResp(&trace.Tasks[i]))
	}
	return resp
}

func validateUUID(id string) error {
	if id == "" {
		return nil
	}
	if !utils.IsValidUUID(id) {
		return fmt.Errorf("invalid UUID format: %s", id)
	}
	return nil
}

func validateStatus(status *consts.StatusType) error {
	if status != nil {
		if _, exists := consts.ValidStatuses[*status]; !exists {
			return fmt.Errorf("invalid status value: %d", *status)
		}
	}
	return nil
}
