package chaos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	guidedcli "aegis/platform/chaos"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func mountHandler(t *testing.T, mgr *Manager) *gin.Engine {
	t.Helper()
	r := gin.New()
	h := &Handler{Mgr: mgr}
	r.POST("/v1beta/guided/resolve", h.GuidedResolve)
	r.POST("/v1beta/guided/apply-next", h.GuidedApplyNext)
	r.GET("/v1beta/systems/:sys/candidates", h.ListSystemCandidates)
	r.DELETE("/v1beta/injections/by-task/:taskID", h.DeleteInjectionByTask)
	return r
}

func stubResolve(t *testing.T, fn func(ctx context.Context, cfg guidedcli.GuidedConfig) (*guidedcli.GuidedResponse, error)) {
	t.Helper()
	testGuidedResolve = fn
	t.Cleanup(func() { testGuidedResolve = nil })
}

func stubApplyNext(t *testing.T, fn func(*guidedcli.GuidedResponse, string) (guidedcli.GuidedConfig, error)) {
	t.Helper()
	testGuidedApplyNext = fn
	t.Cleanup(func() { testGuidedApplyNext = nil })
}

func stubEnumerate(t *testing.T, fn func(ctx context.Context, system, namespace string) ([]guidedcli.GuidedConfig, error)) {
	t.Helper()
	testGuidedEnumerate = fn
	t.Cleanup(func() { testGuidedEnumerate = nil })
}

