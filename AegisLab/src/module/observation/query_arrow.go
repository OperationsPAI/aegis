//go:build duckdb_arrow

package observation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

func (s *Service) GetMetricsCatalog(_ context.Context, _ int) (*MetricsCatalogResp, error) {
	return nil, errNotImplemented
}

func (s *Service) GetMetricsSeries(_ context.Context, _ int, _ *MetricsSeriesReq) (*MetricsSeriesResp, error) {
	return nil, errNotImplemented
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
