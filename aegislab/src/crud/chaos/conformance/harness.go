// Package conformance is the §9 conformance harness shell.
//
// TODO(step-2): the pod_kill Observe currently only asserts the CR exists.
// The §9 observable contract requires asserting target_pod.restart_count
// increases by >= 1 within injection_window_s. Wire a typed kube client
// into Case so Observe can poll Pod.Status.ContainerStatuses[].RestartCount.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"aegis/crud/chaos"
)

// Result is the outcome of running a single (Executor, Capability) pair
// through the apply→observe→destroy→observe sequence.
type Result struct {
	Capability     string
	Executor       string
	Applied        bool
	Observed       bool
	Destroyed      bool
	PostDestroy    bool
	AssertionLog   []string
	FailureReason  string
}

func (r Result) Passed() bool {
	return r.Applied && r.Observed && r.Destroyed && r.PostDestroy && r.FailureReason == ""
}

// Case is the per-Capability adapter the harness calls. Concrete
// Capabilities supply their own observe / post-destroy assertion logic.
type Case struct {
	Capability     string
	IdempotencyKey string
	Namespace      string
	SystemContext  chaos.SystemContext
	Target         map[string]any
	Params         map[string]any
	// Observe asserts the Capability's observable contract (§9) is met
	// while the injection is live. Returns nil on contract pass.
	Observe func(ctx context.Context) error
	// PostDestroy asserts the cluster artifact is gone and the
	// environment has recovered.
	PostDestroy func(ctx context.Context) error
}

type Harness struct {
	Executor      chaos.Executor
	ObserveWait   time.Duration
	DestroyWait   time.Duration
}

func NewHarness(exec chaos.Executor) *Harness {
	return &Harness{
		Executor:    exec,
		ObserveWait: 30 * time.Second,
		DestroyWait: 30 * time.Second,
	}
}

func (h *Harness) Run(ctx context.Context, c Case) Result {
	r := Result{Capability: c.Capability, Executor: h.Executor.Name()}

	handle, err := h.Executor.DeriveHandle(c.Capability, c.IdempotencyKey, c.Namespace, c.Target)
	if err != nil {
		r.FailureReason = fmt.Sprintf("derive-handle: %v", err)
		return r
	}
	if err := h.Executor.Apply(ctx, c.SystemContext, c.Capability, handle, c.Target, c.Params); err != nil {
		r.FailureReason = fmt.Sprintf("apply: %v", err)
		return r
	}
	r.Applied = true
	r.AssertionLog = append(r.AssertionLog, fmt.Sprintf("applied handle=%s", handle))

	if c.Observe != nil {
		if err := waitFor(ctx, h.ObserveWait, c.Observe); err != nil {
			r.FailureReason = fmt.Sprintf("observe: %v", err)
			_ = h.Executor.Destroy(ctx, handle)
			return r
		}
	}
	r.Observed = true

	if err := h.Executor.Destroy(ctx, handle); err != nil {
		r.FailureReason = fmt.Sprintf("destroy: %v", err)
		return r
	}
	r.Destroyed = true

	if c.PostDestroy != nil {
		if err := waitFor(ctx, h.DestroyWait, c.PostDestroy); err != nil {
			r.FailureReason = fmt.Sprintf("post-destroy: %v", err)
			return r
		}
	}
	r.PostDestroy = true
	return r
}

func waitFor(ctx context.Context, total time.Duration, fn func(context.Context) error) error {
	deadline := time.Now().Add(total)
	var lastErr error
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// ErrSkipped is returned by a Case's Observe / PostDestroy when the
// environment doesn't satisfy preconditions (no target workload, no
// in-cluster privilege). The harness treats it as a real skip, not a
// failure.
var ErrSkipped = errors.New("conformance: skipped")
