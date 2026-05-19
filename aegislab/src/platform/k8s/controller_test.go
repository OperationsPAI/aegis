package k8s

import (
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// TestJobUpdateFunc_FailedConditionTransitionEnqueuesDelete covers the
// orchestrator Job cleanup gap: a Job transitioning from "no terminal
// condition" to "JobFailed=True" must trigger HandleJobFailed AND enqueue a
// DeleteJob item so leaked Failed pods don't accumulate in the exp ns.
// Pre-fix the Failed branch only invoked the callback and never enqueued.
func TestJobUpdateFunc_FailedConditionTransitionEnqueuesDelete(t *testing.T) {
	cb := &failedCountingCallback{}
	c := &Controller{
		callback: cb,
		queue: workqueue.NewTypedRateLimitingQueue(
			workqueue.DefaultTypedControllerRateLimiter[QueueItem](),
		),
	}
	defer c.queue.ShutDown()

	handlers := c.genJobEventHandlerFuncs()

	oldJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "j1", Namespace: "exp"},
		Status:     batchv1.JobStatus{},
	}
	newJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "j1", Namespace: "exp"},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
			},
		},
	}

	handlers.UpdateFunc(oldJob, newJob)

	if cb.failed != 1 {
		t.Fatalf("HandleJobFailed call count = %d, want 1", cb.failed)
	}
	if c.queue.Len() != 1 {
		t.Fatalf("queue length = %d, want 1 DeleteJob enqueued", c.queue.Len())
	}
	item, _ := c.queue.Get()
	if item.Type != DeleteJob || item.Namespace != "exp" || item.Name != "j1" {
		t.Fatalf("got queue item %+v, want DeleteJob exp/j1", item)
	}

	// Resync replay: same terminal condition on both sides — must NOT re-fire.
	handlers.UpdateFunc(newJob, newJob)
	if cb.failed != 1 {
		t.Fatalf("resync re-fired HandleJobFailed, count = %d, want 1", cb.failed)
	}
	if c.queue.Len() != 0 {
		t.Fatalf("resync enqueued duplicate DeleteJob, queue len = %d", c.queue.Len())
	}
}

type failedCountingCallback struct {
	failed    int
	succeeded int
	added     int
}

func (f *failedCountingCallback) HandleCRDAdd(string, map[string]string, map[string]string) {}
func (f *failedCountingCallback) HandleCRDDelete(string, map[string]string, map[string]string) {
}
func (f *failedCountingCallback) HandleCRDFailed(string, map[string]string, map[string]string, string) {
}
func (f *failedCountingCallback) HandleCRDSucceeded(string, string, string, time.Time, time.Time, map[string]string, map[string]string) {
}
func (f *failedCountingCallback) HandleJobAdd(string, map[string]string, map[string]string) {
	f.added++
}
func (f *failedCountingCallback) HandleJobFailed(*batchv1.Job, map[string]string, map[string]string) {
	f.failed++
}
func (f *failedCountingCallback) HandleJobSucceeded(*batchv1.Job, map[string]string, map[string]string) {
	f.succeeded++
}