func TestGuidedResolve_HappyPath(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	stubResolve(t, func(ctx context.Context, cfg guidedcli.GuidedConfig) (*guidedcli.GuidedResponse, error) {
		return &guidedcli.GuidedResponse{
			Mode:   "guided",
			Stage:  "select_app",
			Config: cfg,
			Next: []guidedcli.FieldSpec{{
				Name: "app", Kind: "enum", Required: true,
				Options: []guidedcli.FieldOption{{Value: "frontend", Label: "frontend"}},
			}},
		}, nil
	})

	r := mountHandler(t, mgr)
	body, _ := json.Marshal(ChaosGuidedResolveReq{
		Config: guidedcli.GuidedConfig{System: "ts", Namespace: "ts"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1beta/guided/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data ChaosGuidedResolveResp `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if env.Data.Stage != "select_app" || len(env.Data.Next) != 1 {
		t.Fatalf("unexpected payload: %+v", env.Data)
	}
}

func TestGuidedResolve_BadJSON(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	r := mountHandler(t, mgr)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1beta/guided/resolve",
		bytes.NewReader([]byte("not-json")))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 on malformed body, got %d", rec.Code)
	}
}

func TestGuidedApplyNext_HappyPath(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	stubResolve(t, func(ctx context.Context, cfg guidedcli.GuidedConfig) (*guidedcli.GuidedResponse, error) {
		return &guidedcli.GuidedResponse{Config: cfg, Next: []guidedcli.FieldSpec{{Name: "app", Kind: "enum", Required: true}}}, nil
	})
	stubApplyNext(t, func(resp *guidedcli.GuidedResponse, raw string) (guidedcli.GuidedConfig, error) {
		out := resp.Config
		out.App = raw
		return out, nil
	})

	r := mountHandler(t, mgr)
	body, _ := json.Marshal(ChaosGuidedApplyNextReq{
		Current: guidedcli.GuidedConfig{System: "ts", Namespace: "ts"},
		Value:   "frontend",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1beta/guided/apply-next", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data ChaosGuidedApplyNextResp `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.Config.App != "frontend" {
		t.Fatalf("merge missed app: %+v", env.Data.Config)
	}
}

func TestGuidedApplyNext_MissingValue(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	r := mountHandler(t, mgr)
	body, _ := json.Marshal(ChaosGuidedApplyNextReq{Current: guidedcli.GuidedConfig{System: "ts"}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1beta/guided/apply-next", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 on missing value, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestListSystemCandidates_HappyPath(t *testing.T) {
	mgr, _, db := newTestManager(t)
	now := time.Now().UTC()
	if err := db.Create(&System{
		Name: "ts", NsPattern: "ts", AppLabelKey: "app",
		Enabled: true, MaxConcurrentInjections: 5,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed system: %v", err)
	}
	stubEnumerate(t, func(ctx context.Context, system, namespace string) ([]guidedcli.GuidedConfig, error) {
		if system != "ts" || namespace != "ts" {
			return nil, fmt.Errorf("unexpected args: system=%q namespace=%q", system, namespace)
		}
		return []guidedcli.GuidedConfig{
			{System: "ts", Namespace: "ts", App: "frontend", ChaosType: "PodKill"},
		}, nil
	})

	r := mountHandler(t, mgr)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1beta/systems/ts/candidates", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data ChaosSystemCandidatesResp `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.System != "ts" || len(env.Data.Candidates) != 1 || env.Data.Candidates[0].ChaosType != "PodKill" {
		t.Fatalf("unexpected payload: %+v", env.Data)
	}
}

func TestListSystemCandidates_SystemNotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	r := mountHandler(t, mgr)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1beta/systems/ghost/candidates", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown system, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteInjectionByTask_HappyPath_Singleton(t *testing.T) {
	mgr, _, db := newTestManager(t)
	_, pointID := seedSystemAndPoint(t, db)

	inj, err := mgr.CreateInjection(t.Context(), CreateInjectionInput{
		PointID:        pointID,
		Namespace:      "ns0",
		IdempotencyKey: "key-bytask-1",
		Params:         map[string]any{"duration_s": 30},
		CallerMetadata: map[string]any{"task_id": "task-1"},
	})
	if err != nil {
		t.Fatalf("create injection: %v", err)
	}

	r := mountHandler(t, mgr)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1beta/injections/by-task/task-1", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data ChaosTaskInjectionRef `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if env.Data.IsBatch {
		t.Fatalf("expected singleton, got batch ref")
	}
	if env.Data.Injection == nil || env.Data.Injection.ID != inj.ID {
		t.Fatalf("missing injection ref: %+v", env.Data)
	}

	var stored Injection
	if err := db.Where("id = ?", inj.ID).Take(&stored).Error; err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if stored.Status != StatusCancelled {
		t.Fatalf("status not cancelled: %q", stored.Status)
	}
}

// TestDeleteInjectionByTask_HappyPath_Batch covers the hybrid-batch case:
// dispatcher.go stamps the same task_id on the parent batch AND every child,
// so a singleton-table hit whose row carries batch_id != NULL must resolve
// to the BATCH destroy path — otherwise DELETE would silently cancel one
// child of a multi-fault batch instead of the whole envelope.
func TestDeleteInjectionByTask_HappyPath_Batch(t *testing.T) {
	mgr, _, db := newTestManager(t)
	_, pointID := seedSystemAndPoint(t, db)

	out, err := mgr.CreateInjectionBatch(t.Context(), CreateBatchInput{
		BatchIdempotencyKey: "batch-key-1",
		BatchCallerMetadata: map[string]any{"task_id": "task-hybrid"},
		Children: []CreateBatchChild{
			{
				PointID: pointID, Namespace: "ns0",
				IdempotencyKey: "child-1",
				Params:         map[string]any{"duration_s": 30},
				CallerMetadata: map[string]any{"task_id": "task-hybrid"},
			},
			{
				PointID: pointID, Namespace: "ns0",
				IdempotencyKey: "child-2",
				Params:         map[string]any{"duration_s": 30},
				CallerMetadata: map[string]any{"task_id": "task-hybrid"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if len(out.Children) != 2 {
		t.Fatalf("want 2 children, got %d", len(out.Children))
	}

	r := mountHandler(t, mgr)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1beta/injections/by-task/task-hybrid", nil)
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data ChaosTaskInjectionRef `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if !env.Data.IsBatch || env.Data.Batch == nil {
		t.Fatalf("hybrid batch destroy must return batch ref, got %+v", env.Data)
	}
	if env.Data.Batch.ID != out.Batch.ID {
		t.Fatalf("batch id mismatch: got %q want %q", env.Data.Batch.ID, out.Batch.ID)
	}

	var batchRow InjectionBatch
	if err := db.Where("id = ?", out.Batch.ID).Take(&batchRow).Error; err != nil {
		t.Fatalf("re-read batch: %v", err)
	}
	if batchRow.AggregatedStatus != AggCancelled {
		t.Fatalf("batch aggregated_status: want cancelled, got %q", batchRow.AggregatedStatus)
	}
}

func TestDeleteInjectionByTask_UnknownTask(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	r := mountHandler(t, mgr)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1beta/injections/by-task/ghost", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown task, got %d body=%s", rec.Code, rec.Body.String())
	}
}
