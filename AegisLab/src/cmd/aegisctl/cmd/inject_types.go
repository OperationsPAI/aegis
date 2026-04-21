package cmd

import (
	"errors"
	"fmt"
	"strings"
)

// This file holds the small shared types used by multiple inject/regression
// code paths. They live here (rather than in inject.go or inject_guided.go)
// so that callers — `inject guided --apply`, `regression run`, and the execute
// CLI's YAML spec — can depend on a stable surface without pulling in the
// larger, command-specific files.

// injectSubmitResponse captures the fields we care about from the server's
// SubmitInjectionResp envelope (see src/module/injection/api_types.go).
type injectSubmitResponse struct {
	GroupID       string                  `json:"group_id"`
	Items         []injectSubmitItem      `json:"items"`
	OriginalCount int                     `json:"original_count"`
	Warnings      *injectSubmitWarnings   `json:"warnings,omitempty"`
}

// injectSubmitWarnings mirrors module/injection.InjectionWarnings so the CLI
// can distinguish "server silently deduped this submission" from a generic
// empty response. See SubmitInjectionResp in the server module.
type injectSubmitWarnings struct {
	DuplicateServicesInBatch  []string `json:"duplicate_services_in_batch,omitempty"`
	DuplicateBatchesInRequest []int    `json:"duplicate_batches_in_request,omitempty"`
	BatchesExistInDatabase    []int    `json:"batches_exist_in_database,omitempty"`
}

// IsDedupedAll reports whether the server dropped every submitted batch
// because each was a duplicate of an existing injection. When true, Items is
// empty and the caller has no trace_id to follow.
func (r *injectSubmitResponse) IsDedupedAll() bool {
	if r == nil {
		return false
	}
	if len(r.Items) != 0 {
		return false
	}
	if r.Warnings == nil {
		return false
	}
	return len(r.Warnings.BatchesExistInDatabase) > 0
}

// DedupeSummary renders a short, human-friendly message explaining why a
// submission produced no trace_id. Safe to call even when the response is
// not deduped; returns "" in that case.
func (r *injectSubmitResponse) DedupeSummary() string {
	if !r.IsDedupedAll() {
		return ""
	}
	idxs := make([]string, 0, len(r.Warnings.BatchesExistInDatabase))
	for _, i := range r.Warnings.BatchesExistInDatabase {
		idxs = append(idxs, fmt.Sprintf("%d", i))
	}
	return fmt.Sprintf(
		"duplicate submission suppressed (batches [%s]); change a spec field or wait for cooldown",
		strings.Join(idxs, ", "),
	)
}

// errDedupeSuppressed is the sentinel error returned by submit paths when
// the server deduped every batch. The CLI maps this to
// ExitCodeDedupeSuppressed so scripts can branch on it.
var errDedupeSuppressed = errors.New("injection submission was deduplicated by server")

// newDedupeSuppressedError wraps errDedupeSuppressed with a caller-supplied
// human summary while preserving a stable exit code.
func newDedupeSuppressedError(summary string) error {
	return &exitError{
		Code:    ExitCodeDedupeSuppressed,
		Message: summary,
		Cause:   errDedupeSuppressed,
	}
}

// injectSubmitItem is one element of injectSubmitResponse.Items.
type injectSubmitItem struct {
	Index   int    `json:"index"`
	TraceID string `json:"trace_id"`
	TaskID  string `json:"task_id"`
}

// ContainerRef references a container image with optional overrides. It is
// shared by the execute CLI's YAML spec (see execute.go).
type ContainerRef struct {
	Name    string          `yaml:"name"                 json:"name"`
	Version string          `yaml:"version,omitempty"    json:"version,omitempty"`
	EnvVars []ParameterSpec `yaml:"env_vars,omitempty"   json:"env_vars,omitempty"`
	Payload map[string]any  `yaml:"payload,omitempty"    json:"payload,omitempty"`
}

// LabelItem is a key-value label, shared with the execute CLI's YAML spec.
type LabelItem struct {
	Key   string `yaml:"key"   json:"key"`
	Value string `yaml:"value" json:"value"`
}

// ParameterSpec is a key-value parameter (env var, etc.) used inside a
// ContainerRef envelope.
type ParameterSpec struct {
	Key   string `yaml:"key"   json:"key"`
	Value string `yaml:"value" json:"value"`
}
