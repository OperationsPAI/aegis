package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/stretchr/testify/require"
)

// TestInjectSubmitResponseDedupeHelpers exercises the IsDedupedAll /
// DedupeSummary helpers and the errDedupeSuppressed sentinel wrapping. This
// is the unit-level guard for the UX fix against issues #91/#92.
func TestInjectSubmitResponseDedupeHelpers(t *testing.T) {
	// Nil warnings → not deduped.
	r := &injectSubmitResponse{Items: nil}
	if r.IsDedupedAll() {
		t.Fatalf("nil warnings must not be deduped")
	}
	if s := r.DedupeSummary(); s != "" {
		t.Fatalf("empty summary expected, got %q", s)
	}

	// Items populated, warnings set → not deduped (partial-success case).
	r = &injectSubmitResponse{
		Items:    []injectSubmitItem{{Index: 0, TraceID: "t-1"}},
		Warnings: &injectSubmitWarnings{BatchesExistInDatabase: []int{1}},
	}
	if r.IsDedupedAll() {
		t.Fatalf("partial success must not report deduped-all")
	}

	// Items empty + BatchesExistInDatabase set → deduped.
	r = &injectSubmitResponse{
		Items:    []injectSubmitItem{},
		Warnings: &injectSubmitWarnings{BatchesExistInDatabase: []int{0, 2}},
	}
	if !r.IsDedupedAll() {
		t.Fatalf("expected deduped-all")
	}
	summary := r.DedupeSummary()
	if !strings.Contains(summary, "duplicate submission suppressed") {
		t.Fatalf("summary missing friendly prefix: %q", summary)
	}
	if !strings.Contains(summary, "0, 2") {
		t.Fatalf("summary must list batch indices, got %q", summary)
	}

	// newDedupeSuppressedError must wrap errDedupeSuppressed and carry the
	// distinct exit code.
	err := newDedupeSuppressedError(summary)
	if !errors.Is(err, errDedupeSuppressed) {
		t.Fatalf("error must wrap errDedupeSuppressed; got %v", err)
	}
	if code := exitCodeFor(err); code != ExitCodeDedupeSuppressed {
		t.Fatalf("exitCodeFor = %d, want %d", code, ExitCodeDedupeSuppressed)
	}
}

func TestSubmitGuidedApplyRequiresTags(t *testing.T) {
	origPedestalName := guidedApplyPedestalName
	origPedestalTag := guidedApplyPedestalTag
	origBenchmarkName := guidedApplyBenchmarkName
	origBenchmarkTag := guidedApplyBenchmarkTag
	defer func() {
		guidedApplyPedestalName = origPedestalName
		guidedApplyPedestalTag = origPedestalTag
		guidedApplyBenchmarkName = origBenchmarkName
		guidedApplyBenchmarkTag = origBenchmarkTag
	}()

	guidedApplyPedestalName = "ts"
	guidedApplyPedestalTag = ""
	guidedApplyBenchmarkName = "bench"
	guidedApplyBenchmarkTag = ""

	err := submitGuidedApply(guidedcli.GuidedConfig{})
	require.ErrorContains(t, err, "--pedestal-tag")
	require.ErrorContains(t, err, "--benchmark-tag")
}
