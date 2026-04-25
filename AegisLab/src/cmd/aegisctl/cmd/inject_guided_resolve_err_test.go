package cmd

import (
	"strings"
	"testing"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
)

// TestGuidedResolveErr_HappyPath asserts that a finalized response with no
// errors and stage=ready_to_apply does not trip the short-circuit.
func TestGuidedResolveErr_HappyPath(t *testing.T) {
	resp := &guidedcli.GuidedResponse{
		Stage: "ready_to_apply",
	}
	if err := guidedResolveErr(resp); err != nil {
		t.Fatalf("expected nil error for ready_to_apply with no errors; got %v", err)
	}
}

// TestGuidedResolveErr_AppliedStageOK guards the rare case where the
// resolver actually performed an apply locally (cfg.Apply=true, not the
// aegisctl path) — that's still a terminal-success stage.
func TestGuidedResolveErr_AppliedStageOK(t *testing.T) {
	resp := &guidedcli.GuidedResponse{
		Stage: "applied",
	}
	if err := guidedResolveErr(resp); err != nil {
		t.Fatalf("expected nil error for applied stage; got %v", err)
	}
}

// TestGuidedResolveErr_FailFastOnResolverErrors is the primary issue-#176
// guard: when the local resolver returns a non-empty Errors slice, the CLI
// must fail with usage exit code and surface the resolver error verbatim
// instead of shipping an un-normalized config to /inject (which would
// produce the misleading "duration must be greater than 0" server-side).
func TestGuidedResolveErr_FailFastOnResolverErrors(t *testing.T) {
	resp := &guidedcli.GuidedResponse{
		Stage: "fill_required_fields",
		Config: guidedcli.GuidedConfig{
			ChaosType: "JVMMemoryStress",
			App:       "shipping",
			Class:     "com.example.ShippingResource",
			Method:    "ship",
		},
		Errors: []string{
			`jvm method "ship" not found under app "shipping" class "com.example.ShippingResource"`,
		},
	}
	err := guidedResolveErr(resp)
	if err == nil {
		t.Fatalf("expected error when resolver reports errors; got nil")
	}
	if code := exitCodeFor(err); code != ExitCodeUsage {
		t.Fatalf("expected ExitCodeUsage (%d); got %d", ExitCodeUsage, code)
	}
	msg := err.Error()
	if !strings.Contains(msg, "JVMMemoryStress") {
		t.Fatalf("error message must include the chaos_type for context; got %q", msg)
	}
	if !strings.Contains(msg, "app=shipping") {
		t.Fatalf("error message must include identifier (app=); got %q", msg)
	}
	if !strings.Contains(msg, "jvm method") {
		t.Fatalf("error message must surface the resolver's actual error; got %q", msg)
	}
	// Crucially: must not mention the misleading server-side duration check.
	if strings.Contains(msg, "duration must be greater than 0") {
		t.Fatalf("error message must not regurgitate the misleading server validator message; got %q", msg)
	}
}

// TestGuidedResolveErr_PreservesAllErrors ensures every resolver error
// reaches the user, not just the first.
func TestGuidedResolveErr_PreservesAllErrors(t *testing.T) {
	resp := &guidedcli.GuidedResponse{
		Stage: "fill_required_fields",
		Config: guidedcli.GuidedConfig{
			ChaosType: "JVMException",
			App:       "auth",
		},
		Errors: []string{
			"exception_opt is required",
			"class not found in cache",
			"resource lookup timed out",
		},
	}
	err := guidedResolveErr(resp)
	if err == nil {
		t.Fatalf("expected error; got nil")
	}
	msg := err.Error()
	for _, want := range []string{"exception_opt is required", "class not found in cache", "resource lookup timed out"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message must preserve resolver error %q; got %q", want, msg)
		}
	}
}

// TestGuidedResolveErr_NonFinalStageFailsEvenWithoutErrors covers the edge
// case where the resolver returns a non-final stage (e.g. still asking for
// more fields) but happens not to populate Errors[]. Submitting that
// response would still ship an incomplete config to the server.
func TestGuidedResolveErr_NonFinalStageFailsEvenWithoutErrors(t *testing.T) {
	resp := &guidedcli.GuidedResponse{
		Stage: "fill_required_fields",
		Config: guidedcli.GuidedConfig{
			ChaosType: "JVMMemoryStress",
		},
	}
	err := guidedResolveErr(resp)
	if err == nil {
		t.Fatalf("expected error for non-final stage; got nil")
	}
	if code := exitCodeFor(err); code != ExitCodeUsage {
		t.Fatalf("expected ExitCodeUsage (%d); got %d", ExitCodeUsage, code)
	}
	if !strings.Contains(err.Error(), "fill_required_fields") {
		t.Fatalf("error message must surface the stage; got %q", err.Error())
	}
}

// TestGuidedResolveErr_NilResponse hardens against a nil pointer (defensive;
// guidedcli.Resolve returning nil with err==nil shouldn't happen but a
// caller mistake would otherwise panic in submit).
func TestGuidedResolveErr_NilResponse(t *testing.T) {
	if err := guidedResolveErr(nil); err == nil {
		t.Fatalf("expected error for nil response; got nil")
	}
}
