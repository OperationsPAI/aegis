package chaos

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aegis/platform/testutil"

	"gorm.io/gorm"
)

// fakeExecutor records call counts so tests can assert Apply is invoked
// at most once per idempotency_key.
type fakeExecutor struct {
	deriveCount  atomic.Int64
	applyCount   atomic.Int64
	destroyCount atomic.Int64
	applyErr     error
	destroyErr   error
}

func (e *fakeExecutor) Name() string { return "fake" }
func (e *fakeExecutor) SupportedCapabilities() []CapabilitySupport {
	return []CapabilitySupport{{Capability: "pod_kill", Maturity: CapStable}}
}
func (e *fakeExecutor) DeriveHandle(capability, key string, target map[string]any) (string, error) {
	e.deriveCount.Add(1)
	name, err := DeriveChaosMeshCRName("pod-kill", key)
	if err != nil {
		return "", err
	}
	ns, _ := target["namespace"].(string)
	return string(mustJSON(map[string]any{"name": name, "namespace": ns, "gvr": "fake"})), nil
}
func (e *fakeExecutor) Apply(ctx context.Context, capability, handle string, target, params map[string]any) error {
	e.applyCount.Add(1)
	return e.applyErr
}
func (e *fakeExecutor) Status(ctx context.Context, handle string) (ExecState, map[string]any, error) {
	return ExecStateRunning, nil, nil
}
func (e *fakeExecutor) Destroy(ctx context.Context, handle string) error {
	e.destroyCount.Add(1)
	return e.destroyErr
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func newTestManager(t *testing.T) (*Manager, *fakeExecutor, *gorm.DB) {
	t.Helper()
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(
		&System{}, &Service{}, &ImportLock{}, &Capability{},
		&Point{}, &ExecutorRecord{}, &InjectionBatch{}, &Injection{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := SeedCapabilities(db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	exec := &fakeExecutor{}
	return NewManager(db, exec), exec, db
}

func seedSystemAndPoint(t *testing.T, db *gorm.DB) (sysName, pointID string) {
	t.Helper()
	now := time.Now().UTC()
	sys := System{
		Name: "ts", NsPattern: "ts*", AppLabelKey: "app",
		Enabled: true, MaxConcurrentInjections: 5,
	}
	if err := db.Create(&sys).Error; err != nil {
		t.Fatalf("create system: %v", err)
	}
	svc := Service{
		SystemName: "ts", Name: "frontend", Instance: "default",
		ChartVersion: "v1.0.0", Status: ServiceActive,
		DiscoveredAt: now, LastSeenAt: now,
	}
	if err := db.Create(&svc).Error; err != nil {
		t.Fatalf("create svc: %v", err)
	}
	target := map[string]any{"namespace": "ts", "app": "frontend"}
	id, err := ServiceBoundPointID(PointIdentity{
		System: "ts", Service: "frontend", Instance: "default",
		ChartVersion: "v1.0.0", Capability: "pod_kill", Target: target,
	})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	p := Point{
		ID: id, SystemName: "ts", ServiceID: &svc.ID,
		CapabilityName: "pod_kill", Target: JSONMap(target),
		Source: "test", Status: PointActive,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create point: %v", err)
	}
	return "ts", id
}

// TestCreateInjection_HandlePersistedBeforeApply asserts ADR-0004:
// executor_handle is written on the same row Create as the rest of the
// injection. Even if Apply errors (simulated crash), the row keeps the
// deterministic handle so a restart's reconciler can resume via a plain
// Status(handle) call.
func TestCreateInjection_HandlePersistedBeforeApply(t *testing.T) {
	mgr, exec, db := newTestManager(t)
	_, pointID := seedSystemAndPoint(t, db)
	exec.applyErr = errors.New("simulated crash mid-Apply")

	inj, err := mgr.CreateInjection(t.Context(), CreateInjectionInput{
		PointID: pointID, Params: map[string]any{"duration_s": 30},
		IdempotencyKey: "key-crash-1",
	})
	if err == nil {
		t.Fatalf("expected Apply error to bubble")
	}
	if inj == nil || inj.ExecutorHandle == "" {
		t.Fatalf("handle must be populated even on Apply failure; got %+v", inj)
	}

	var stored Injection
	if err := db.Where("idempotency_key = ?", "key-crash-1").Take(&stored).Error; err != nil {
		t.Fatalf("row missing after Apply failure: %v", err)
	}
	if stored.ExecutorHandle == "" {
		t.Fatalf("DB row's executor_handle is empty — ADR-0004 violation")
	}

	wantName, _ := DeriveChaosMeshCRName("pod-kill", "key-crash-1")
	if !strings.Contains(stored.ExecutorHandle, wantName) {
		t.Fatalf("handle %q does not contain deterministic CR name %q",
			stored.ExecutorHandle, wantName)
	}
	if stored.Status != StatusFailed {
		t.Fatalf("status: want failed, got %q", stored.Status)
	}
}

// TestCreateInjection_IdempotentReplay: same idempotency_key twice → one
// row, exactly one Apply call.
func TestCreateInjection_IdempotentReplay(t *testing.T) {
	mgr, exec, db := newTestManager(t)
	_, pointID := seedSystemAndPoint(t, db)

	first, err := mgr.CreateInjection(t.Context(), CreateInjectionInput{
		PointID: pointID, IdempotencyKey: "key-replay",
		Params: map[string]any{"duration_s": 30},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := mgr.CreateInjection(t.Context(), CreateInjectionInput{
		PointID: pointID, IdempotencyKey: "key-replay",
		Params: map[string]any{"duration_s": 30},
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("idempotent replay must return same id; got %q vs %q", first.ID, second.ID)
	}
	if got := exec.applyCount.Load(); got != 1 {
		t.Fatalf("Apply called %d times; expected exactly 1", got)
	}

	var count int64
	if err := db.Model(&Injection{}).Where("idempotency_key = ?", "key-replay").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

func TestDeleteInjection_Idempotent(t *testing.T) {
	mgr, exec, db := newTestManager(t)
	_, pointID := seedSystemAndPoint(t, db)
	inj, err := mgr.CreateInjection(t.Context(), CreateInjectionInput{
		PointID: pointID, IdempotencyKey: "key-del",
		Params: map[string]any{"duration_s": 30},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := mgr.DeleteInjection(t.Context(), inj.ID)
	if err != nil {
		t.Fatalf("delete 1: %v", err)
	}
	if first.Status != StatusCancelled {
		t.Fatalf("status after delete: want cancelled, got %q", first.Status)
	}
	if first.DestroyedAt == nil {
		t.Fatalf("destroyed_at must be set on successful Destroy")
	}
	firstDestroyedAt := *first.DestroyedAt
	if exec.destroyCount.Load() != 1 {
		t.Fatalf("Destroy expected 1 call, got %d", exec.destroyCount.Load())
	}

	second, err := mgr.DeleteInjection(t.Context(), inj.ID)
	if err != nil {
		t.Fatalf("delete 2: %v", err)
	}
	if second.Status != StatusCancelled {
		t.Fatalf("second delete must keep cancelled, got %q", second.Status)
	}
	if second.DestroyedAt == nil || !second.DestroyedAt.Equal(firstDestroyedAt) {
		t.Fatalf("destroyed_at should be stable across replays; first=%v second=%v",
			firstDestroyedAt, second.DestroyedAt)
	}
	if exec.destroyCount.Load() != 1 {
		t.Fatalf("second DELETE must not re-Destroy; got %d total calls", exec.destroyCount.Load())
	}
	if second.DestroyError != "" {
		t.Fatalf("destroy_error must remain empty after success; got %q", second.DestroyError)
	}

	var inDB Injection
	if err := db.Where("id = ?", inj.ID).Take(&inDB).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if inDB.Status != StatusCancelled {
		t.Fatalf("db row status: %q", inDB.Status)
	}
}
