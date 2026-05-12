package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegis/platform/config"
	"aegis/platform/dto"

	"github.com/sirupsen/logrus"
)

type Client struct {
	address    string
	httpClient *http.Client
}

type QueryOpts struct {
	Start     time.Time
	End       time.Time
	Limit     int
	Direction string
	// Extra filters appended to the base `{app="rcabench"} | task_id=<id>`
	// stream selector. Used by the filtered logs and histogram endpoints.
	// When provided, the substring is wrapped in a `|= "<q>"` filter; when
	// the caller wants to push a raw LogQL fragment they can pass it via
	// RawLogQL which is appended verbatim after the base selector.
	Substring string
	RawLogQL  string
}

type queryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// HistogramBucket captures a single time-bucket of log volume returned by
// Loki's `count_over_time` query, broken down by level.
type HistogramBucket struct {
	StartTS time.Time
	EndTS   time.Time
	Count   int64
	ByLevel map[string]int64
}

type matrixResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func NewClient() *Client {
	address := config.GetString("loki.address")
	timeout := config.GetString("loki.timeout")
	timeoutDuration := 10 * time.Second
	if timeout != "" {
		if d, err := time.ParseDuration(timeout); err == nil {
			timeoutDuration = d
		}
	}

	return &Client{
		address: address,
		httpClient: &http.Client{
			Timeout: timeoutDuration,
		},
	}
}

