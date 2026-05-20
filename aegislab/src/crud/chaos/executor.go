package chaos

import "context"

type ExecState int

const (
	ExecStatePending ExecState = iota
	ExecStateRunning
	ExecStateSucceeded
	ExecStateFailed
	// ExecStateOrphaned signals the executor's CR is gone but the executor
	// cannot tell whether that is the legitimate post-Destroy steady state
	// or a mid-flight vanish (manual kubectl delete, GC race). Callers that
	// hold the row's prior status decide: terminal-prior rows treat this
	// as Succeeded; pending/running rows treat it as Failed.
	ExecStateOrphaned
)

func (s ExecState) String() string {
	switch s {
	case ExecStatePending:
		return StatusPending
	case ExecStateRunning:
		return StatusRunning
	case ExecStateSucceeded:
		return StatusSucceeded
	case ExecStateFailed:
		return StatusFailed
	case ExecStateOrphaned:
		return "orphaned"
	}
	return "unknown"
}

type CapabilitySupport struct {
	Capability string `json:"capability"`
	Maturity   string `json:"maturity"`
}

// Executor is the §8 internal abstraction. ADR-0004 requires the handle
// to be persisted BEFORE Apply, so the service layer derives the handle
// up-front (via DeriveHandle) and hands it in. Apply itself is a pure
// renderer — it does not allocate names.
type Executor interface {
	Name() string
	SupportedCapabilities() []CapabilitySupport

	// DeriveHandle produces the deterministic executor_handle for an
	// (idempotency_key, target) tuple. Pure function — no cluster I/O.
	// Same inputs always yield the same handle, which is what makes
	// post-crash recovery a plain Status(handle) call.
	DeriveHandle(capability, idempotencyKey string, target map[string]any) (handle string, err error)

	// Apply is idempotent on handle: the second call with the same handle
	// MUST be a no-op on the cluster. Chaos-Mesh treats AlreadyExists as
	// success per ADR-0004.
	Apply(ctx context.Context,
		sysCtx SystemContext,
		capability, handle string,
		target, params map[string]any,
	) error

	Status(ctx context.Context, handle string) (state ExecState, diagnostics map[string]any, err error)

	// Destroy is idempotent: destroying an absent resource MUST succeed
	// (ADR-0003).
	Destroy(ctx context.Context, handle string) error
}
