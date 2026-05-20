package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aegis/platform/dto"
	"aegis/platform/model"
	"aegis/platform/testutil"

	chaoshooks "aegis/crud/hooks/chaos"

	"github.com/gin-gonic/gin"
)

// TestDispatcherUniquenessGateCollides pins the §11 step 5b coexistence
// requirement: when both the legacy CRD watcher and the chaos-service
// webhook receiver race for the same conceptual injection, exactly one
// BuildDatapack task is submitted. The shared key is (task_id, kind).
//
// This is the actual bug the task description warned about — pre-fix, the
// CRD path bypassed the gate entirely and the two paths could not collide.
func TestDispatcherUniquenessGateCollides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&chaoshooks.HookSubmission{}, &model.FaultInjection{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var submitted int32
	submit := func(ctx context.Context, _ *dto.UnifiedTask) error {
		atomic.AddInt32(&submitted, 1)
		return nil
	}
	h := chaoshooks.NewHandlerWithSubmitter(db, submit)
	r := gin.New()
	r.POST("/api/v1/hooks/chaos", h.Singleton)

	const taskID = "task-shared-XYZ"
	const isHybrid = false
	terminal := "succeeded"

	body := chaoshooks.SingletonWebhook{
		InjectionID:    "01CHAOSULID-WEBHOOK",
		IdempotencyKey: "idem-1",
		Status:         terminal,
		StartedAt:      time.Now().Add(-time.Minute).UTC(),
		FinishedAt:     time.Now().UTC(),
		CallerMetadata: chaoshooks.CallerMetadata{
			TaskID:    taskID,
			TraceID:   "trace-1",
			ProjectID: 1,
			Benchmark: &dto.ContainerVersionItem{ID: 7, Name: "bench"},
			Datapack:  &dto.InjectionItem{ID: 0, Name: taskID, PreDuration: 60},
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Path A — chaos-service webhook receiver (Singleton handler).
	go func() {
		defer wg.Done()
		buf, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/hooks/chaos", bytes.NewReader(buf))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("webhook: want 200 got %d body=%s", w.Code, w.Body.String())
		}
	}()

	// Path B — legacy CRD watcher: claims the same (task_id, kind) gate.
	go func() {
		defer wg.Done()
		claimed, err := chaoshooks.ClaimBuildDatapackGate(context.Background(), db, taskID, isHybrid, terminal)
		if err != nil {
			t.Errorf("CRD claim: %v", err)
			return
		}
		if claimed {
			// Production CRD path would submit BuildDatapack here; mirror
			// that by bumping the same counter the webhook fireOnce drives.
			atomic.AddInt32(&submitted, 1)
		}
	}()

	wg.Wait()

	if got := atomic.LoadInt32(&submitted); got != 1 {
		t.Fatalf("uniqueness gate collision broken: want exactly 1 BuildDatapack submission, got %d", got)
	}

	// One row in the gate table, keyed on task_id (NOT on the chaos ULID).
	var rows []chaoshooks.HookSubmission
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("query gate rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 gate row, got %d (%+v)", len(rows), rows)
	}
	if rows[0].ID != taskID {
		t.Fatalf("gate row keyed on wrong id: want %q got %q", taskID, rows[0].ID)
	}
}

// TestDispatchPathDefaultsToChaosMeshDirect pins the default-branch behavior
// for an unset etcd flag so a fresh cluster boots on the legacy path.
func TestDispatchPathDefaultsToChaosMeshDirect(t *testing.T) {
	if got := executorAuthoritative("any-system"); got != executorPathChaosMeshDirect {
		t.Fatalf("unset flag: want %q got %q", executorPathChaosMeshDirect, got)
	}
}

