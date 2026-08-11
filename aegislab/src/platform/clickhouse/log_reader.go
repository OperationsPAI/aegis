// Package clickhouse wraps the ClickHouse otel.* tables aegislab queries
// for observability features. log_reader.go reads otel.otel_logs to back
// the /injections/<id>/logs and /injections/<id>/logs/histogram endpoints
// after the migration off Loki (Loki was never deployed in byte-cluster;
// see commit message for context).
package clickhouse

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2"

	db "aegis/platform/db"
)

// orchestratorServiceName is the ServiceName resource attribute the
// aegislab OTel pipeline stamps onto every log record it emits (see
// platform/tracing/logs_provider.go and the shared rcabenchResource).
// Filtering on it keeps the result set scoped to aegislab's own logs and
// excludes anything ingested from the SUT under different ServiceName
// values. Mirrors the trace-side constant in
// crud/observability/trace/spans_clickhouse.go.
const orchestratorServiceName = "rcabench"

// LogEntry is the projection of one otel.otel_logs row we surface to the
// domain layer. Attributes carries every LogAttribute key/value so callers
// can pick out task_id, job_id, trace_id, etc. without needing the table
// schema. Mirrors `dto.LogEntry` in shape but stays in the platform layer
// to keep the ClickHouse reader independent of the orchestrator DTO.
type LogEntry struct {
	Timestamp    time.Time
	SeverityText string
	Body         string
	TraceID      string
	SpanID       string
	Attributes   map[string]string
}

// LogQueryOpts narrows the otel.otel_logs scan to a single task window
// with optional SQL-pushed filters. Empty Level / Substring leave the
// corresponding predicate off entirely so the query plan stays small.
type LogQueryOpts struct {
	Start     time.Time
	End       time.Time
	Limit     int
	Level     string
	Substring string
}

// HistogramBucket is one row of the time-bucketed count() result.
// ByLevel maps SeverityText (lower-cased) → count for that bucket.
type HistogramBucket struct {
	StartTS time.Time
	EndTS   time.Time
	Count   int64
	ByLevel map[string]int64
}

// LogReader is the seam consumers depend on so we can stub the ClickHouse
// connection in tests. Production implementation is *clickHouseLogReader,
// returned by NewClickHouseLogReader.
//
// QueryJobLogs / QueryLogHistogram filter by `LogAttributes['task_id']`
// — useful when streaming the logs of one specific worker pickup.
//
// QueryTraceLogs / QueryTraceLogHistogram filter by
// `LogAttributes['trace_id']` — this is what the injection-detail UI
// wants, because a single injection spans multiple tasks
// (RestartPedestal → FaultInjection → BuildDatapack → AlgorithmRun →
// CollectResult) and the user expects to see logs from all of them.
type LogReader interface {
	QueryJobLogs(ctx context.Context, taskID string, opts LogQueryOpts) ([]LogEntry, error)
	QueryLogHistogram(ctx context.Context, taskID string, opts LogQueryOpts, buckets int) ([]HistogramBucket, error)
	QueryTraceLogs(ctx context.Context, traceID string, opts LogQueryOpts) ([]LogEntry, error)
	QueryTraceLogHistogram(ctx context.Context, traceID string, opts LogQueryOpts, buckets int) ([]HistogramBucket, error)
}

type clickHouseLogReader struct {
	cfg *db.DatabaseConfig
}

// NewClickHouseLogReader builds the production reader from the same
// [database.clickhouse] config block the trace reader uses, so the two
// observability paths share connection parameters.
func NewClickHouseLogReader() LogReader {
	return &clickHouseLogReader{cfg: db.NewDatabaseConfig("clickhouse")}
}

func (r *clickHouseLogReader) openConn() (chdriver.Conn, error) {
	if r.cfg == nil || r.cfg.Host == "" {
		return nil, fmt.Errorf("clickhouse host not configured")
	}
	return chdriver.Open(&chdriver.Options{
		Addr: []string{net.JoinHostPort(r.cfg.Host, strconv.Itoa(r.cfg.Port))},
		Auth: chdriver.Auth{
			Database: "otel",
			Username: r.cfg.User,
			Password: r.cfg.Password,
		},
		Protocol:    chdriver.HTTP,
		DialTimeout: 3 * time.Second,
	})
}

