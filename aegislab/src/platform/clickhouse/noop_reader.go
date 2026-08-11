package clickhouse

import "context"

// NoopLogReader is a LogReader that returns empty results without
// dialling ClickHouse. Used by fx-wired boot tests that exercise the
// module graph offline; production binaries still get the real
// ClickHouse-backed reader via NewClickHouseLogReader.
type NoopLogReader struct{}

func (NoopLogReader) QueryJobLogs(_ context.Context, _ string, _ LogQueryOpts) ([]LogEntry, error) {
	return nil, nil
}

func (NoopLogReader) QueryLogHistogram(_ context.Context, _ string, _ LogQueryOpts, _ int) ([]HistogramBucket, error) {
	return nil, nil
}

func (NoopLogReader) QueryTraceLogs(_ context.Context, _ string, _ LogQueryOpts) ([]LogEntry, error) {
	return nil, nil
}

func (NoopLogReader) QueryTraceLogHistogram(_ context.Context, _ string, _ LogQueryOpts, _ int) ([]HistogramBucket, error) {
	return nil, nil
}
