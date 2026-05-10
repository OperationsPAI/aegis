//go:build duckdb_arrow

package observation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

// Parquet filename conventions used inside a datapack. These are the only
// inputs observation reads, mirroring datapack_store.go validParquetFiles.
const (
	abnormalMetricsFile = "abnormal_metrics.parquet"
	normalMetricsFile   = "normal_metrics.parquet"
	abnormalTracesFile  = "abnormal_traces.parquet"
	normalTracesFile    = "normal_traces.parquet"
)

// resolveDatapackParquet returns the absolute path of fileName within the
// datapack of the given injection id, or an error if the datapack is not
// ready or the file is missing.
func (s *Service) resolveDatapackParquet(ctx context.Context, id int, fileName string) (string, error) {
	name, err := s.injections.GetReadyDatapackName(ctx, id)
	if err != nil {
		return "", err
	}
	return s.store.ResolveFilePath(name, fileName)
}

// describeColumns returns the column types of a parquet file via duckdb's
// `DESCRIBE SELECT *` introspection. The returned map keys are column names
// (verbatim) and values are normalised (uppercase) duckdb type strings.
func describeColumns(ctx context.Context, db *sql.DB, parquet string) (map[string]string, []string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("DESCRIBE SELECT * FROM read_parquet('%s')", parquet))
	if err != nil {
		return nil, nil, fmt.Errorf("describe parquet failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cols := make(map[string]string)
	order := []string{}
	for rows.Next() {
		var name, typ, null, key, def, extra string
		if err := rows.Scan(&name, &typ, &null, &key, &def, &extra); err != nil {
			return nil, nil, err
		}
		cols[name] = strings.ToUpper(strings.TrimSpace(typ))
		order = append(order, name)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return cols, order, nil
}

func quoteIdent(name string) string {
	return fmt.Sprintf("\"%s\"", strings.ReplaceAll(name, "\"", "\"\""))
}

func openDuckDB() (*sql.DB, error) {
	connector, err := duckdb.NewConnector("", nil)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}

func isNumericDuckType(t string) bool {
	switch {
	case strings.HasPrefix(t, "DECIMAL"), t == "FLOAT", t == "DOUBLE", t == "REAL",
		t == "TINYINT", t == "SMALLINT", t == "INTEGER", t == "BIGINT", t == "HUGEINT",
		t == "UTINYINT", t == "USMALLINT", t == "UINTEGER", t == "UBIGINT", t == "UHUGEINT":
		return true
	}
	return false
}

func isTimestampDuckType(t string) bool {
	return strings.HasPrefix(t, "TIMESTAMP") || t == "DATE" || t == "TIME"
}

// pickColumn returns the first column name from candidates that exists in cols.
func pickColumn(cols map[string]string, candidates ...string) string {
	for _, c := range candidates {
		if _, ok := cols[c]; ok {
			return c
		}
	}
	return ""
}

// timestampExpr returns a duckdb expression that converts the column to a
// TIMESTAMP regardless of its on-disk type. Integer columns are assumed to be
// nanoseconds since epoch (matching the OTel time_unix_nano convention).
func timestampExpr(quoted string, typ string) string {
	switch {
	case strings.HasPrefix(typ, "TIMESTAMP"):
		return quoted
	case typ == "DATE":
		return fmt.Sprintf("CAST(%s AS TIMESTAMP)", quoted)
	case typ == "BIGINT" || typ == "UBIGINT" || typ == "HUGEINT" || typ == "UHUGEINT":
		return fmt.Sprintf("make_timestamp(CAST(%s AS BIGINT) / 1000)", quoted)
	default:
		return fmt.Sprintf("CAST(%s AS TIMESTAMP)", quoted)
	}
}

// span column lookup encapsulates the assumed parquet schema. We resolve real
// column names through DESCRIBE rather than hardcoding so the same code works
// across collector versions, but the candidate sets reflect the OTel→Parquet
// conventions used by AegisLab's datapack builder.
type spanColumns struct {
	traceID    string
	spanID     string
	parentID   string
	service    string
	op         string
	startTS    string
	endTS      string
	statusCode string
	statusType string
	attrs      string
	events     string
	durationNS string
}

func resolveSpanColumns(cols map[string]string) (*spanColumns, error) {
	sc := &spanColumns{
		traceID:    pickColumn(cols, "trace_id", "TraceId", "TraceID"),
		spanID:     pickColumn(cols, "span_id", "SpanId", "SpanID"),
		parentID:   pickColumn(cols, "parent_span_id", "parent_id", "ParentSpanId", "ParentSpanID"),
		service:    pickColumn(cols, "service_name", "ServiceName", "service"),
		op:         pickColumn(cols, "name", "operation_name", "SpanName", "span_name"),
		startTS:    pickColumn(cols, "start_time", "start_time_unix_nano", "Timestamp", "timestamp"),
		endTS:      pickColumn(cols, "end_time", "end_time_unix_nano", "EndTimestamp"),
		statusCode: pickColumn(cols, "status_code", "StatusCode"),
		statusType: pickColumn(cols, "status_message", "StatusMessage"),
		attrs:      pickColumn(cols, "span_attributes", "attributes", "SpanAttributes", "Attributes"),
		events:     pickColumn(cols, "events", "Events"),
		durationNS: pickColumn(cols, "duration", "Duration", "duration_ns", "duration_nano"),
	}
	if sc.traceID == "" || sc.spanID == "" || sc.service == "" || sc.op == "" || sc.startTS == "" {
		return nil, fmt.Errorf("traces parquet missing required columns (need trace_id, span_id, service, op, start_ts)")
	}
	return sc, nil
}

// spanDurationExpr returns a duckdb expression yielding the span duration in
// nanoseconds. If the parquet exposes an explicit duration column we use it,
// otherwise we compute end - start.
func spanDurationExpr(sc *spanColumns, cols map[string]string) string {
	if sc.durationNS != "" {
		t := cols[sc.durationNS]
		if strings.HasPrefix(t, "INTERVAL") {
			return fmt.Sprintf("epoch_ns(%s)", quoteIdent(sc.durationNS))
		}
		return fmt.Sprintf("CAST(%s AS BIGINT)", quoteIdent(sc.durationNS))
	}
	if sc.endTS == "" {
		return "0"
	}
	startExpr := timestampExpr(quoteIdent(sc.startTS), cols[sc.startTS])
	endExpr := timestampExpr(quoteIdent(sc.endTS), cols[sc.endTS])
	return fmt.Sprintf("(epoch_ns(%s) - epoch_ns(%s))", endExpr, startExpr)
}

// spanErrorPredicate returns a boolean SQL predicate that identifies
// error-status spans. Tries status_code (textual) first, falls back to never.
func spanErrorPredicate(sc *spanColumns) string {
	if sc.statusCode != "" {
		return fmt.Sprintf("UPPER(CAST(%s AS VARCHAR)) IN ('STATUS_CODE_ERROR', 'ERROR', '2')", quoteIdent(sc.statusCode))
	}
	return "FALSE"
}

// spanStatusExpr returns a SQL expression yielding the textual status (ok|error).
func spanStatusExpr(sc *spanColumns) string {
	if sc.statusCode == "" {
		return "'ok'"
	}
	return fmt.Sprintf("CASE WHEN UPPER(CAST(%s AS VARCHAR)) IN ('STATUS_CODE_ERROR', 'ERROR', '2') THEN 'error' ELSE 'ok' END", quoteIdent(sc.statusCode))
}

// parseJSONOrPairs accepts a JSON object string and returns it as a map; for
// other duckdb stringifications (MAP / STRUCT) it returns a single-key wrapper
// so the frontend always sees an object.
func parseJSONOrPairs(s string) map[string]interface{} {
	if s == "" {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err == nil {
		return m
	}
	return map[string]interface{}{"raw": s}
}

func parseSpanEvents(s string) []SpanEvent {
	if s == "" {
		return nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return []SpanEvent{{Name: "raw", Attributes: map[string]interface{}{"value": s}}}
	}
	out := make([]SpanEvent, 0, len(raw))
	for _, r := range raw {
		ev := SpanEvent{}
		if v, ok := r["ts"]; ok {
			ev.TS = fmt.Sprint(v)
		} else if v, ok := r["timestamp"]; ok {
			ev.TS = fmt.Sprint(v)
		}
		if v, ok := r["name"]; ok {
			ev.Name = fmt.Sprint(v)
		}
		if v, ok := r["attributes"]; ok {
			if m, ok := v.(map[string]interface{}); ok {
				ev.Attributes = m
			}
		}
		out = append(out, ev)
	}
	return out
}

var errNotImplemented = errors.New("observation endpoint not yet implemented")

// L3.1 — metrics catalog
//
// Strategy: pick the abnormal_metrics.parquet file, run DESCRIBE, expose every
// column as either a metric (numeric) or a dimension (textual). For a richer
// catalog the parquet would need explicit metric metadata; here we surface
// what duckdb sees so the frontend can drive selectors honestly without
// fabricated quantiles or descriptions.
func (s *Service) GetMetricsCatalog(ctx context.Context, id int) (*MetricsCatalogResp, error) {
	parquet, err := s.resolveDatapackParquet(ctx, id, abnormalMetricsFile)
	if err != nil {
		return nil, err
	}

	db, err := openDuckDB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	cols, order, err := describeColumns(ctx, db, parquet)
	if err != nil {
		return nil, err
	}

	dims := []string{}
	metrics := []MetricCatalogItem{}
	for _, name := range order {
		typ := cols[name]
		if isNumericDuckType(typ) && !isTimestampDuckType(typ) {
			metrics = append(metrics, MetricCatalogItem{Name: name})
		} else if !isTimestampDuckType(typ) {
			dims = append(dims, name)
		}
	}
	for i := range metrics {
		metrics[i].Dimensions = dims
	}

	return &MetricsCatalogResp{Metrics: metrics}, nil
}

// L3.2 — metrics time series
//
// Strategy: parse start/end (RFC3339), bucket by step using duckdb's time_bucket,
// average the metric column per bucket. Hardcoded assumption: the metrics parquet
// has a timestamp column named one of {time, timestamp, ts, time_unix_nano}. If
// the requested metric or timestamp column is absent we return an explicit error
// rather than mocking. group_by selects an additional GROUP BY dimension; filter
// is parsed as `dim=value` and added to the WHERE clause via parameterised SQL.
func (s *Service) GetMetricsSeries(ctx context.Context, id int, req *MetricsSeriesReq) (*MetricsSeriesResp, error) {
	parquet, err := s.resolveDatapackParquet(ctx, id, abnormalMetricsFile)
	if err != nil {
		return nil, err
	}

	db, err := openDuckDB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	cols, _, err := describeColumns(ctx, db, parquet)
	if err != nil {
		return nil, err
	}

	if _, ok := cols[req.Metric]; !ok {
		return nil, fmt.Errorf("metric %q not found in %s", req.Metric, filepath.Base(parquet))
	}
	if !isNumericDuckType(cols[req.Metric]) {
		return nil, fmt.Errorf("metric %q is not numeric (type %s)", req.Metric, cols[req.Metric])
	}

	tsCol := pickColumn(cols, "time", "timestamp", "ts", "time_unix_nano")
	if tsCol == "" {
		return nil, fmt.Errorf("no timestamp column found in %s (expected one of: time, timestamp, ts, time_unix_nano)", filepath.Base(parquet))
	}

	step := strings.TrimSpace(req.Step)
	if step == "" {
		step = "30s"
	}
	stepDur, err := time.ParseDuration(step)
	if err != nil {
		return nil, fmt.Errorf("invalid step %q: %w", step, err)
	}
	stepSeconds := int64(stepDur.Seconds())
	if stepSeconds <= 0 {
		return nil, fmt.Errorf("step must be at least 1 second, got %q", step)
	}

	tsExpr := timestampExpr(quoteIdent(tsCol), cols[tsCol])

	var conds []string
	args := []interface{}{}
	if req.Start != "" {
		t, err := time.Parse(time.RFC3339, req.Start)
		if err != nil {
			return nil, fmt.Errorf("invalid start: %w", err)
		}
		conds = append(conds, fmt.Sprintf("%s >= ?", tsExpr))
		args = append(args, t.UTC())
	}
	if req.End != "" {
		t, err := time.Parse(time.RFC3339, req.End)
		if err != nil {
			return nil, fmt.Errorf("invalid end: %w", err)
		}
		conds = append(conds, fmt.Sprintf("%s < ?", tsExpr))
		args = append(args, t.UTC())
	}

	if req.Filter != "" {
		dim, val, ok := strings.Cut(req.Filter, "=")
		if !ok {
			return nil, fmt.Errorf("filter must be dim=value, got %q", req.Filter)
		}
		dim = strings.TrimSpace(dim)
		val = strings.TrimSpace(val)
		if _, exists := cols[dim]; !exists {
			return nil, fmt.Errorf("filter dimension %q not found", dim)
		}
		conds = append(conds, fmt.Sprintf("%s = ?", quoteIdent(dim)))
		args = append(args, val)
	}

	groupBy := strings.TrimSpace(req.GroupBy)
	if groupBy != "" {
		if _, exists := cols[groupBy]; !exists {
			return nil, fmt.Errorf("group_by dimension %q not found", groupBy)
		}
	}

	groupColExpr := ""
	if groupBy != "" {
		groupColExpr = fmt.Sprintf(", CAST(%s AS VARCHAR) AS grp", quoteIdent(groupBy))
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	groupClause := ""
	if groupBy != "" {
		groupClause = ", grp"
	}

	query := fmt.Sprintf(
		`SELECT time_bucket(INTERVAL %d SECOND, %s) AS bucket, avg(CAST(%s AS DOUBLE)) AS value%s
		 FROM read_parquet('%s')
		 %s
		 GROUP BY bucket%s
		 ORDER BY bucket`,
		stepSeconds,
		tsExpr,
		quoteIdent(req.Metric),
		groupColExpr,
		parquet,
		where,
		groupClause,
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("metrics series query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	seriesByLabel := map[string]*MetricSeries{}
	for rows.Next() {
		var bucket time.Time
		var value sql.NullFloat64
		var grp sql.NullString
		if groupBy != "" {
			if err := rows.Scan(&bucket, &value, &grp); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&bucket, &value); err != nil {
				return nil, err
			}
		}
		key := ""
		labels := map[string]string{}
		if groupBy != "" {
			key = grp.String
			labels[groupBy] = grp.String
		}
		series, ok := seriesByLabel[key]
		if !ok {
			series = &MetricSeries{Labels: labels, Points: []MetricPoint{}}
			seriesByLabel[key] = series
		}
		v := 0.0
		if value.Valid {
			v = value.Float64
		}
		series.Points = append(series.Points, MetricPoint{TS: bucket.UTC().Format(time.RFC3339Nano), Value: v})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]MetricSeries, 0, len(seriesByLabel))
	keys := make([]string, 0, len(seriesByLabel))
	for k := range seriesByLabel {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, *seriesByLabel[k])
	}
	return &MetricsSeriesResp{Series: out, Step: step}, nil
}

func (s *Service) ListSpans(_ context.Context, _ int, _ *ListSpansReq) (*ListSpansResp, error) {
	return nil, errNotImplemented
}

func (s *Service) GetSpanTree(_ context.Context, _ int, _ string) (*SpanTreeResp, error) {
	return nil, errNotImplemented
}

func (s *Service) GetServiceMap(_ context.Context, _ int, _ *ServiceMapReq) (*ServiceMapResp, error) {
	return nil, errNotImplemented
}
