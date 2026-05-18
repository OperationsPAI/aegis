package clickhouse

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildJobLogsQueryBaseShape(t *testing.T) {
	start := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	stmt, args := buildJobLogsQuery("task-1", LogQueryOpts{
		Start: start,
		End:   end,
		Limit: 200,
	})

	require.Contains(t, stmt, "FROM otel.otel_logs")
	require.Contains(t, stmt, "ORDER BY Timestamp ASC")
	require.Contains(t, stmt, "ServiceName = ?")
	require.Contains(t, stmt, "LogAttributes['task_id'] = ?")
	require.Contains(t, stmt, "Timestamp >= ?")
	require.Contains(t, stmt, "Timestamp <= ?")
	require.Contains(t, stmt, "LIMIT ?")
	require.NotContains(t, stmt, "lower(SeverityText)")     // level filter off
	require.NotContains(t, stmt, "positionCaseInsensitive") // substring filter off

	// Args order: service, task, start, end, limit.
	require.Equal(t, []any{orchestratorServiceName, "task-1", start, end, 200}, args)
}

func TestBuildJobLogsQueryWithLevelAndSubstring(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()

	stmt, args := buildJobLogsQuery("task-2", LogQueryOpts{
		Start:     start,
		End:       end,
		Limit:     50,
		Level:     "ERROR",
		Substring: "panic",
	})

	require.Contains(t, stmt, "lower(SeverityText) = ?")
	require.Contains(t, stmt, "positionCaseInsensitive(Body, ?) > 0")

	// Args order: service, task, start, end, level (lowercased), substring, limit.
	require.Equal(t, []any{
		orchestratorServiceName, "task-2", start, end, "error", "panic", 50,
	}, args)
}

func TestBuildHistogramQueryInterpolatesBucketWidth(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	stmt, args := buildHistogramQuery("task-3", LogQueryOpts{Start: start, End: end}, 30)

	require.Contains(t, stmt, "toStartOfInterval(Timestamp, INTERVAL 30 SECOND)")
	require.Contains(t, stmt, "GROUP BY bucket, SeverityText")
	require.Equal(t, []any{orchestratorServiceName, "task-3", start, end}, args)
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

func TestBuildJobLogsQueryWhitespaceFiltersIgnored(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	stmt, _ := buildJobLogsQuery("task-x", LogQueryOpts{
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
