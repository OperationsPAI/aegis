package trace

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2"

	db "aegis/platform/db"
)

// orchestratorServiceName is the ServiceName attribute the aegislab runtime
// stamps on every span it exports via OTLP (see platform/tracing/provider.go).
// Filtering on this keeps the span tree scoped to aegislab's own activity and
// excludes spans the OTel collector ingests from the SUT.
const orchestratorServiceName = "rcabench"

// orchestratorTraceAttr is the SpanAttribute carrying the aegislab trace UUID
// (consts.TraceCarrier propagates it through the orchestrator). Each OTel
// TraceId emitted under a single aegis trace gets this attribute on at least
// the root span, so the two-stage IN query below can fan from one aegis
// trace_id to N OTel traces and pull every descendant span.
const orchestratorTraceAttr = "trace_id"

// SpanReader is the seam the trace service uses to fetch orchestrator OTel
// spans from ClickHouse. Mirrors the FreshnessProbe pattern in
// core/orchestrator/freshness.go so tests can fake the storage layer without
// opening a TCP connection.
type SpanReader interface {
	ReadSpansByTraceID(ctx context.Context, traceID string) ([]OTelSpanRow, error)
}

// OTelSpanRow is the raw shape of one otel.otel_traces row that we project
// into a SpanNode for the API response. Kept separate so service-layer code
// can defer the conversion until it sees the rowset (e.g. to compute relative
// startNs against the earliest span in the trace).
type OTelSpanRow struct {
	TraceID         string
	SpanID          string
	ParentSpanID    string
	ServiceName     string
	SpanName        string
	Timestamp       time.Time
	DurationNanos   uint64
	StatusCode      string
	SpanAttributes  map[string]string
	ResourceService string
}

type clickHouseSpanReader struct {
	cfg *db.DatabaseConfig
}

// NewClickHouseSpanReader builds the production reader from the same
// [database.clickhouse] config block that the FreshnessProbe uses.
func NewClickHouseSpanReader() SpanReader {
	return &clickHouseSpanReader{cfg: db.NewDatabaseConfig("clickhouse")}
}

func (r *clickHouseSpanReader) ReadSpansByTraceID(ctx context.Context, traceID string) ([]OTelSpanRow, error) {
	if r.cfg == nil || r.cfg.Host == "" {
		return nil, fmt.Errorf("clickhouse host not configured")
	}

	conn, err := chdriver.Open(&chdriver.Options{
		Addr: []string{net.JoinHostPort(r.cfg.Host, strconv.Itoa(r.cfg.Port))},
		Auth: chdriver.Auth{
			Database: "otel",
			Username: r.cfg.User,
			Password: r.cfg.Password,
		},
		Protocol:    chdriver.HTTP,
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("clickhouse open: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Two-stage fan-out:
	//   1. inner SELECT discovers every OTel TraceId tagged with this aegis
	//      trace UUID on at least one span. The attribute lives on the root
	//      span of each task; child DB/HTTP spans inherit context but not
	//      attributes, so we need this hop to find them.
	//   2. outer SELECT pulls every span whose OTel TraceId is in that set.
	//
	// We pin a 7-day lookback so the inner partition prune actually fires —
	// the table is `PARTITION BY toDate(Timestamp)` and a full-table scan
	// for one rarely-used attribute would be expensive. 7 days lines up with
	// the retention window we keep for orchestration traces in PostgreSQL,
	// so anything still visible in /traces should also resolve here.
	const stmt = `
		SELECT
			TraceId,
			SpanId,
			ParentSpanId,
			ServiceName,
			SpanName,
			Timestamp,
			Duration,
			StatusCode,
			SpanAttributes,
			ResourceAttributes['service.name'] AS resource_service
		FROM otel.otel_traces
		WHERE TraceId IN (
			SELECT DISTINCT TraceId FROM otel.otel_traces
			WHERE ServiceName = ?
			  AND SpanAttributes[?] = ?
			  AND Timestamp > now() - INTERVAL 7 DAY
		)
		  AND Timestamp > now() - INTERVAL 7 DAY
		ORDER BY Timestamp ASC
	`
	rows, err := conn.Query(ctx, stmt, orchestratorServiceName, orchestratorTraceAttr, traceID)
	if err != nil {
		return nil, fmt.Errorf("clickhouse query spans: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []OTelSpanRow
	for rows.Next() {
		var (
			row     OTelSpanRow
			attrs   map[string]string
			ts      time.Time
			dur     uint64
			statusC string
		)
		if err := rows.Scan(
			&row.TraceID,
			&row.SpanID,
			&row.ParentSpanID,
			&row.ServiceName,
			&row.SpanName,
			&ts,
			&dur,
			&statusC,
			&attrs,
			&row.ResourceService,
		); err != nil {
			return nil, fmt.Errorf("clickhouse scan span row: %w", err)
		}
		row.Timestamp = ts
		row.DurationNanos = dur
		row.StatusCode = statusC
		row.SpanAttributes = attrs
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse iter span rows: %w", err)
	}
	return out, nil
}
