package trace

import (
	"regexp"
	"testing"
	"time"

	"aegis/consts"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newCancelService(t *testing.T) (*Service, sqlmock.Sqlmock, func()) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	require.NoError(t, err)

	// nil redis + nil k8s — CancelTrace tolerates both, so the unit test
	// exercises only the DB/state-machine logic.
	svc := NewService(NewRepository(db), nil, nil)
	return svc, mock, func() { _ = sqlDB.Close() }
}

func expectGetTrace(mock sqlmock.Sqlmock, traceID string, state consts.TraceState) {
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT * FROM `traces` WHERE id = ? AND status != ? ORDER BY `traces`.`id` LIMIT ?")).
		WithArgs(traceID, consts.CommonDeleted, 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "type", "last_event", "start_time", "end_time", "group_id",
			"project_id", "leaf_num", "state", "status", "created_at", "updated_at",
		}).AddRow(traceID, consts.TraceTypeFullPipeline, "", now, nil,
			"", 0, 1, state, consts.CommonEnabled, now, now))
	// Preload Tasks returns empty set — no tasks in fixture.
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT * FROM `tasks` WHERE `tasks`.`trace_id` = ? ORDER BY level ASC, sequence ASC")).
		WithArgs(traceID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

func TestCancelTrace_NotFound(t *testing.T) {
	svc, mock, cleanup := newCancelService(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT * FROM `traces` WHERE id = ? AND status != ? ORDER BY `traces`.`id` LIMIT ?")).
		WithArgs("missing", consts.CommonDeleted, 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := svc.CancelTrace(t.Context(), "missing")
	require.Error(t, err)
	require.ErrorIs(t, err, consts.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCancelTrace_AlreadyTerminal(t *testing.T) {
	svc, mock, cleanup := newCancelService(t)
	defer cleanup()

	const traceID = "trace-1"
	expectGetTrace(mock, traceID, consts.TraceCompleted)

	resp, err := svc.CancelTrace(t.Context(), traceID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "Completed", resp.State)
	require.Contains(t, resp.Message, "already terminal")
	require.Empty(t, resp.CancelledTasks)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCancelTrace_RunningMarksCancelled(t *testing.T) {
	svc, mock, cleanup := newCancelService(t)
	defer cleanup()

	const traceID = "trace-2"
	expectGetTrace(mock, traceID, consts.TraceRunning)

	// ListInFlightTaskIDsByTrace
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT `id` FROM `tasks` WHERE trace_id = ? AND status != ? AND state IN (?,?,?)")).
		WithArgs(traceID, consts.CommonDeleted,
			consts.TaskPending, consts.TaskRescheduled, consts.TaskRunning).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	// MarkTraceCancelled transaction: update trace + update tasks.
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE `traces` SET `end_time`=?,`last_event`=?,`state`=?,`updated_at`=? WHERE id = ? AND status != ?")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE `tasks` SET `state`=?,`updated_at`=? WHERE trace_id = ? AND status != ? AND state IN (?,?,?)")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	resp, err := svc.CancelTrace(t.Context(), traceID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "Cancelled", resp.State)
	require.Contains(t, resp.Message, "cancelled trace")
	require.NoError(t, mock.ExpectationsWereMet())
}
