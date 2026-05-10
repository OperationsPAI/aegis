package injection

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegis/consts"
	"aegis/dto"
	"aegis/httpx"
	loki "aegis/infra/loki"

	"github.com/gin-gonic/gin"
)

// InjectionLogQueryReq captures the query parameters for the filtered logs
// endpoint. Both `q` (substring) and `level` are applied client-side after
// the Loki range query so they remain consistent regardless of how the
// underlying ingestion pipeline labels entries.
type InjectionLogQueryReq struct {
	Start  string `form:"start" binding:"omitempty"`
	End    string `form:"end" binding:"omitempty"`
	Q      string `form:"q" binding:"omitempty"`
	Level  string `form:"level" binding:"omitempty"`
	Limit  int    `form:"limit" binding:"omitempty"`
	Cursor string `form:"cursor" binding:"omitempty"`
}

func (req *InjectionLogQueryReq) Validate() error {
	if req.Start != "" {
		if _, err := time.Parse(time.RFC3339, req.Start); err != nil {
			return fmt.Errorf("invalid start: %w", err)
		}
	}
	if req.End != "" {
		if _, err := time.Parse(time.RFC3339, req.End); err != nil {
			return fmt.Errorf("invalid end: %w", err)
		}
	}
	if req.Limit < 0 {
		return fmt.Errorf("limit must be non-negative")
	}
	switch strings.ToLower(req.Level) {
	case "", "error", "warn", "info":
	default:
		return fmt.Errorf("invalid level %q: expected error|warn|info", req.Level)
	}
	return nil
}