// TestController_EnsureNamespaceActive covers issue #194: the controller's
// in-memory activeNamespaces set was diverging from the lock store, silently
// dropping CRD AddFunc events for namespaces that had been marked inactive
// (typically after a fault.injection.failed cleanup). The fix wires
// monitor.AcquireLock to call EnsureNamespaceActive on every successful
// acquire so a fresh trace's CRD events are no longer filtered.
//
// The table covers four states the controller can be in for a given namespace:
//   - never seen
//   - previously active and still active
//   - previously active, then deactivated by RemoveNamespaceInformers
//     (this is the bug case — pre-fix, CRD events stayed dropped until a
//     worker pod restart; post-fix, EnsureNamespaceActive flips it back)
//   - the new-namespace lazy-load case (informers already created by an
//     earlier AddNamespaceInformers, then deactivated, then a new trace
//     re-acquires the lock)
func TestController_EnsureNamespaceActive(t *testing.T) {
	const ns = "sockshop3"

	tests := []struct {
		name           string
		setup          func(c *Controller)
		wantActiveBefore bool
		wantActiveAfter  bool
	}{
		{
			name: "already_active_stays_active",
			setup: func(c *Controller) {
				// Simulate prior AddNamespaceInformers having created the
				// per-ns informer map and marked the ns active.
				c.crdInformers[ns] = map[schema.GroupVersionResource]cache.SharedIndexInformer{}
				c.activeNamespaces[ns] = true
			},
			wantActiveBefore: true,
			wantActiveAfter:  true,
		},
		{
			name: "deactivated_then_reactivated_clears_filter_bug194",
			setup: func(c *Controller) {
				// Existing informer + the deactivated state that previously
				// caused "Ignoring CRD add event for inactive namespace".
				c.crdInformers[ns] = map[schema.GroupVersionResource]cache.SharedIndexInformer{}
				c.activeNamespaces[ns] = true
				c.RemoveNamespaceInformers([]string{ns})
			},
			wantActiveBefore: false,
			wantActiveAfter:  true,
		},
		{
			name: "previously_unknown_then_deactivated_then_reactivated",
			setup: func(c *Controller) {
				// Edge case: RemoveNamespaceInformers can stamp an ns
				// inactive even if no informer was ever added (the deactivate
				// path doesn't gate on existence). EnsureNamespaceActive must
				// still flip it back so CRD events flow.
				c.RemoveNamespaceInformers([]string{ns})
				// Pretend an informer was created on first add so
				// EnsureNamespaceActive's wrapped AddNamespaceInformers takes
				// the short-circuit branch (no real k8s client available in
				// a unit test).
				c.crdInformers[ns] = map[schema.GroupVersionResource]cache.SharedIndexInformer{}
			},
			wantActiveBefore: false,
			wantActiveAfter:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Controller{
				crdInformers:     make(map[string]map[schema.GroupVersionResource]cache.SharedIndexInformer),
				activeNamespaces: make(map[string]bool),
			}
			tc.setup(c)

			if got := c.isNamespaceActive(ns); got != tc.wantActiveBefore {
				t.Fatalf("pre-condition: isNamespaceActive(%q)=%v, want %v", ns, got, tc.wantActiveBefore)
			}

			if err := c.EnsureNamespaceActive(ns); err != nil {
				t.Fatalf("EnsureNamespaceActive(%q) returned error: %v", ns, err)
			}

			if got := c.isNamespaceActive(ns); got != tc.wantActiveAfter {
				t.Fatalf("post-condition: isNamespaceActive(%q)=%v, want %v", ns, got, tc.wantActiveAfter)
			}
		})
	}
}

// TestController_EnsureNamespaceActive_NilSafe verifies the helper is safe to
// call on a nil receiver and with an empty namespace; both are no-ops. This
// matters because RegisterConsumerHandlers wires the controller as the
// monitor's NamespaceActivator only when both are non-nil, and AcquireLock
// itself nil-checks the activator before calling — but defense in depth on
// the controller side prevents a future caller from panicking on a misconfig.
func TestController_EnsureNamespaceActive_NilSafe(t *testing.T) {
	var c *Controller
	if err := c.EnsureNamespaceActive("anything"); err != nil {
		t.Fatalf("nil receiver should be a no-op, got %v", err)
	}

	c = &Controller{
		crdInformers:     make(map[string]map[schema.GroupVersionResource]cache.SharedIndexInformer),
		activeNamespaces: make(map[string]bool),
	}
	if err := c.EnsureNamespaceActive(""); err != nil {
		t.Fatalf("empty namespace should be a no-op, got %v", err)
	}
	if c.isNamespaceActive("") {
		t.Fatalf("empty namespace must not be marked active")
	}
}
