package injection

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	chinfra "aegis/platform/clickhouse"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/httpx"

	"github.com/gin-gonic/gin"
)

// InjectionLogHistogramReq captures the query parameters for the log
// histogram endpoint.
type InjectionLogHistogramReq struct {
	Start   string `form:"start" binding:"omitempty"`
	End     string `form:"end" binding:"omitempty"`
	Buckets int    `form:"buckets" binding:"omitempty"`
	Q       string `form:"q" binding:"omitempty"`
}

func (req *InjectionLogHistogramReq) Validate() error {
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
	if req.Buckets < 0 {
		return fmt.Errorf("buckets must be non-negative")
	}
	if req.Buckets > 1000 {
		return fmt.Errorf("buckets must be <= 1000")
	}
	return nil
}

// InjectionLogHistogramBucket is a single bucket in the histogram response.
type InjectionLogHistogramBucket struct {
	StartTS time.Time        `json:"start_ts"`
	EndTS   time.Time        `json:"end_ts"`
	Count   int64            `json:"count"`
	ByLevel map[string]int64 `json:"by_level,omitempty"`
}

// InjectionLogHistogramResp wraps the bucket list returned to the client.
type InjectionLogHistogramResp struct {
	Buckets []InjectionLogHistogramBucket `json:"buckets"`
}

// GetLogsHistogram returns time-bucketed log counts for an injection's
// primary task. Backed by a single ClickHouse aggregation; the
// per-severity breakdown comes from one GROUP BY (SeverityText) scan
// instead of N range queries the Loki path required.
//
// Empty windows still return an empty bucket list so the chart can render
// its zero-state, but ClickHouse errors are now surfaced rather than
// silently swallowed.
func (s *Service) GetLogsHistogram(ctx context.Context, id int, req *InjectionLogHistogramReq) (*InjectionLogHistogramResp, error) {
	injection, err := s.repo.loadInjection(id)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return nil, fmt.Errorf("%w: injection id: %d", consts.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get injection: %w", err)
	}

	resp := &InjectionLogHistogramResp{Buckets: []InjectionLogHistogramBucket{}}
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
	buckets := req.Buckets
	if buckets <= 0 {
		buckets = 60
	}

	chCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// The endpoint historically didn't accept a separate `level` filter —
	// it relied on Loki returning a level-broken-down series. The
	// ClickHouse path groups by SeverityText inside the same scan, so we
	// pass Level="" and let the reader populate ByLevel for the UI.
	histogram, chErr := s.chLogReader.QueryLogHistogram(chCtx, *injection.TaskID, chinfra.LogQueryOpts{
		Start:     start,
		End:       end,
		Substring: substringFromQuery(req.Q),
	}, buckets)
	if chErr != nil {
		return nil, fmt.Errorf("clickhouse log histogram query failed: %w", chErr)
	}

	out := make([]InjectionLogHistogramBucket, 0, len(histogram))
	for _, b := range histogram {
		out = append(out, InjectionLogHistogramBucket{
			StartTS: b.StartTS,
			EndTS:   b.EndTS,
			Count:   b.Count,
			ByLevel: b.ByLevel,
		})
	}
	resp.Buckets = out
	return resp, nil
}

// GetInjectionLogsHistogram returns time-bucketed log volume for the injection.
//
//	@Summary		Bucketed log volume
//	@Description	Return per-bucket log counts (with by-level breakdown) for an injection's logs
//	@Tags			Injections
//	@ID				get_injection_logs_histogram
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		int																true	"Injection ID"
//	@Param			start	query		string															false	"RFC3339 start time"
//	@Param			end		query		string															false	"RFC3339 end time"
//	@Param			buckets	query		int																false	"Number of buckets"	default(60)
//	@Param			q		query		string															false	"Substring filter on log body"
//	@Success		200		{object}	dto.GenericResponse[InjectionLogHistogramResp]	"Histogram returned"
//	@Failure		400		{object}	dto.GenericResponse[any]										"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]										"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]										"Permission denied"
//	@Failure		404		{object}	dto.GenericResponse[any]										"Injection not found"
//	@Failure		500		{object}	dto.GenericResponse[any]										"Internal server error"
//	@Router			/api/v2/injections/{id}/logs/histogram [get]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) GetInjectionLogsHistogram(c *gin.Context) {
	id, ok := parsePositiveID(c, consts.URLPathID, "injection ID")
	if !ok {
		return
	}
	var req InjectionLogHistogramReq
	if err := c.ShouldBindQuery(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Validation failed: "+err.Error())
		return
	}
	resp, err := h.service.GetLogsHistogram(c.Request.Context(), id, &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}