// InjectionLogEntry is the per-row payload returned by the filtered logs
// endpoint. `attrs` carries any Loki stream labels worth surfacing
// (job_id, trace_id, etc.) and `service` is sourced from the same labels
// when available.
type InjectionLogEntry struct {
	TS      time.Time         `json:"ts"`
	Level   string            `json:"level,omitempty"`
	Service string            `json:"service,omitempty"`
	Msg     string            `json:"msg"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// InjectionLogsFilteredResp is the paginated response for the filtered
// logs endpoint. NextCursor is empty when there are no more entries.
type InjectionLogsFilteredResp struct {
	Entries       []InjectionLogEntry `json:"entries"`
	NextCursor    string              `json:"next_cursor,omitempty"`
	TotalEstimate int                 `json:"total_estimate"`
}

// GetLogsFiltered returns paginated, filterable Loki entries for an
// injection's primary task. Filtering and pagination are applied in-process
// after the Loki range query because the cursor must be stable across
// repeated calls and Loki itself does not expose offset-based cursors.
func (s *Service) GetLogsFiltered(ctx context.Context, id int, req *InjectionLogQueryReq) (*InjectionLogsFilteredResp, error) {
	injection, err := s.repo.loadInjection(id)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return nil, fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get injection: %w", err)
	}

	resp := &InjectionLogsFilteredResp{Entries: []InjectionLogEntry{}}
	if injection.TaskID == nil {
		return resp, nil
	}

	task, taskErr := s.repo.loadTask(*injection.TaskID)
	if taskErr != nil {
		return resp, nil
	}

	start, end, err := resolveLogWindow(req.Start, req.End, task.CreatedAt)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}

	lokiCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rawQ, substr := splitLogQuery(req.Q)
	entries, lokiErr := s.lokiClient.QueryJobLogs(lokiCtx, *injection.TaskID, loki.QueryOpts{
		Start:     start,
		End:       end,
		Direction: "forward",
		Substring: substr,
		RawLogQL:  rawQ,
	})
	if lokiErr != nil {
		return resp, nil
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	level := strings.ToLower(req.Level)
	filtered := make([]InjectionLogEntry, 0, len(entries))
	for _, entry := range entries {
		row := toInjectionLogEntry(entry)
		if level != "" && row.Level != level {
			continue
		}
		filtered = append(filtered, row)
	}

	offset, err := decodeCursor(req.Cursor)
	if err != nil {
		return nil, err
	}
	if offset > len(filtered) {
		offset = len(filtered)
	}
	page := filtered[offset:]
	if len(page) > limit {
		page = page[:limit]
	}
	nextCursor := ""
	if offset+len(page) < len(filtered) {
		nextCursor = encodeCursor(offset + len(page))
	}

	resp.Entries = page
	resp.NextCursor = nextCursor
	resp.TotalEstimate = len(filtered)
	return resp, nil
}

// GetInjectionLogs handles paginated/filterable log retrieval for an injection.
//
//	@Summary		Query injection logs
//	@Description	Return paginated, filterable log entries for the BuildDatapack window of an injection
//	@Tags			Injections
//	@ID				get_injection_logs
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		int																true	"Injection ID"
//	@Param			start	query		string															false	"RFC3339 start time"
//	@Param			end		query		string															false	"RFC3339 end time"
//	@Param			q		query		string															false	"Substring or LogQL pipeline fragment"
//	@Param			level	query		string															false	"Filter by level: error|warn|info"
//	@Param			limit	query		int																false	"Maximum entries per page"	default(200)
//	@Param			cursor	query		string															false	"Pagination cursor"
//	@Success		200		{object}	dto.GenericResponse[InjectionLogsFilteredResp]	"Logs retrieved successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]										"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]										"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]										"Permission denied"
//	@Failure		404		{object}	dto.GenericResponse[any]										"Injection not found"
//	@Failure		500		{object}	dto.GenericResponse[any]										"Internal server error"
//	@Router			/api/v2/injections/{id}/logs [get]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) GetInjectionLogs(c *gin.Context) {
	id, ok := parsePositiveID(c, consts.URLPathID, "injection ID")
	if !ok {
		return
	}
	var req InjectionLogQueryReq
	if err := c.ShouldBindQuery(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Validation failed: "+err.Error())
		return
	}
	resp, err := h.service.GetLogsFiltered(c.Request.Context(), id, &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

func resolveLogWindow(startStr, endStr string, taskCreatedAt time.Time) (time.Time, time.Time, error) {
	var start, end time.Time
	var err error
	if startStr != "" {
		start, err = time.Parse(time.RFC3339, startStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start: %w", err)
		}
	} else {
		start = taskCreatedAt
	}
	if endStr != "" {
		end, err = time.Parse(time.RFC3339, endStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end: %w", err)
		}
	} else {
		end = time.Now()
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("end must be after start")
	}
	return start, end, nil
}

func splitLogQuery(q string) (raw, substr string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", ""
	}
	if strings.HasPrefix(q, "|") {
		return q, ""
	}
	return "", q
}

var levelKeywords = []string{"error", "warn", "info", "debug"}

func toInjectionLogEntry(entry dto.LogEntry) InjectionLogEntry {
	row := InjectionLogEntry{
		TS:    entry.Timestamp,
		Msg:   entry.Line,
		Level: string(entry.Level),
	}
	if row.Level == "" {
		row.Level = detectLevel(entry.Line)
	}
	attrs := map[string]string{}
	if entry.JobID != "" {
		attrs["job_id"] = entry.JobID
	}
	if entry.TraceID != "" {
		attrs["trace_id"] = entry.TraceID
	}
	if entry.TaskID != "" {
		attrs["task_id"] = entry.TaskID
	}
	if len(attrs) > 0 {
		row.Attrs = attrs
	}
	if jobID, ok := attrs["job_id"]; ok {
		row.Service = jobID
	}
	return row
}

func detectLevel(line string) string {
	lower := strings.ToLower(line)
	for _, lvl := range levelKeywords {
		if strings.Contains(lower, lvl) {
			return lvl
		}
	}
	return ""
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor: %w", err)
	}
	offset, err := strconv.Atoi(string(raw))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return offset, nil
}
