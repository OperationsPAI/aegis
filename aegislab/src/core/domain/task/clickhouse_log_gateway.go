package task

import (
	"context"
	"strings"
	"time"

	chinfra "aegis/platform/clickhouse"
	"aegis/platform/consts"
	"aegis/platform/dto"
)

// ClickHouseLogGateway adapts the platform/clickhouse LogReader into the
// dto.LogEntry shape the task domain (streaming, polling, historical
// fetch) expects. Replaces the prior LokiGateway one-for-one: the inputs
// (taskID, start) are identical and the output keeps the same per-entry
// metadata so WebSocket / poll responses are byte-identical for the
// frontend.
type ClickHouseLogGateway struct {
	reader chinfra.LogReader
}

func NewClickHouseLogGateway(reader chinfra.LogReader) *ClickHouseLogGateway {
	return &ClickHouseLogGateway{reader: reader}
}

// QueryJobLogs returns every aegislab log line emitted for the given
// task between `start` and now. End=now (rather than start+1h like the
// old Loki path) is deliberate: the streaming and historical-fetch
// callers want the full available history, and the ClickHouse predicate
// keeps the scan cheap because the per-task LogAttributes index narrows
// the partition well before the timestamp filter runs.
func (g *ClickHouseLogGateway) QueryJobLogs(ctx context.Context, taskID string, start time.Time) ([]dto.LogEntry, error) {
	entries, err := g.reader.QueryJobLogs(ctx, taskID, chinfra.LogQueryOpts{
		Start: start,
		End:   time.Now(),
	})
	if err != nil {
		return nil, err
	}

	out := make([]dto.LogEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, dto.LogEntry{
			Timestamp: e.Timestamp,
			Line:      e.Body,
			TaskID:    taskID,
			JobID:     e.Attributes["job_id"],
			TraceID:   firstNonEmpty(e.Attributes["trace_id"], e.TraceID),
			Level:     consts.LogLevel(strings.ToLower(e.SeverityText)),
		})
	}
	return out, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
