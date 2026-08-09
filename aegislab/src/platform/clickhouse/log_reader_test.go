package clickhouse

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildLogsQueryBaseShape(t *testing.T) {
	start := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	stmt, args := buildLogsQuery("task_id", "task-1", LogQueryOpts{
		Start: start,
		End:   end,
		Limit: 200,
	})

	require.Contains(t, stmt, "FROM otel.otel_logs")
	require.Contains(t, stmt, "ORDER BY Timestamp ASC")
	require.NotContains(t, stmt, "ServiceName = ?") // dropped — trace_id/task_id is the only scoping needed; ServiceName predicate excluded pod-stdout
	require.Contains(t, stmt, "LogAttributes['task_id'] = ?")
	require.Contains(t, stmt, "Timestamp >= ?")
	require.Contains(t, stmt, "Timestamp <= ?")
	require.Contains(t, stmt, "LIMIT ?")
	require.NotContains(t, stmt, "lower(SeverityText)")     // level filter off
	require.NotContains(t, stmt, "positionCaseInsensitive") // substring filter off

	// Args order: task, start, end, limit.
	require.Equal(t, []any{"task-1", start, end, 200}, args)
}

func TestBuildLogsQueryByTraceAttribute(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()

	stmt, args := buildLogsQuery("trace_id", "trace-abc", LogQueryOpts{
		Start: start,
		End:   end,
		Limit: 1000,
	})

	require.Contains(t, stmt, "LogAttributes['trace_id'] = ?")
	require.NotContains(t, stmt, "LogAttributes['task_id']")
	require.Equal(t, []any{"trace-abc", start, end, 1000}, args)
}

func TestBuildLogsQueryWithLevelAndSubstring(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()

	stmt, args := buildLogsQuery("task_id", "task-2", LogQueryOpts{
		Start:     start,
		End:       end,
		Limit:     50,
		Level:     "ERROR",
		Substring: "panic",
	})

	require.Contains(t, stmt, "lower(SeverityText) = ?")
	require.Contains(t, stmt, "positionCaseInsensitive(Body, ?) > 0")

	// Args order: task, start, end, level (lowercased), substring, limit.
	require.Equal(t, []any{
		"task-2", start, end, "error", "panic", 50,
	}, args)
}

func TestBuildHistogramQueryInterpolatesBucketWidth(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	stmt, args := buildHistogramQuery("task_id", "task-3", LogQueryOpts{Start: start, End: end}, 30)

	require.Contains(t, stmt, "toStartOfInterval(Timestamp, INTERVAL 30 SECOND)")
	require.Contains(t, stmt, "GROUP BY bucket, SeverityText")
	require.Contains(t, stmt, "LogAttributes['task_id'] = ?")
	require.Equal(t, []any{"task-3", start, end}, args)
}

func TestBuildHistogramQueryByTraceAttribute(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	stmt, _ := buildHistogramQuery("trace_id", "trace-xyz", LogQueryOpts{Start: start, End: end}, 60)

	require.Contains(t, stmt, "LogAttributes['trace_id'] = ?")
	require.NotContains(t, stmt, "LogAttributes['task_id']")
	require.Contains(t, stmt, "INTERVAL 60 SECOND")
}

func TestSortBucketsAscending(t *testing.T) {
	t0 := time.Now().UTC().Truncate(time.Minute)
	buckets := []HistogramBucket{
		{StartTS: t0.Add(2 * time.Minute)},
		{StartTS: t0},
		{StartTS: t0.Add(time.Minute)},
	}
	sortBuckets(buckets)
	require.True(t, buckets[0].StartTS.Equal(t0))
	require.True(t, buckets[1].StartTS.Equal(t0.Add(time.Minute)))
	require.True(t, buckets[2].StartTS.Equal(t0.Add(2*time.Minute)))
}

func TestBuildLogsQueryWhitespaceFiltersIgnored(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	stmt, _ := buildLogsQuery("task_id", "task-x", LogQueryOpts{
		Start:     start,
		End:       end,
		Limit:     10,
		Level:     "   ",
		Substring: "  ",
	})
	// Whitespace-only level / substring don't add predicates.
	require.NotContains(t, stmt, "lower(SeverityText)")
	require.False(t, strings.Contains(stmt, "positionCaseInsensitive"))
}

func TestQuoteAttrKeyRejectsHostileKeys(t *testing.T) {
	// Acceptable keys round-trip safely.
	require.Equal(t, "'task_id'", quoteAttrKey("task_id"))
	require.Equal(t, "'trace_id'", quoteAttrKey("trace_id"))
	require.Equal(t, "'k8s.namespace'", quoteAttrKey("k8s.namespace"))

	// Anything outside [a-z0-9._] panics — SQLi defense in depth.
	require.Panics(t, func() { quoteAttrKey("task_id'; DROP TABLE") })
	require.Panics(t, func() { quoteAttrKey("Task_ID") }) // uppercase blocked too
	require.Panics(t, func() { quoteAttrKey("task id") })
}
