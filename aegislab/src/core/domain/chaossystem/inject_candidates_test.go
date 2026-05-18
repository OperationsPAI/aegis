package chaossystem

import (
	"context"
	"errors"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/model"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/spf13/viper"
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

// TestListInjectCandidatesAutoNamespaceFansOut pins the auto-namespace
// behaviour: when the caller omits namespace, the service expands the
// system's pool (Count=3 → bench-cand-auto0/1/2) and unions the per-namespace
// enumerator output, deduping candidates whose identity matches across pool
// slots.
func TestListInjectCandidatesAutoNamespaceFansOut(t *testing.T) {
	service, db, _ := newMetadataService(t)
	const systemName = "bench-cand-auto"
	cleanup := seedSystemInViper(t, systemName, false)
	defer cleanup()

	// Pool size = 3 so the auto-namespace fan-out has multiple slots to
	// hit; the seed helper already wires ns_pattern=^bench-cand-auto\d+$.
	viper.Set("injection.system."+systemName+".count", 3)
	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "3",
		ValueType:    consts.ConfigValueTypeInt,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	calledNamespaces := make([]string, 0, 3)
	prev := enumerateCandidatesFn
	defer func() { enumerateCandidatesFn = prev }()
	enumerateCandidatesFn = func(_ context.Context, _, namespace string) ([]guidedcli.GuidedConfig, error) {
		calledNamespaces = append(calledNamespaces, namespace)
		// Each pool slot exposes the same logical apps; dedup should
		// collapse them into one row each.
		return []guidedcli.GuidedConfig{
			{System: systemName, SystemType: "ts", Namespace: namespace, App: "frontend", ChaosType: "PodKill"},
			{System: systemName, SystemType: "ts", Namespace: namespace, App: "cart", ChaosType: "PodKill"},
		}, nil
	}

	resp, err := service.ListInjectCandidates(context.Background(), systemName, "")
	if err != nil {
		t.Fatalf("ListInjectCandidates auto-namespace: %v", err)
	}
	if len(calledNamespaces) != 3 {
		t.Fatalf("expected enumerator to be called once per pool slot (3), got %d: %v",
			len(calledNamespaces), calledNamespaces)
	}
	if resp.Count != 2 || len(resp.Candidates) != 2 {
		t.Fatalf("expected 2 deduplicated candidates, got count=%d candidates=%d",
			resp.Count, len(resp.Candidates))
	}
	apps := map[string]bool{}
	for _, c := range resp.Candidates {
		apps[c.App] = true
	}
	if !apps["frontend"] || !apps["cart"] {
		t.Errorf("expected dedup to keep both apps, got %v", apps)
	}
}

func TestListInjectCandidatesAutoNamespaceEmptyPool(t *testing.T) {
	service, db, _ := newMetadataService(t)
	const systemName = "bench-cand-zero"
	cleanup := seedSystemInViper(t, systemName, false)
	defer cleanup()

	// Count=0: no pool slots → empty response, no enumerator call.
	viper.Set("injection.system."+systemName+".count", 0)
	anchor := &model.DynamicConfig{
		Key:          systemKey(systemName, fieldCount),
		DefaultValue: "0",
		ValueType:    consts.ConfigValueTypeInt,
	}
	if err := db.Create(anchor).Error; err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	calls := 0
	prev := enumerateCandidatesFn
	defer func() { enumerateCandidatesFn = prev }()
	enumerateCandidatesFn = func(_ context.Context, _, _ string) ([]guidedcli.GuidedConfig, error) {
		calls++
		return nil, errors.New("should not be called for empty pool")
	}

	resp, err := service.ListInjectCandidates(context.Background(), systemName, "")
	if err != nil {
		t.Fatalf("ListInjectCandidates auto-namespace empty pool: %v", err)
	}
	if calls != 0 {
		t.Errorf("expected enumerator to be skipped for empty pool, got %d calls", calls)
	}
	if resp.Count != 0 || len(resp.Candidates) != 0 {
		t.Errorf("expected empty candidates, got %+v", resp)
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