func (c *Client) QueryJobLogs(ctx context.Context, taskID string, opts QueryOpts) ([]dto.LogEntry, error) {
	if taskID == "" {
		return nil, fmt.Errorf("taskID is required")
	}

	if opts.Start.IsZero() {
		opts.Start = time.Now().Add(-1 * time.Hour)
	}
	if opts.End.IsZero() {
		opts.End = time.Now()
	}
	if opts.Limit <= 0 {
		maxEntries := config.GetInt("loki.max_entries")
		if maxEntries > 0 {
			opts.Limit = maxEntries
		} else {
			opts.Limit = 5000
		}
	}
	if opts.Direction == "" {
		opts.Direction = "forward"
	}

	logQL := buildJobLogQL(taskID, opts)

	params := url.Values{}
	params.Set("query", logQL)
	params.Set("start", strconv.FormatInt(opts.Start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(opts.End.UnixNano(), 10))
	params.Set("limit", strconv.Itoa(opts.Limit))
	params.Set("direction", opts.Direction)

	reqURL := fmt.Sprintf("%s/loki/api/v1/query_range?%s", c.address, params.Encode())
	logrus.Infof("Loki query: url=%s", reqURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Loki request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki query failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("loki query returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Loki response: %w", err)
	}

	var lokiResp queryRangeResponse
	if err := json.Unmarshal(body, &lokiResp); err != nil {
		return nil, fmt.Errorf("failed to parse Loki response: %w", err)
	}
	if lokiResp.Status != "success" {
		return nil, fmt.Errorf("loki query status: %s", lokiResp.Status)
	}
	if len(lokiResp.Data.Result) == 0 {
		logrus.Warnf("Loki returned 0 streams for task %s, raw response: %s", taskID, string(body))
	}

	var entries []dto.LogEntry
	for _, result := range lokiResp.Data.Result {
		for _, value := range result.Values {
			if len(value) < 2 {
				continue
			}
			nsec, err := strconv.ParseInt(value[0], 10, 64)
			if err != nil {
				logrus.Warnf("Loki: invalid timestamp %s: %v", value[0], err)
				continue
			}

			entries = append(entries, dto.LogEntry{
				Timestamp: time.Unix(0, nsec),
				Line:      value[1],
				TaskID:    taskID,
				TraceID:   result.Stream["trace_id"],
				JobID:     result.Stream["job_id"],
			})
		}
	}

	logrus.Infof("Loki: queried %d log entries for task %s", len(entries), taskID)
	return entries, nil
}

// buildJobLogQL builds the LogQL pipeline for the given task and optional
// substring / raw-logql filters. RawLogQL takes precedence over Substring
// when both are provided.
func buildJobLogQL(taskID string, opts QueryOpts) string {
	logQL := fmt.Sprintf(`{app="rcabench"} | task_id=%q`, taskID)
	switch {
	case strings.TrimSpace(opts.RawLogQL) != "":
		logQL = logQL + " " + strings.TrimSpace(opts.RawLogQL)
	case strings.TrimSpace(opts.Substring) != "":
		logQL = fmt.Sprintf(`%s |= %q`, logQL, opts.Substring)
	}
	return logQL
}

// QueryJobLogHistogram returns log-volume buckets for a task using Loki's
// `count_over_time` aggregation. It issues one matrix query for the total
// count and one per level (error/warn/info) so that the by_level breakdown
// reflects entries Loki itself classifies; entries without a `level` label
// fall through to the total count only.
func (c *Client) QueryJobLogHistogram(ctx context.Context, taskID string, opts QueryOpts, buckets int) ([]HistogramBucket, error) {
	if taskID == "" {
		return nil, fmt.Errorf("taskID is required")
	}
	if opts.Start.IsZero() || opts.End.IsZero() {
		return nil, fmt.Errorf("start and end are required for histogram")
	}
	if !opts.End.After(opts.Start) {
		return nil, fmt.Errorf("end must be after start")
	}
	if buckets <= 0 {
		buckets = 60
	}

	totalSpan := opts.End.Sub(opts.Start)
	step := totalSpan / time.Duration(buckets)
	if step < time.Second {
		step = time.Second
	}
	stepStr := lokiDuration(step)

	base := buildJobLogQL(taskID, opts)
	totals, err := c.queryRangeMatrix(ctx, fmt.Sprintf("count_over_time(%s [%s])", base, stepStr), opts.Start, opts.End, step)
	if err != nil {
		return nil, err
	}

	out := make([]HistogramBucket, 0, buckets)
	for ts, count := range totals {
		out = append(out, HistogramBucket{
			StartTS: ts,
			EndTS:   ts.Add(step),
			Count:   count,
			ByLevel: map[string]int64{},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartTS.Before(out[j].StartTS) })

	for _, level := range []string{"error", "warn", "info"} {
		levelQL := fmt.Sprintf(`%s |~ %q`, base, "(?i)"+level)
		levelCounts, err := c.queryRangeMatrix(ctx, fmt.Sprintf("count_over_time(%s [%s])", levelQL, stepStr), opts.Start, opts.End, step)
		if err != nil {
			return nil, err
		}
		for i := range out {
			if v, ok := levelCounts[out[i].StartTS]; ok {
				out[i].ByLevel[level] = v
			}
		}
	}

	return out, nil
}

func (c *Client) queryRangeMatrix(ctx context.Context, query string, start, end time.Time, step time.Duration) (map[time.Time]int64, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	params.Set("step", lokiDuration(step))

	reqURL := fmt.Sprintf("%s/loki/api/v1/query_range?%s", c.address, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Loki request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki histogram query failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("loki histogram returned status %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Loki response: %w", err)
	}
	var parsed matrixResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse Loki histogram response: %w", err)
	}
	if parsed.Status != "success" {
		return nil, fmt.Errorf("loki histogram status: %s", parsed.Status)
	}
	totals := make(map[time.Time]int64)
	for _, series := range parsed.Data.Result {
		for _, pair := range series.Values {
			if len(pair) < 2 {
				continue
			}
			tsFloat, ok := pair[0].(float64)
			if !ok {
				continue
			}
			valStr, ok := pair[1].(string)
			if !ok {
				continue
			}
			val, err := strconv.ParseInt(valStr, 10, 64)
			if err != nil {
				continue
			}
			ts := time.Unix(int64(tsFloat), 0).UTC()
			totals[ts] += val
		}
	}
	return totals, nil
}

func lokiDuration(d time.Duration) string {
	if d <= 0 {
		return "1s"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}
