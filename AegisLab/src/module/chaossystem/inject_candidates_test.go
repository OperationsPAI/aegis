package chaossystem

import (
	"context"
	"errors"
	"testing"

	"aegis/consts"
	"aegis/model"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
)

// TestListInjectCandidatesReturnsFlatJSON pins the API contract for #181:
// the bulk endpoint returns a flat list of GuidedConfig-shaped tuples with
// system/namespace/app/chaos_type/target identifiers populated. Numerical
// params stay nil — callers fill them in before submitting.
func TestListInjectCandidatesReturnsFlatJSON(t *testing.T) {
	service, db, _ := newMetadataService(t)
	const systemName = "bench-cand"
	cleanup := seedSystemInViper(t, systemName, false)
	defer cleanup()

	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	// Stub the in-process enumerator: 3 candidates with mixed leaf shapes
	// (pod-level, http, jvm) so we can assert the JSON shape covers each
	// target dimension.
	prev := enumerateCandidatesFn
	defer func() { enumerateCandidatesFn = prev }()
	enumerateCandidatesFn = func(ctx context.Context, system, namespace string) ([]guidedcli.GuidedConfig, error) {
		if system != systemName {
			t.Errorf("enumerator called with system=%q, want %q", system, systemName)
		}
		if namespace != "bench-cand0" {
			t.Errorf("enumerator called with namespace=%q, want bench-cand0", namespace)
		}
		return []guidedcli.GuidedConfig{
			{System: system, SystemType: "ts", Namespace: namespace, App: "frontend", ChaosType: "PodKill"},
			{System: system, SystemType: "ts", Namespace: namespace, App: "frontend", ChaosType: "HTTPRequestAbort", Route: "/api", HTTPMethod: "POST"},
			{System: system, SystemType: "ts", Namespace: namespace, App: "frontend", ChaosType: "JVMLatency", Class: "com.example.A", Method: "doIt"},
		}, nil
	}

	resp, err := service.ListInjectCandidates(context.Background(), systemName, "bench-cand0")
	if err != nil {
		t.Fatalf("ListInjectCandidates: %v", err)
	}
	if resp == nil {
		t.Fatal("ListInjectCandidates: nil response")
	}
	if resp.Count != 3 || len(resp.Candidates) != 3 {
		t.Fatalf("count mismatch: count=%d candidates=%d", resp.Count, len(resp.Candidates))
	}

	// Pod-level: no target identifiers populated.
	pod := resp.Candidates[0]
	if pod.ChaosType != "PodKill" || pod.App != "frontend" || pod.Container != "" {
		t.Errorf("pod-level candidate shape wrong: %+v", pod)
	}

	// HTTP: route + http_method present, no domain/class/etc.
	http := resp.Candidates[1]
	if http.ChaosType != "HTTPRequestAbort" || http.Route != "/api" || http.HTTPMethod != "POST" {
		t.Errorf("http candidate shape wrong: %+v", http)
	}
	if http.Container != "" || http.Domain != "" || http.Class != "" {
		t.Errorf("http candidate has stray identifiers: %+v", http)
	}

	// JVM: class + method present.
	jvm := resp.Candidates[2]
	if jvm.ChaosType != "JVMLatency" || jvm.Class != "com.example.A" || jvm.Method != "doIt" {
		t.Errorf("jvm candidate shape wrong: %+v", jvm)
	}
}

func TestListInjectCandidatesRejectsEmptyNamespace(t *testing.T) {
	service, db, _ := newMetadataService(t)
	const systemName = "bench-cand-empty"
	cleanup := seedSystemInViper(t, systemName, false)
	defer cleanup()

	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "1",
		ValueType:    consts.ConfigValueTypeInt,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	_, err := service.ListInjectCandidates(context.Background(), systemName, "")
	if err == nil {
		t.Fatal("expected ListInjectCandidates to reject empty namespace")
	}
	if !errors.Is(err, consts.ErrBadRequest) {
		t.Errorf("error should wrap ErrBadRequest, got %v", err)
	}
}

func TestListInjectCandidatesNotFoundForUnseededSystem(t *testing.T) {
	service, _, _ := newMetadataService(t)

	_, err := service.ListInjectCandidates(context.Background(), "no-such-system", "no-such-system0")
	if err == nil {
		t.Fatal("expected ListInjectCandidates to return ErrNotFound for unseeded system")
	}
	if !errors.Is(err, consts.ErrNotFound) {
		t.Errorf("error should wrap ErrNotFound, got %v", err)
	}
}
