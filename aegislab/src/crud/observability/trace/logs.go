package trace

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	chinfra "aegis/platform/clickhouse"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/httpx"
	"aegis/platform/utils"

	"github.com/gin-gonic/gin"
)

// TraceLogQueryReq is the portal-side filter set for the trace-logs endpoint.
// Mirrors injection.InjectionLogQueryReq so the frontend can reuse the same
// query-builder code path; substring + level filters push into ClickHouse
// (see platform/clickhouse/log_reader.buildTraceLogsQuery), pagination is
// applied in-process so the cursor stays stable across calls.
type TraceLogQueryReq struct {
	Start  string `form:"start" binding:"omitempty"`
	End    string `form:"end" binding:"omitempty"`
	Q      string `form:"q" binding:"omitempty"`
	Level  string `form:"level" binding:"omitempty"`
	Limit  int    `form:"limit" binding:"omitempty"`
	Cursor string `form:"cursor" binding:"omitempty"`
}

func (req *TraceLogQueryReq) Validate() error {
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

// TraceLogEntry is the per-row payload for the trace-logs endpoint. Shape is
// byte-identical to InjectionLogEntry so the frontend's Logs panel can share
// the rendering code.
type TraceLogEntry struct {
	TS      time.Time         `json:"ts"`
	Level   string            `json:"level,omitempty"`
	Service string            `json:"service,omitempty"`
	Msg     string            `json:"msg"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// TraceLogsResp is the paginated response for the trace-logs endpoint.
// NextCursor is empty when there are no more entries.
type TraceLogsResp struct {
	Entries       []TraceLogEntry `json:"entries"`
	NextCursor    string          `json:"next_cursor,omitempty"`
	TotalEstimate int             `json:"total_estimate"`
}

// GetTraceLogs returns paginated, filterable ClickHouse log entries for the
// given aegis trace. We pivot directly on trace_id (rather than via an
// injection / primary task) so callers fetching ExecutionDetail logs don't
// need to round-trip through injection metadata. When the trace has no rows
// in the `task` table — e.g. the orchestrator dropped writes mid-flight or
// the trace was issued from a non-orchestrator path — we return an empty
// entries list rather than 404 so the Logs panel renders an empty state.
//
// ClickHouse errors are surfaced (no longer swallowed into an empty
// response): a Loki-not-deployed environment used to silently return an
// empty entries list which made the panel render blank with no indication
// of misconfiguration.
func (s *Service) GetTraceLogs(ctx context.Context, traceID string, req *TraceLogQueryReq) (*TraceLogsResp, error) {
	resp := &TraceLogsResp{Entries: []TraceLogEntry{}}

	tasks, err := s.repo.ListTasksByTraceID(traceID)
	if err != nil {
		return nil, fmt.Errorf("load tasks for trace: %w", err)
	}
	if len(tasks) == 0 {
		return resp, nil
	}

	// Earliest task CreatedAt anchors the time window when the caller did
	// not supply explicit Start. Tasks are not ordered by the repo helper,
	// so scan once to pick the floor.
	earliest := tasks[0].CreatedAt
	for i := 1; i < len(tasks); i++ {
		if tasks[i].CreatedAt.Before(earliest) {
			earliest = tasks[i].CreatedAt
		}
	}

	start, end, err := resolveTraceLogWindow(req.Start, req.End, earliest)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}

	if s.logs == nil {
		return nil, fmt.Errorf("clickhouse log reader not configured")
	}

	chCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	entries, chErr := s.logs.QueryTraceLogs(chCtx, traceID, chinfra.LogQueryOpts{
		Start:     start,
		End:       end,
		Level:     strings.ToLower(strings.TrimSpace(req.Level)),
		Substring: substringFromQuery(req.Q),
	})
	if chErr != nil {
		return nil, fmt.Errorf("clickhouse logs query failed: %w", chErr)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	filtered := make([]TraceLogEntry, 0, len(entries))
	for _, entry := range entries {
		filtered = append(filtered, toTraceLogEntry(entry))
	}

	offset, err := decodeTraceCursor(req.Cursor)
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
		nextCursor = encodeTraceCursor(offset + len(page))
	}

	resp.Entries = page
	resp.NextCursor = nextCursor
	resp.TotalEstimate = len(filtered)
	return resp, nil
}

// GetTraceLogs handles paginated/filterable log retrieval for a trace.
//
//	@Summary		Query trace logs
//	@Description	Return paginated, filterable log entries for every task under an aegis trace. Mirrors /injections/{id}/logs but pivots directly on trace_id so ExecutionDetail callers do not need to round-trip through injection metadata. Returns an empty entries list when the trace has no rows in the task table.
//	@Tags			Traces
//	@ID				get_trace_logs
//	@Produce		json
//	@Security		BearerAuth
//	@Param			trace_id	path		string										true	"Trace ID"
//	@Param			start		query		string										false	"RFC3339 start time"
//	@Param			end			query		string										false	"RFC3339 end time"
//	@Param			q			query		string										false	"Substring filter on log body"
//	@Param			level		query		string										false	"Filter by level: error|warn|info"
//	@Param			limit		query		int											false	"Maximum entries per page"	default(200)
//	@Param			cursor		query		string										false	"Pagination cursor"
//	@Success		200			{object}	dto.GenericResponse[TraceLogsResp]			"Logs retrieved successfully"
//	@Failure		400			{object}	dto.GenericResponse[any]					"Invalid request"
//	@Failure		401			{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		403			{object}	dto.GenericResponse[any]					"Permission denied"
//	@Failure		500			{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/api/v2/traces/{trace_id}/logs [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) GetTraceLogs(c *gin.Context) {
	traceID := c.Param(consts.URLPathTraceID)
	if !utils.IsValidUUID(traceID) {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid trace ID")
		return
	}
	var req TraceLogQueryReq
	if err := c.ShouldBindQuery(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Validation failed: "+err.Error())
		return
	}
	resp, err := h.service.GetTraceLogs(c.Request.Context(), traceID, &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

func resolveTraceLogWindow(startStr, endStr string, fallbackStart time.Time) (time.Time, time.Time, error) {
	var start, end time.Time
	var err error
	if startStr != "" {
		start, err = time.Parse(time.RFC3339, startStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start: %w", err)
		}
	} else {
		start = fallbackStart
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

// substringFromQuery strips the legacy LogQL escape prefix that the
// pre-ClickHouse code used to accept. Anything beginning with `|` is treated
// as no-filter rather than passed through as a literal — matches the
// injection-side behaviour so the two endpoints accept identical inputs.
func substringFromQuery(q string) string {
	q = strings.TrimSpace(q)
	if strings.HasPrefix(q, "|") {
		return ""
	}
	return q
}

var levelKeywords = []string{"error", "warn", "info", "debug"}

func toTraceLogEntry(entry chinfra.LogEntry) TraceLogEntry {
	row := TraceLogEntry{
		TS:    entry.Timestamp,
		Msg:   entry.Body,
		Level: strings.ToLower(entry.SeverityText),
	}
	if row.Level == "" {
		row.Level = detectLevel(entry.Body)
	}
	attrs := map[string]string{}
	if jobID := entry.Attributes["job_id"]; jobID != "" {
		attrs["job_id"] = jobID
	}
	traceID := entry.Attributes["trace_id"]
	if traceID == "" {
		traceID = entry.TraceID
	}
	if traceID != "" {
		attrs["trace_id"] = traceID
	}
	if taskID := entry.Attributes["task_id"]; taskID != "" {
		attrs["task_id"] = taskID
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

func encodeTraceCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeTraceCursor(cursor string) (int, error) {
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
