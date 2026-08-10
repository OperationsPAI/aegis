package chaos

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"aegis/platform/testutil"
)

// TestWebhookFire_PayloadAndRetry exercises the three webhook behaviours
// the live-cluster A5/A6/A7 assertions can't easily isolate: payload
// shape (caller_metadata logical-equivalence round-trip via JSONMap
// re-marshal), retry-on-5xx, and final-failure surfaced into webhook_error.
func TestWebhookFire_PayloadAndRetry(t *testing.T) {
	db := testutil.NewSQLiteGormDB(t)
	if err := db.AutoMigrate(&Injection{}); err != nil {
		t.Fatal(err)
	}

	// Make backoff effectively zero for the test. Length matches the
	// 4 inter-attempt sleeps (webhookMaxAttempts - 1).
	origBackoff := webhookBackoff
	webhookBackoff = []time.Duration{0, 0, 0, 0}
	t.Cleanup(func() { webhookBackoff = origBackoff })

	t.Run("happy path + caller_metadata logical-equivalence", func(t *testing.T) {
		var gotBody []byte
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		inj := Injection{
			ID: "inj1", IdempotencyKey: "k1", Status: StatusSucceeded,
			Params:         JSONMap{},
			CallerMetadata: JSONMap{"task_id": "t-1", "nested": map[string]any{"x": 1.0}},
			Ts:             time.Now().UTC(),
		}
		if err := db.Create(&inj).Error; err != nil {
			t.Fatal(err)
		}
		w := NewWebhookSender(srv.Client(), srv.URL, db, nil)
		if err := w.Fire(t.Context(), &inj); err != nil {
			t.Fatalf("Fire: %v", err)
		}
		if hits.Load() != 1 {
			t.Errorf("hits = %d, want 1", hits.Load())
		}

		var got map[string]json.RawMessage
		if err := json.Unmarshal(gotBody, &got); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		var meta map[string]any
		_ = json.Unmarshal(got["caller_metadata"], &meta)
		if meta["task_id"] != "t-1" {
			t.Errorf("caller_metadata.task_id = %v", meta["task_id"])
		}

		var reloaded Injection
		_ = db.Where("id = ?", "inj1").Take(&reloaded).Error
		if reloaded.WebhookAttemptedAt == nil {
			t.Errorf("webhook_attempted_at not set")
		}
		if reloaded.WebhookError != "" {
			t.Errorf("webhook_error = %q, want empty", reloaded.WebhookError)
		}
	})

	t.Run("retry then success", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := hits.Add(1)
			if n < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		inj := Injection{ID: "inj2", IdempotencyKey: "k2", Status: StatusSucceeded, Params: JSONMap{}, Ts: time.Now().UTC()}
		_ = db.Create(&inj).Error
		w := NewWebhookSender(srv.Client(), srv.URL, db, nil)
		if err := w.Fire(t.Context(), &inj); err != nil {
			t.Fatalf("Fire: %v", err)
		}
		if got := hits.Load(); got != 3 {
			t.Errorf("hits = %d, want 3 (two 500s then 200)", got)
		}
	})

	t.Run("total failure surfaces in webhook_error", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		inj := Injection{ID: "inj3", IdempotencyKey: "k3", Status: StatusFailed, Params: JSONMap{}, Ts: time.Now().UTC()}
		_ = db.Create(&inj).Error
		w := NewWebhookSender(srv.Client(), srv.URL, db, nil)
		if err := w.Fire(t.Context(), &inj); err == nil {
			t.Errorf("Fire should return final error")
		}
		if got := hits.Load(); got != int32(webhookMaxAttempts) {
			t.Errorf("hits = %d, want %d", got, webhookMaxAttempts)
		}
		var reloaded Injection
		_ = db.Where("id = ?", "inj3").Take(&reloaded).Error
		if reloaded.WebhookError == "" {
			t.Errorf("webhook_error empty after total failure")
		}
	})

	t.Run("disabled (empty URL) returns errWebhookDisabled and no DB write", func(t *testing.T) {
		inj := Injection{ID: "inj4", IdempotencyKey: "k4", Status: StatusSucceeded, Params: JSONMap{}, Ts: time.Now().UTC()}
		_ = db.Create(&inj).Error
		w := NewWebhookSender(nil, "", db, nil)
		err := w.Fire(t.Context(), &inj)
		if err != errWebhookDisabled {
			t.Errorf("err = %v, want errWebhookDisabled", err)
		}
		var reloaded Injection
		_ = db.Where("id = ?", "inj4").Take(&reloaded).Error
		if reloaded.WebhookAttemptedAt != nil {
			t.Errorf("disabled webhook must not record an attempt")
		}
	})
}
