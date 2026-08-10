package cluster

import "time"

// ClusterCheckStatus mirrors the granular status values the portal's
// ClusterStatus page renders. It is intentionally distinct from the
// "healthy"/"unhealthy" pair returned by /system/health: cluster checks
// can be informationally degraded ("warn") or indeterminate ("unknown")
// without the system being broken.
type ClusterCheckStatus string

const (
	ClusterCheckOK      ClusterCheckStatus = "ok"
	ClusterCheckWarn    ClusterCheckStatus = "warn"
	ClusterCheckFail    ClusterCheckStatus = "fail"
	ClusterCheckUnknown ClusterCheckStatus = "unknown"
)

// ClusterCheckAction describes a remediation button the portal can render
// next to a failing check. The handler always emits action == nil until
// the Pedestal install/restart flow lands and wires the kind values to
// real endpoints; see the comment in service.go.
type ClusterCheckAction struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// ClusterCheck is the per-check payload the ClusterStatus page renders as
// a status card. IDs are stable and match the seeds the portal mock store
// currently uses (chk-k8s, chk-redis, …) so frontend wiring needs zero
// rename when this endpoint becomes the source of truth.
type ClusterCheck struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Status ClusterCheckStatus  `json:"status"`
	Detail string              `json:"detail"`
	Action *ClusterCheckAction `json:"action,omitempty"`
}

// ClusterEvent is a single line in the recent-events terminal. The handler
// returns an empty slice today; the source-of-truth for events is TBD.
type ClusterEvent struct {
	Ts    time.Time `json:"ts"`
	Level string    `json:"level"`
	Body  string    `json:"body"`
}

// ClusterStatusResp is the aggregated payload returned by
// GET /api/v2/cluster/status. It composes the per-check status grid plus
// the (currently empty) recent-events stream.
type ClusterStatusResp struct {
	Checks    []ClusterCheck `json:"checks"`
	Events    []ClusterEvent `json:"events"`
	UpdatedAt time.Time      `json:"updated_at"`
}
