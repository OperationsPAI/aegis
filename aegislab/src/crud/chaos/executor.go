package chaos

import "context"

type ExecState int

const (
	ExecStatePending ExecState = iota
	ExecStateRunning
	ExecStateSucceeded
	ExecStateFailed
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
	}
	return "unknown"
}

type CapabilitySupport struct {
	Capability string `json:"capability"`
	Maturity   string `json:"maturity"`
}

// Executor is the §8 internal abstraction. Not exposed externally until a
// second Executor lands (per §11 step 7) — keep it minimal.
type Executor interface {
	Name() string
	SupportedCapabilities() []CapabilitySupport

	// Apply MUST be idempotent on idempotencyKey: a second call with the
	// same key MUST be a no-op on the cluster and return the same handle.
	// The Chaos-Mesh implementation derives CR metadata.name from the key
	// (ADR-0004) so the natural backend uniqueness constraint enforces this.
	Apply(ctx context.Context,
		capability, idempotencyKey string,
		target, params map[string]any,
	) (handle string, err error)

	Status(ctx context.Context, handle string) (state ExecState, diagnostics map[string]any, err error)

	// Destroy is idempotent: destroying an absent resource MUST succeed
	// (ADR-0003).
	Destroy(ctx context.Context, handle string) error
}
