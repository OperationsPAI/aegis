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

	chinfra "aegis/platform/clickhouse"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/httpx"

	"github.com/gin-gonic/gin"
)

// InjectionLogQueryReq captures the query parameters for the filtered logs
// endpoint. Substring + level filters push down into the ClickHouse query
// where possible (see platform/clickhouse/log_reader.buildJobLogsQuery);
// pagination still happens in-process after the scan because the cursor
// must remain stable across repeated calls.
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
// endpoint. `attrs` carries any OTel LogAttributes worth surfacing
// (job_id, trace_id, etc.) and `service` is sourced from the same labels
// when available. Shape is byte-identical to the previous Loki-backed
// version so the frontend DTO does not change.
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

// GetLogsFiltered returns paginated, filterable ClickHouse log entries for
// an injection's primary task. Pagination is applied in-process after the
// scan because the cursor must be stable across repeated calls — the SQL
// query already prunes by task / time / level / substring.
//
// ClickHouse errors are surfaced (no longer swallowed into an empty
// response): a Loki-not-deployed environment used to silently return an
// empty entries list, which made the frontend Logs panel render blank
// with no indication of misconfiguration. Returning the error lets the
// UI render an error state on react-query failure.
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
	if task.TraceID == "" {
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

	chCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Filter by trace_id so the panel sees ALL stage logs
	// (RestartPedestal / FaultInjection / BuildDatapack / RunAlgorithm /
	// CollectResult), not just the FaultInjection task injection.TaskID
	// happens to point at.
	entries, chErr := s.chLogReader.QueryTraceLogs(chCtx, task.TraceID, chinfra.LogQueryOpts{
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

	filtered := make([]InjectionLogEntry, 0, len(entries))
	for _, entry := range entries {
		// Prefer the entry's own task_id attribute over the injection's
		// owning TaskID so individual rows can be traced back to the
		// specific stage they came from.
		taskID := entry.Attributes["task_id"]
		if taskID == "" {
			taskID = *injection.TaskID
		}
		filtered = append(filtered, toInjectionLogEntry(entry, taskID))
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
//	@Param			q		query		string															false	"Substring filter on log body"
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

// substringFromQuery extracts the user-supplied substring filter. The
// previous Loki path also accepted raw LogQL fragments starting with `|`
// — that escape hatch is intentionally dropped: ClickHouse has no
// equivalent and exposing raw SQL would be a SQLi gift. Callers passing a
// LogQL-style fragment now get treated as a literal substring (which
// matches nothing useful) — that's acceptable because the only frontend
// caller supplies plain text from the search input.
func substringFromQuery(q string) string {
	q = strings.TrimSpace(q)
	if strings.HasPrefix(q, "|") {
		return ""
	}
	return q
}

var levelKeywords = []string{"error", "warn", "info", "debug"}

func toInjectionLogEntry(entry chinfra.LogEntry, taskID string) InjectionLogEntry {
	row := InjectionLogEntry{
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
	if taskID != "" {
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
