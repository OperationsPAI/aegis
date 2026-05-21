package injection

import (
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/model"
	"aegis/platform/testutil"

	"github.com/stretchr/testify/require"
)

// TestListExistingEngineConfigs_TerminalTaskStateAllowsResubmit pins the R3
// behavior change: a previous FaultInjection whose owning Task has reached a
// terminal state (Completed / Error / Cancelled) must NOT count as a
// duplicate, so an operator can rerun the same regression case after it
// finishes. Tasks still in flight (Pending / Running / Rescheduled) keep
// the suppression active so concurrent submits of the same scenario still
// fail fast.
func TestListExistingEngineConfigs_TerminalTaskStateAllowsResubmit(t *testing.T) {
	db := testutil.NewSQLiteGormDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Task{},
		&model.FaultInjection{},
		&model.Label{},
	))

	repo := NewRepository(db)
	engineCfg := `[{"chaos_type":"PodKill","app":"frontend"}]`

	mkTask := func(id string, state consts.TaskState) *model.Task {
		return &model.Task{
			ID:        id,
			Type:      consts.TaskTypeRestartPedestal,
			Status:    consts.CommonEnabled,
			State:     state,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
	}
	mkInjection := func(taskID string) *model.FaultInjection {
		return &model.FaultInjection{
			Name:         "n",
			Source:       consts.DatapackSourceInjection,
			EngineConfig: engineCfg,
			TaskID:       &taskID,
			State:        consts.DatapackInjectSuccess,
			Status:       consts.CommonEnabled,
		}
	}

	t.Run("in_flight_task_blocks", func(t *testing.T) {
		require.NoError(t, db.Exec("DELETE FROM fault_injections").Error)
		require.NoError(t, db.Exec("DELETE FROM tasks").Error)
		require.NoError(t, db.Create(mkTask("t-running", consts.TaskRunning)).Error)
		require.NoError(t, db.Create(mkInjection("t-running")).Error)

		existing, err := repo.listExistingEngineConfigs([]string{engineCfg})
		require.NoError(t, err)
		require.Equal(t, []string{engineCfg}, existing, "running task must still block resubmit")
	})

	t.Run("completed_task_allows_resubmit", func(t *testing.T) {
		require.NoError(t, db.Exec("DELETE FROM fault_injections").Error)
		require.NoError(t, db.Exec("DELETE FROM tasks").Error)
		require.NoError(t, db.Create(mkTask("t-done", consts.TaskCompleted)).Error)
		require.NoError(t, db.Create(mkInjection("t-done")).Error)

		existing, err := repo.listExistingEngineConfigs([]string{engineCfg})
		require.NoError(t, err)
		require.Empty(t, existing, "completed task must not block resubmit")
	})

	t.Run("error_task_allows_resubmit", func(t *testing.T) {
		require.NoError(t, db.Exec("DELETE FROM fault_injections").Error)
		require.NoError(t, db.Exec("DELETE FROM tasks").Error)
		require.NoError(t, db.Create(mkTask("t-err", consts.TaskError)).Error)
		require.NoError(t, db.Create(mkInjection("t-err")).Error)

		existing, err := repo.listExistingEngineConfigs([]string{engineCfg})
		require.NoError(t, err)
		require.Empty(t, existing, "failed task must not block resubmit")
	})

	t.Run("cancelled_task_allows_resubmit", func(t *testing.T) {
		require.NoError(t, db.Exec("DELETE FROM fault_injections").Error)
		require.NoError(t, db.Exec("DELETE FROM tasks").Error)
		require.NoError(t, db.Create(mkTask("t-canc", consts.TaskCancelled)).Error)
		require.NoError(t, db.Create(mkInjection("t-canc")).Error)

		existing, err := repo.listExistingEngineConfigs([]string{engineCfg})
		require.NoError(t, err)
		require.Empty(t, existing, "cancelled task must not block resubmit")
	})
}
