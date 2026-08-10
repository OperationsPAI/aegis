package execution

import (
	"testing"
	"time"

	"aegis/platform/authz"
	"aegis/platform/consts"
	"aegis/platform/model"
	"aegis/platform/testutil"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupCascadeTestDB stands up a minimal sqlite schema with only the rows the
// cascade touches: traces, tasks, executions, and the execution/label join.
// Migrator().CreateTable bypasses gorm.AutoMigrate's transitive FK walk —
// otherwise gorm tries to materialize dataset_versions and container_versions
// (both declaring uniqueIndex `idx_active_version_unique`) and collides.
func setupCascadeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	tables := []any{
		&model.Trace{},
		&model.Task{},
		&model.Execution{},
		&model.ExecutionInjectionLabel{},
	}
	for _, tbl := range tables {
		if err := db.Migrator().CreateTable(tbl); err != nil {
			t.Fatalf("create table %T: %v", tbl, err)
		}
	}
	return db
}

// TestCascadeDeleteByInjectionIDsRespectsScope verifies that a scoped
// (non-admin) caller cannot collaterally delete executions whose
// trace.project_id lies outside scope.VisibleProjects, even when the
// injection ids passed in own those executions.
func TestCascadeDeleteByInjectionIDsRespectsScope(t *testing.T) {
	db := setupCascadeTestDB(t)

	// Two project worlds:
	//   project 1: trace "tr1" → task "task1" → execution exec_visible
	//   project 2: trace "tr2" → task "task2" → execution exec_hidden
	// Both executions point at datapack_id=42 (same injection id).
	now := time.Now()
	require.NoError(t, db.Create(&model.Trace{ID: "tr1", ProjectID: 1, StartTime: now, Status: consts.CommonEnabled}).Error)
	require.NoError(t, db.Create(&model.Trace{ID: "tr2", ProjectID: 2, StartTime: now, Status: consts.CommonEnabled}).Error)
	require.NoError(t, db.Create(&model.Task{ID: "task1", TraceID: "tr1", Status: consts.CommonEnabled}).Error)
	require.NoError(t, db.Create(&model.Task{ID: "task2", TraceID: "tr2", Status: consts.CommonEnabled}).Error)

	taskID1 := "task1"
	taskID2 := "task2"
	execVisible := &model.Execution{TaskID: &taskID1, DatapackID: 42, Status: consts.CommonEnabled}
	execHidden := &model.Execution{TaskID: &taskID2, DatapackID: 42, Status: consts.CommonEnabled}
	require.NoError(t, db.Create(execVisible).Error)
	require.NoError(t, db.Create(execHidden).Error)

	c := NewCascader(db)

	scope := authz.CallerScope{UserID: 7, VisibleProjects: []int64{1}}
	require.NoError(t, c.CascadeDeleteByInjectionIDs(t.Context(), scope, []int{42}))

	var visibleAfter, hiddenAfter model.Execution
	require.NoError(t, db.Unscoped().First(&visibleAfter, execVisible.ID).Error)
	require.NoError(t, db.Unscoped().First(&hiddenAfter, execHidden.ID).Error)

	require.Equal(t, consts.CommonDeleted, visibleAfter.Status, "execution in scope should be soft-deleted")
	require.Equal(t, consts.CommonEnabled, hiddenAfter.Status, "execution outside scope must NOT be touched")
}

// TestCascadeDeleteByInjectionIDsAdminTouchesAll confirms an admin scope
// deletes every execution for the given injection ids regardless of project.
func TestCascadeDeleteByInjectionIDsAdminTouchesAll(t *testing.T) {
	db := setupCascadeTestDB(t)

	now := time.Now()
	require.NoError(t, db.Create(&model.Trace{ID: "tr1", ProjectID: 1, StartTime: now, Status: consts.CommonEnabled}).Error)
	require.NoError(t, db.Create(&model.Trace{ID: "tr2", ProjectID: 2, StartTime: now, Status: consts.CommonEnabled}).Error)
	require.NoError(t, db.Create(&model.Task{ID: "task1", TraceID: "tr1", Status: consts.CommonEnabled}).Error)
	require.NoError(t, db.Create(&model.Task{ID: "task2", TraceID: "tr2", Status: consts.CommonEnabled}).Error)

	taskID1 := "task1"
	taskID2 := "task2"
	exec1 := &model.Execution{TaskID: &taskID1, DatapackID: 42, Status: consts.CommonEnabled}
	exec2 := &model.Execution{TaskID: &taskID2, DatapackID: 42, Status: consts.CommonEnabled}
	require.NoError(t, db.Create(exec1).Error)
	require.NoError(t, db.Create(exec2).Error)

	c := NewCascader(db)
	require.NoError(t, c.CascadeDeleteByInjectionIDs(t.Context(), authz.SystemScope(), []int{42}))

	var a, b model.Execution
	require.NoError(t, db.Unscoped().First(&a, exec1.ID).Error)
	require.NoError(t, db.Unscoped().First(&b, exec2.ID).Error)
	require.Equal(t, consts.CommonDeleted, a.Status)
	require.Equal(t, consts.CommonDeleted, b.Status)
}