func (r *clickHouseLogReader) QueryJobLogs(ctx context.Context, taskID string, opts LogQueryOpts) ([]LogEntry, error) {
	if taskID == "" {
		return nil, fmt.Errorf("taskID is required")
	}
	return r.queryLogsByAttr(ctx, "task_id", taskID, opts)
}

func (r *clickHouseLogReader) QueryTraceLogs(ctx context.Context, traceID string, opts LogQueryOpts) ([]LogEntry, error) {
	if traceID == "" {
		return nil, fmt.Errorf("traceID is required")
	}
	return r.queryLogsByAttr(ctx, "trace_id", traceID, opts)
}

func (r *clickHouseLogReader) queryLogsByAttr(ctx context.Context, attrKey, attrValue string, opts LogQueryOpts) ([]LogEntry, error) {
	if opts.Start.IsZero() {
		opts.Start = time.Now().Add(-1 * time.Hour)
	}
	if opts.End.IsZero() {
		opts.End = time.Now()
	}
	if opts.Limit <= 0 {
		opts.Limit = 5000
	}

	conn, err := r.openConn()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	stmt, args := buildLogsQuery(attrKey, attrValue, opts)
	rows, err := conn.Query(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse query logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []LogEntry
	for rows.Next() {
		var (
			entry LogEntry
			ts    time.Time
			attrs map[string]string
		)
		if err := rows.Scan(
			&ts,
			&entry.SeverityText,
			&entry.Body,
			&entry.TraceID,
			&entry.SpanID,
			&attrs,
		); err != nil {
			return nil, fmt.Errorf("clickhouse scan log row: %w", err)
		}
		entry.Timestamp = ts
		entry.Attributes = attrs
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse iter log rows: %w", err)
	}
	return out, nil
}

func (r *clickHouseLogReader) QueryLogHistogram(ctx context.Context, taskID string, opts LogQueryOpts, buckets int) ([]HistogramBucket, error) {
	if taskID == "" {
		return nil, fmt.Errorf("taskID is required")
	}
	return r.histogramByAttr(ctx, "task_id", taskID, opts, buckets)
}

func (r *clickHouseLogReader) QueryTraceLogHistogram(ctx context.Context, traceID string, opts LogQueryOpts, buckets int) ([]HistogramBucket, error) {
	if traceID == "" {
		return nil, fmt.Errorf("traceID is required")
	}
	return r.histogramByAttr(ctx, "trace_id", traceID, opts, buckets)
}

func (r *clickHouseLogReader) histogramByAttr(ctx context.Context, attrKey, attrValue string, opts LogQueryOpts, buckets int) ([]HistogramBucket, error) {
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
	stepSec := int64(step / time.Second)

	conn, err := r.openConn()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	stmt, args := buildHistogramQuery(attrKey, attrValue, opts, stepSec)
	rows, err := conn.Query(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse query log histogram: %w", err)
	}
	defer func() { _ = rows.Close() }()

	bucketByTS := make(map[time.Time]*HistogramBucket)
	for rows.Next() {
		var (
			bucketTS time.Time
			sev      string
			count    uint64
		)
		if err := rows.Scan(&bucketTS, &sev, &count); err != nil {
			return nil, fmt.Errorf("clickhouse scan histogram row: %w", err)
		}
		bucket, ok := bucketByTS[bucketTS]
		if !ok {
			bucket = &HistogramBucket{
				StartTS: bucketTS,
				EndTS:   bucketTS.Add(step),
				ByLevel: map[string]int64{},
			}
			bucketByTS[bucketTS] = bucket
		}
		bucket.Count += int64(count)
		if sev != "" {
			bucket.ByLevel[strings.ToLower(sev)] += int64(count)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse iter histogram rows: %w", err)
	}

	out := make([]HistogramBucket, 0, len(bucketByTS))
	for _, b := range bucketByTS {
		out = append(out, *b)
	}
	sortBuckets(out)
	return out, nil
}

// buildLogsQuery assembles the SELECT used by both QueryJobLogs and
// QueryTraceLogs. The two differ only in which LogAttributes key they
// scope on (`task_id` vs `trace_id`); everything else — level pushdown,
// substring pushdown, time bounds, limit — is identical. Exposing
// attrKey lets the same SQL shape serve both callers without copy-paste.
func buildLogsQuery(attrKey, attrValue string, opts LogQueryOpts) (string, []any) {
	var (
		preds []string
		args  []any
	)
	// trace_id / task_id is already the safety predicate — only aegis-
	// stamped records (orchestrator OTLP push + DaemonSet-filtered pod
	// stdout) carry it. Filtering on ServiceName here used to exclude
	// pod-stdout lines whose ServiceName is empty, dropping the algorithm
	// pod's actual output from the LOGs panel.
	preds = append(preds, fmt.Sprintf("LogAttributes[%s] = ?", quoteAttrKey(attrKey)))
	args = append(args, attrValue)
	preds = append(preds, "Timestamp >= ?")
	args = append(args, opts.Start)
	preds = append(preds, "Timestamp <= ?")
	args = append(args, opts.End)
	if lvl := strings.TrimSpace(opts.Level); lvl != "" {
		preds = append(preds, "lower(SeverityText) = ?")
		args = append(args, strings.ToLower(lvl))
	}
	if sub := strings.TrimSpace(opts.Substring); sub != "" {
		preds = append(preds, "positionCaseInsensitive(Body, ?) > 0")
		args = append(args, sub)
	}

	stmt := `
		SELECT
			Timestamp,
			SeverityText,
			Body,
			hex(TraceId) AS trace_id,
			hex(SpanId)  AS span_id,
			LogAttributes
		FROM otel.otel_logs
		WHERE ` + strings.Join(preds, "\n\t\t  AND ") + `
		ORDER BY Timestamp ASC
		LIMIT ?`
	args = append(args, opts.Limit)
	return stmt, args
}

// buildHistogramQuery is the histogram sibling of buildLogsQuery. The
// bucket width is interpolated into the SQL (not passed as a parameter)
// because ClickHouse's INTERVAL syntax doesn't accept positional ints in
// the duration position — we sanitize stepSec to a small positive int up
// the call chain so this is safe.
func buildHistogramQuery(attrKey, attrValue string, opts LogQueryOpts, stepSec int64) (string, []any) {
	var (
		preds []string
		args  []any
	)
	preds = append(preds, fmt.Sprintf("LogAttributes[%s] = ?", quoteAttrKey(attrKey)))
	args = append(args, attrValue)
	preds = append(preds, "Timestamp >= ?")
	args = append(args, opts.Start)
	preds = append(preds, "Timestamp <= ?")
	args = append(args, opts.End)
	if lvl := strings.TrimSpace(opts.Level); lvl != "" {
		preds = append(preds, "lower(SeverityText) = ?")
		args = append(args, strings.ToLower(lvl))
	}
	if sub := strings.TrimSpace(opts.Substring); sub != "" {
		preds = append(preds, "positionCaseInsensitive(Body, ?) > 0")
		args = append(args, sub)
	}

	stmt := fmt.Sprintf(`
		SELECT
			toStartOfInterval(Timestamp, INTERVAL %d SECOND) AS bucket,
			SeverityText,
			count() AS n
		FROM otel.otel_logs
		WHERE %s
		GROUP BY bucket, SeverityText
		ORDER BY bucket ASC`, stepSec, strings.Join(preds, "\n\t\t  AND "))
	return stmt, args
}

// quoteAttrKey hardens against an SQLi vector that would otherwise open
// if a caller passed an attacker-influenced key (callers today use
// constant string literals, but defense in depth). ClickHouse identifier
// quoting uses backticks; LogAttributes[...] indexing wants the key as a
// quoted string. Reject anything outside the lowercase-letter / digit /
// underscore alphabet — every legitimate OTel attribute key respects it.
func quoteAttrKey(k string) string {
	for _, r := range k {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '.' {
			panic(fmt.Sprintf("invalid log attribute key: %q", k))
		}
	}
	return "'" + k + "'"
}

func sortBuckets(buckets []HistogramBucket) {
	// Insertion sort — bucket counts are bounded by the histogram width
	// (typically ≤ 60) so the O(n²) is fine and avoids importing sort
	// just for this one call.
	for i := 1; i < len(buckets); i++ {
		for j := i; j > 0 && buckets[j-1].StartTS.After(buckets[j].StartTS); j-- {
			buckets[j-1], buckets[j] = buckets[j], buckets[j-1]
		}
	}
}
