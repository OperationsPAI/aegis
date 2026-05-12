package consumer

import (
	"testing"
	"time"

	"aegis/platform/consts"
	"aegis/platform/model"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newK8sHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.FaultInjection{}))
	return db
}

// TestBuildFallbackInjectionItem_UsesArgsAndRow verifies the fallback
// constructed when updateInjectionTimestamp fails: ID and PreDuration come
// from the persisted row (we can still read it; only the UPDATE failed),
// while StartTime/EndTime come from the chaos-mesh callback args (those
// are the authoritative values the failed UPDATE would have written).
// Pins the issue #293 latent fix: HandleCRDSucceeded used to return early
// on this warning, dropping the BuildDatapack submit and stranding the
// trace.
func TestBuildFallbackInjectionItem_UsesArgsAndRow(t *testing.T) {
	db := newK8sHandlerTestDB(t)
	require.NoError(t, db.Create(&model.FaultInjection{
		Name:        "fi-fallback-test",
		PreDuration: 3,
		Status:      consts.CommonEnabled,
	}).Error)

	wantStart := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	wantEnd := wantStart.Add(2 * time.Minute)

	got, err := buildFallbackInjectionItem(db, "fi-fallback-test", wantStart, wantEnd)
	require.NoError(t, err)
	require.Equal(t, "fi-fallback-test", got.Name)
	require.Equal(t, 3, got.PreDuration, "PreDuration must be read from the persisted row")
	require.True(t, got.StartTime.Equal(wantStart),
		"StartTime must come from the callback args, not from the row")
	require.True(t, got.EndTime.Equal(wantEnd),
		"EndTime must come from the callback args, not from the row")
	require.NotZero(t, got.ID, "ID must be populated from the row so downstream tasks can join")
}

// TestBuildFallbackInjectionItem_SkipsDeletedRows confirms a tombstoned
// FaultInjection row is treated the same way the rest of the pipeline
// treats it (status=-1 means "deleted"), so a stale CRD callback can't
// resurrect it via the fallback path.
func TestBuildFallbackInjectionItem_SkipsDeletedRows(t *testing.T) {
	db := newK8sHandlerTestDB(t)
	require.NoError(t, db.Create(&model.FaultInjection{
		Name:        "fi-deleted",
		PreDuration: 1,
		Status:      consts.CommonDeleted,
	}).Error)

	_, err := buildFallbackInjectionItem(db, "fi-deleted", time.Now(), time.Now())
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// TestBuildFallbackInjectionItem_ReportsMissingRow makes sure the helper
// signals a real lookup failure instead of silently producing a zero-ID
// InjectionItem (which would lie to BuildDatapack about which row to
// process).
func TestBuildFallbackInjectionItem_ReportsMissingRow(t *testing.T) {
	db := newK8sHandlerTestDB(t)
	_, err := buildFallbackInjectionItem(db, "no-such-injection", time.Now(), time.Now())
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
