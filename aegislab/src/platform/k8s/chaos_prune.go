package k8s

import (
	"time"

	"aegis/platform/consts"
)

// ChaosCRRef is the minimal projection of a chaos-mesh.org CR needed by the
// orphan predicate. Kept narrow so unit tests can drive the predicate without
// dragging in unstructured/runtime types.
type ChaosCRRef struct {
	Kind              string
	Resource          string // GVR.Resource (e.g. "podchaos")
	Namespace         string
	Name              string
	Labels            map[string]string
	CreationTimestamp time.Time
}

// TaskStateLookup answers "what state is task <id> in?". Mirrors the same
// shape the ratelimiter service already uses (see lookupTaskStateWith).
// `found` is false when no row exists for the id.
type TaskStateLookup func(taskID string) (state consts.TaskState, found bool, err error)

// OrphanReason is a short, stable token describing why a chaos CR is judged
// orphaned. CLI/API consumers grep on these — do not rephrase casually.
type OrphanReason string

const (
	// ReasonNoTaskLabel — the CR carries no `task_id` label at all. Either
	// it was hand-applied with `kubectl apply` or labels were stripped.
	ReasonNoTaskLabel OrphanReason = "no_task_label"
	// ReasonTaskMissing — `task_id` is set but no matching row in `tasks`.
	// The task was deleted (or never persisted), so nothing will ever reap
	// this CR via the normal callback path.
	ReasonTaskMissing OrphanReason = "task_missing"
	// ReasonTaskTerminal — task is in TaskCompleted / TaskError /
	// TaskCancelled and the CR has lingered past the age cutoff. The
	// chaos-mesh callback either never fired or already fired but failed
	// to delete the CR (zombie finalizer).
	ReasonTaskTerminal OrphanReason = "task_terminal"
)

// OrphanRecord is what the predicate emits for each CR judged orphaned.
type OrphanRecord struct {
	Kind       string        `json:"kind"`
	Resource   string        `json:"resource"`
	Namespace  string        `json:"namespace"`
	Name       string        `json:"name"`
	TaskID     string        `json:"task_id"`
	TaskState  string        `json:"task_state"`
	Reason     OrphanReason  `json:"reason"`
	AgeSeconds float64       `json:"age_seconds"`
	Age        time.Duration `json:"-"`
}

// ClassifyChaosOrphans applies the orphan predicate to `crs` using `lookup`
// to resolve task state. `now` and `minAge` together gate the terminal-task
// arm: a task that just terminated should not be reaped before its own
// callback has had a chance to run.
//
// A CR is orphaned when:
//
//  1. it carries no `task_id` label, OR
//  2. its `task_id` does not resolve to a tasks row, OR
//  3. the resolved task is in a terminal state AND the CR's creation
//     timestamp is older than `now - minAge`.
//
// Lookup errors are returned alongside whatever partial result was
// accumulated; callers decide whether to proceed.
func ClassifyChaosOrphans(
	crs []ChaosCRRef,
	lookup TaskStateLookup,
	now time.Time,
	minAge time.Duration,
) ([]OrphanRecord, []error) {
	if minAge < 0 {
		minAge = 0
	}
	cutoff := now.Add(-minAge)

	var (
		out  []OrphanRecord
		errs []error
	)
	for _, cr := range crs {
		taskID := cr.Labels[consts.JobLabelTaskID]
		age := now.Sub(cr.CreationTimestamp)
		base := OrphanRecord{
			Kind:       cr.Kind,
			Resource:   cr.Resource,
			Namespace:  cr.Namespace,
			Name:       cr.Name,
			TaskID:     taskID,
			AgeSeconds: age.Seconds(),
			Age:        age,
		}

		if taskID == "" {
			base.Reason = ReasonNoTaskLabel
			out = append(out, base)
			continue
		}

		state, found, err := lookup(taskID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !found {
			base.Reason = ReasonTaskMissing
			out = append(out, base)
			continue
		}
		if !isTerminalTaskState(state) {
			continue
		}
		// Terminal — but skip if the CR is younger than the cutoff so we
		// don't race the callback that's about to fire.
		if !cr.CreationTimestamp.IsZero() && cr.CreationTimestamp.After(cutoff) {
			continue
		}
		base.TaskState = consts.GetTaskStateName(state)
		base.Reason = ReasonTaskTerminal
		out = append(out, base)
	}
	return out, errs
}

// isTerminalTaskState mirrors ratelimiter.isTerminalState. Kept local so the
// platform/k8s package stays free of the crud/admin import.
func isTerminalTaskState(s consts.TaskState) bool {
	return s == consts.TaskCompleted || s == consts.TaskError || s == consts.TaskCancelled
}
