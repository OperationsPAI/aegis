package k8s

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"aegis/platform/consts"
)

func TestClassifyChaosOrphans(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	fresh := now.Add(-30 * time.Second)

	tasks := map[string]consts.TaskState{
		"task-running":   consts.TaskRunning,
		"task-pending":   consts.TaskPending,
		"task-completed": consts.TaskCompleted,
		"task-error":     consts.TaskError,
		"task-cancelled": consts.TaskCancelled,
	}
	lookup := func(id string) (consts.TaskState, bool, error) {
		if id == "task-lookup-fails" {
			return 0, false, fmt.Errorf("db down")
		}
		s, ok := tasks[id]
		return s, ok, nil
	}

	crs := []ChaosCRRef{
		// Orphan: no task_id label at all.
		{Kind: "PodChaos", Resource: "podchaos", Namespace: "hs0", Name: "no-label",
			Labels: map[string]string{}, CreationTimestamp: old},
		// Orphan: task_id present but no row.
		{Kind: "NetworkChaos", Resource: "networkchaos", Namespace: "hs0", Name: "missing-task",
			Labels: map[string]string{consts.JobLabelTaskID: "ghost"}, CreationTimestamp: old},
		// Not orphan: task is still running.
		{Kind: "PodChaos", Resource: "podchaos", Namespace: "hs0", Name: "live-running",
			Labels: map[string]string{consts.JobLabelTaskID: "task-running"}, CreationTimestamp: old},
		// Not orphan: task is pending.
		{Kind: "PodChaos", Resource: "podchaos", Namespace: "hs0", Name: "live-pending",
			Labels: map[string]string{consts.JobLabelTaskID: "task-pending"}, CreationTimestamp: old},
		// Orphan: completed + old.
		{Kind: "HTTPChaos", Resource: "httpchaos", Namespace: "hs0", Name: "term-completed-old",
			Labels: map[string]string{consts.JobLabelTaskID: "task-completed"}, CreationTimestamp: old},
		// Orphan: error + old.
		{Kind: "JVMChaos", Resource: "jvmchaos", Namespace: "hs0", Name: "term-error-old",
			Labels: map[string]string{consts.JobLabelTaskID: "task-error"}, CreationTimestamp: old},
		// Orphan: cancelled + old.
		{Kind: "DNSChaos", Resource: "dnschaos", Namespace: "hs0", Name: "term-cancelled-old",
			Labels: map[string]string{consts.JobLabelTaskID: "task-cancelled"}, CreationTimestamp: old},
		// Not orphan: terminal but too young — callback may still be in flight.
		{Kind: "PodChaos", Resource: "podchaos", Namespace: "hs0", Name: "term-completed-fresh",
			Labels: map[string]string{consts.JobLabelTaskID: "task-completed"}, CreationTimestamp: fresh},
		// Lookup error: surfaced as err, not as orphan.
		{Kind: "PodChaos", Resource: "podchaos", Namespace: "hs0", Name: "lookup-fail",
			Labels: map[string]string{consts.JobLabelTaskID: "task-lookup-fails"}, CreationTimestamp: old},
	}

	orphans, errs := ClassifyChaosOrphans(crs, lookup, now, time.Hour)

	if len(errs) != 1 {
		t.Fatalf("want 1 lookup err, got %d: %v", len(errs), errs)
	}

	got := make(map[string]OrphanReason, len(orphans))
	for _, o := range orphans {
		got[o.Name] = o.Reason
	}

	want := map[string]OrphanReason{
		"no-label":           ReasonNoTaskLabel,
		"missing-task":       ReasonTaskMissing,
		"term-completed-old": ReasonTaskTerminal,
		"term-error-old":     ReasonTaskTerminal,
		"term-cancelled-old": ReasonTaskTerminal,
	}
	if len(got) != len(want) {
		names := make([]string, 0, len(got))
		for k := range got {
			names = append(names, k)
		}
		sort.Strings(names)
		t.Fatalf("orphan count mismatch: got %d %v, want %d %v",
			len(got), names, len(want), want)
	}
	for name, reason := range want {
		if got[name] != reason {
			t.Errorf("%s: want reason %q, got %q", name, reason, got[name])
		}
	}

	for _, name := range []string{"live-running", "live-pending", "term-completed-fresh", "lookup-fail"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s should NOT be classified as orphan", name)
		}
	}
}

func TestClassifyChaosOrphans_ZeroMinAgeReapsAtSameInstant(t *testing.T) {
	// Edge case: minAge=0 means "no grace period". A terminal task whose CR
	// was created exactly at `now` should still be reaped — cutoff equals
	// the timestamp and `After(cutoff)` is false. Pin the semantics so a
	// future refactor doesn't flip it silently.
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	crs := []ChaosCRRef{{
		Kind: "PodChaos", Resource: "podchaos", Namespace: "hs0", Name: "instant",
		Labels: map[string]string{consts.JobLabelTaskID: "t"}, CreationTimestamp: now,
	}}
	lookup := func(string) (consts.TaskState, bool, error) {
		return consts.TaskCompleted, true, nil
	}
	orphans, errs := ClassifyChaosOrphans(crs, lookup, now, 0)
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(orphans) != 1 {
		t.Fatalf("want 1 orphan with minAge=0 and CR ts == now, got %d", len(orphans))
	}
}
