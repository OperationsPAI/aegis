package chaosprune

// PruneReq is the request body for POST /api/v2/admin/chaos/prune.
type PruneReq struct {
	// Namespace, when non-empty, scopes the sweep to a single namespace.
	// Empty = all namespaces. Use this to target one benchmark cluster slot.
	Namespace string `json:"namespace,omitempty"`
	// AgeSeconds is the minimum age (in seconds) a terminal-task CR must
	// reach before it's eligible for reaping. Defaults to 300 (5 min) so
	// the chaos-mesh callback always has a grace window. CRs with no
	// task_id label or a missing task row are reaped regardless of age.
	AgeSeconds int `json:"age_seconds,omitempty"`
	// DryRun, when true, returns the candidate set without deleting.
	// Defaults to true if omitted — explicit opt-in is required to mutate.
	DryRun *bool `json:"dry_run,omitempty"`
	// IncludeKinds restricts the sweep to specific chaos resource plurals,
	// e.g. ["podchaos","networkchaos"]. Empty = every namespaced
	// chaos-mesh.org resource discovered in the cluster.
	IncludeKinds []string `json:"include_kinds,omitempty"`
}

// PruneCandidate describes one orphaned CR. Mirrors k8s.OrphanRecord but
// re-declared here so the API DTO is decoupled from the platform layer.
type PruneCandidate struct {
	Kind       string  `json:"kind"`
	Resource   string  `json:"resource"`
	Namespace  string  `json:"namespace"`
	Name       string  `json:"name"`
	TaskID     string  `json:"task_id"`
	TaskState  string  `json:"task_state"`
	Reason     string  `json:"reason"`
	AgeSeconds float64 `json:"age_seconds"`
	// Deleted is true when DryRun=false and the delete succeeded.
	Deleted bool `json:"deleted"`
	// DeleteError carries the per-CR delete failure (if any) when the
	// orphan sweep tried to reap it. Stays empty in dry-run mode.
	DeleteError string `json:"delete_error,omitempty"`
}

// PruneResp is the response body for POST /api/v2/admin/chaos/prune.
type PruneResp struct {
	DryRun     bool             `json:"dry_run"`
	Namespace  string           `json:"namespace,omitempty"`
	AgeSeconds int              `json:"age_seconds"`
	Candidates []PruneCandidate `json:"candidates"`
	// Warnings collects non-fatal list/lookup errors (transient apiserver
	// hiccups, partial discovery, etc.). The candidate set still reflects
	// whatever could be enumerated.
	Warnings []string `json:"warnings,omitempty"`
}
