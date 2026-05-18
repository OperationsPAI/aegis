package task

import (
	"context"
	"regexp"
	"testing"
	"time"

	chinfra "aegis/platform/clickhouse"
	"aegis/platform/consts"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// fakeLogReader is a deterministic stand-in for the ClickHouse log reader
// the task domain depends on. Tests inject canned entries instead of
// running ClickHouse — the gateway-level translation (otel.LogEntry →
// dto.LogEntry) is the only behaviour we want to exercise here.
type fakeLogReader struct {
	entries []chinfra.LogEntry
	err     error
}

func (f *fakeLogReader) QueryJobLogs(_ context.Context, _ string, _ chinfra.LogQueryOpts) ([]chinfra.LogEntry, error) {
	return f.entries, f.err
}

func (f *fakeLogReader) QueryLogHistogram(_ context.Context, _ string, _ chinfra.LogQueryOpts, _ int) ([]chinfra.HistogramBucket, error) {
	return nil, nil
}

func newTaskService(t *testing.T, gateway *ClickHouseLogGateway) (*Service, sqlmock.Sqlmock, func()) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	require.NoError(t, err)

	if gateway == nil {
		gateway = NewClickHouseLogGateway(&fakeLogReader{})
	}

	service := NewService(NewRepository(db), NewTaskLogService(NewRepository(db), nil, gateway), gateway, nil, nil)
	return service, mock, func() {
		_ = sqlDB.Close()
	}
}

func TestTaskServiceListSuccess(t *testing.T) {
	service, mock, cleanup := newTaskService(t, nil)
	defer cleanup()

	now := time.Now()
	req := &ListTaskReq{State: "Pending"}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `tasks` WHERE tasks.state = ?")).
		WithArgs(consts.TaskPending).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tasks` WHERE tasks.state = ? ORDER BY tasks.created_at DESC LIMIT ?")).
		WithArgs(consts.TaskPending, 20).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "type", "immediate", "execute_time", "cron_expr", "payload", "trace_id", "parent_task_id",
			"level", "sequence", "state", "status", "created_at", "updated_at",
		}).AddRow("task-1", consts.TaskTypeRunAlgorithm, true, 0, "", "{}", "trace-1", nil, 0, 0, consts.TaskPending, consts.CommonEnabled, now, now))

	resp, err := service.List(t.Context(), req)

	require.NoError(t, err)
	require.Len(t, resp.Items, 1)
	require.Equal(t, "task-1", resp.Items[0].ID)
	require.Equal(t, consts.GetTaskStateName(consts.TaskPending), resp.Items[0].State)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTaskServiceQueryHistoricalLogsSuccess(t *testing.T) {
	ts1 := time.Unix(1710000000, 0).UTC()
	ts2 := time.Unix(1710000001, 0).UTC()
	fake := &fakeLogReader{
		entries: []chinfra.LogEntry{
			{
				Timestamp:    ts1,
				SeverityText: "INFO",
				Body:         "first log",
				TraceID:      "trace-1",
				Attributes:   map[string]string{"task_id": "task-1", "job_id": "job-1"},
			},
			{
				Timestamp:    ts2,
				SeverityText: "INFO",
				Body:         "second log",
				TraceID:      "trace-1",
				Attributes:   map[string]string{"task_id": "task-1", "job_id": "job-1"},
			},
		},
	}
	gateway := NewClickHouseLogGateway(fake)

	logs, err := gateway.QueryJobLogs(context.Background(), "task-1", time.Unix(1710000000, 0))
	require.NoError(t, err)
	require.Len(t, logs, 2)
	require.Equal(t, "first log", logs[0].Line)
	require.Equal(t, "second log", logs[1].Line)
	require.Equal(t, "trace-1", logs[0].TraceID)
	require.Equal(t, "job-1", logs[0].JobID)
	require.Equal(t, consts.LogLevel("info"), logs[0].Level)
}
