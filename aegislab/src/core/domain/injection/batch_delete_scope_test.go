package injection

import (
	"context"
	"errors"
	"testing"
	"time"

	"aegis/platform/authz"
	"aegis/platform/consts"
	"aegis/platform/model"
	"aegis/platform/testutil"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type recordingCascader struct {
	calls [][]int
}

func (r *recordingCascader) CascadeDeleteByInjectionIDs(_ context.Context, _ authz.CallerScope, ids []int) error {
	cp := append([]int(nil), ids...)
	r.calls = append(r.calls, cp)
	return nil
}

func setupBatchDeleteScopeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	for _, tbl := range []any{
		&model.Project{},
		&model.Trace{},
		&model.Task{},
		&model.ContainerVersion{},
		&model.Label{},
		&model.FaultInjection{},
		&model.FaultInjectionLabel{},
	} {
		if err := db.Migrator().CreateTable(tbl); err != nil {
			t.Fatalf("create table %T: %v", tbl, err)
		}
	}
	return db
}

// TestBatchDeleteRejectsCrossProjectIDs verifies that a scoped (non-admin)
// caller asking to delete a mixed-project set of injections fails the
// whole batch — not partial delete, not silent drop. The Cascader must not
// be called.
func TestBatchDeleteRejectsCrossProjectIDs(t *testing.T) {
	db := setupBatchDeleteScopeDB(t)

	now := time.Now()
	require.NoError(t, db.Create(&model.Trace{ID: "tr1", ProjectID: 1, StartTime: now, Status: consts.CommonEnabled}).Error)
	require.NoError(t, db.Create(&model.Trace{ID: "tr2", ProjectID: 2, StartTime: now, Status: consts.CommonEnabled}).Error)
	taskID1, taskID2 := "task1", "task2"
	require.NoError(t, db.Create(&model.Task{ID: taskID1, TraceID: "tr1", Status: consts.CommonEnabled}).Error)
	require.NoError(t, db.Create(&model.Task{ID: taskID2, TraceID: "tr2", Status: consts.CommonEnabled}).Error)

	injInScope := &model.FaultInjection{Name: "inj-p1", TaskID: &taskID1, EngineConfig: "{}", Status: consts.CommonEnabled}
	injOutScope := &model.FaultInjection{Name: "inj-p2", TaskID: &taskID2, EngineConfig: "{}", Status: consts.CommonEnabled}
	require.NoError(t, db.Create(injInScope).Error)
	require.NoError(t, db.Create(injOutScope).Error)

	cascader := &recordingCascader{}
	svc := &Service{repo: NewRepository(db), executionCascade: cascader}

	scope := authz.CallerScope{UserID: 7, VisibleProjects: []int64{1}}
	err := svc.BatchDelete(t.Context(), scope, &BatchDeleteInjectionReq{
		IDs: []int{injInScope.ID, injOutScope.ID},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, consts.ErrNotFound), "expected wrapped ErrNotFound, got %v", err)
	require.Empty(t, cascader.calls, "cascader must not run when scope rejects the batch")

	var inScopeAfter, outScopeAfter model.FaultInjection
	require.NoError(t, db.First(&inScopeAfter, injInScope.ID).Error)
	require.NoError(t, db.First(&outScopeAfter, injOutScope.ID).Error)
	require.Equal(t, consts.CommonEnabled, inScopeAfter.Status, "in-scope injection must not be partially deleted")
	require.Equal(t, consts.CommonEnabled, outScopeAfter.Status)
}

// TestBatchDeleteAcceptsInScopeIDs is the happy-path counterpart: scoped
// caller with ids that all live in VisibleProjects → cascader runs and
// injections are soft-deleted.
func TestBatchDeleteAcceptsInScopeIDs(t *testing.T) {
	db := setupBatchDeleteScopeDB(t)

	now := time.Now()
	require.NoError(t, db.Create(&model.Trace{ID: "tr1", ProjectID: 1, StartTime: now, Status: consts.CommonEnabled}).Error)
	taskID1 := "task1"
	require.NoError(t, db.Create(&model.Task{ID: taskID1, TraceID: "tr1", Status: consts.CommonEnabled}).Error)
	inj := &model.FaultInjection{Name: "inj-p1", TaskID: &taskID1, EngineConfig: "{}", Status: consts.CommonEnabled}
	require.NoError(t, db.Create(inj).Error)

	cascader := &recordingCascader{}
	svc := &Service{repo: NewRepository(db), executionCascade: cascader}

	scope := authz.CallerScope{UserID: 7, VisibleProjects: []int64{1}}
	require.NoError(t, svc.BatchDelete(t.Context(), scope, &BatchDeleteInjectionReq{IDs: []int{inj.ID}}))
	require.Len(t, cascader.calls, 1)
	require.Equal(t, []int{inj.ID}, cascader.calls[0])

	var after model.FaultInjection
	require.NoError(t, db.First(&after, inj.ID).Error)
	require.Equal(t, consts.CommonDeleted, after.Status)
}
