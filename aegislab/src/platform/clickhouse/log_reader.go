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
type LogReader interface {
	QueryJobLogs(ctx context.Context, taskID string, opts LogQueryOpts) ([]LogEntry, error)
	QueryLogHistogram(ctx context.Context, taskID string, opts LogQueryOpts, buckets int) ([]HistogramBucket, error)
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

	stmt, args := buildJobLogsQuery(taskID, opts)
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

	stmt, args := buildHistogramQuery(taskID, opts, stepSec)
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

// buildJobLogsQuery assembles the SELECT for QueryJobLogs together with
// its positional arguments. Split out for unit-testability — exposing
// the SQL shape lets us assert filter pushdown without standing up
// ClickHouse.
func buildJobLogsQuery(taskID string, opts LogQueryOpts) (string, []any) {
	var (
		preds []string
		args  []any
	)
	preds = append(preds, "ServiceName = ?")
	args = append(args, orchestratorServiceName)
	preds = append(preds, "LogAttributes['task_id'] = ?")
	args = append(args, taskID)
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

// buildHistogramQuery is the histogram sibling of buildJobLogsQuery. The
// bucket width is interpolated into the SQL (not passed as a parameter)
// because ClickHouse's INTERVAL syntax doesn't accept positional ints in
// the duration position — we sanitize stepSec to a small positive int up
// the call chain so this is safe.
func buildHistogramQuery(taskID string, opts LogQueryOpts, stepSec int64) (string, []any) {
	var (
		preds []string
		args  []any
	)
	preds = append(preds, "ServiceName = ?")
	args = append(args, orchestratorServiceName)
	preds = append(preds, "LogAttributes['task_id'] = ?")
	args = append(args, taskID)
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
