package guidedcli

import (
	"context"
	"testing"

	"github.com/OperationsPAI/chaos-experiment/internal/resourcelookup"
	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

// installEnumerateFixture rewires the package-level hooks to a tiny
// in-memory fixture and returns a teardown that restores the originals.
// Tests should always defer the teardown — the hooks are package-level
// state, so leakage would corrupt other tests in the same package run.
func installEnumerateFixture(t *testing.T, fx enumerateFixture) func() {
	t.Helper()

	originals := struct {
		apps          func(ctx context.Context, namespace string, systemType systemconfig.SystemType) ([]string, error)
		containers    func(ctx context.Context, systemType systemconfig.SystemType, namespace, app string) ([]string, error)
		http          func(systemType systemconfig.SystemType, app string) ([]resourcelookup.AppEndpointPair, error)
		network       func(systemType systemconfig.SystemType, app string) ([]string, error)
		dns           func(systemType systemconfig.SystemType, app string) ([]resourcelookup.AppDNSPair, error)
		jvmMethods    func(systemType systemconfig.SystemType, app string) ([]resourcelookup.AppMethodPair, error)
		dbOps         func(systemType systemconfig.SystemType, app string) ([]resourcelookup.AppDatabasePair, error)
		runtimeMutator func(systemType systemconfig.SystemType, app string) ([]runtimeMutatorTarget, error)
	}{
		apps:           enumerateAppLabelsHook,
		containers:     enumerateContainersByAppHook,
		http:           enumerateHTTPEndpointsByAppHook,
		network:        enumerateNetworkTargetsByAppHook,
		dns:            enumerateDNSDomainsByAppHook,
		jvmMethods:     enumerateJVMMethodsByAppHook,
		dbOps:          enumerateDatabaseOpsByAppHook,
		runtimeMutator: enumerateRuntimeMutatorMethodsByAppHook,
	}

	enumerateAppLabelsHook = func(ctx context.Context, namespace string, systemType systemconfig.SystemType) ([]string, error) {
		return fx.apps, nil
	}
	enumerateContainersByAppHook = func(ctx context.Context, systemType systemconfig.SystemType, namespace, app string) ([]string, error) {
		return fx.containersByApp[app], nil
	}
	enumerateHTTPEndpointsByAppHook = func(systemType systemconfig.SystemType, app string) ([]resourcelookup.AppEndpointPair, error) {
		return fx.httpByApp[app], nil
	}
	enumerateNetworkTargetsByAppHook = func(systemType systemconfig.SystemType, app string) ([]string, error) {
		return fx.networkByApp[app], nil
	}
	enumerateDNSDomainsByAppHook = func(systemType systemconfig.SystemType, app string) ([]resourcelookup.AppDNSPair, error) {
		return fx.dnsByApp[app], nil
	}
	enumerateJVMMethodsByAppHook = func(systemType systemconfig.SystemType, app string) ([]resourcelookup.AppMethodPair, error) {
		return fx.jvmMethodsByApp[app], nil
	}
	enumerateDatabaseOpsByAppHook = func(systemType systemconfig.SystemType, app string) ([]resourcelookup.AppDatabasePair, error) {
		return fx.dbOpsByApp[app], nil
	}
	enumerateRuntimeMutatorMethodsByAppHook = func(systemType systemconfig.SystemType, app string) ([]runtimeMutatorTarget, error) {
		return fx.mutatorByApp[app], nil
	}

	return func() {
		enumerateAppLabelsHook = originals.apps
		enumerateContainersByAppHook = originals.containers
		enumerateHTTPEndpointsByAppHook = originals.http
		enumerateNetworkTargetsByAppHook = originals.network
		enumerateDNSDomainsByAppHook = originals.dns
		enumerateJVMMethodsByAppHook = originals.jvmMethods
		enumerateDatabaseOpsByAppHook = originals.dbOps
		enumerateRuntimeMutatorMethodsByAppHook = originals.runtimeMutator
	}
}

type enumerateFixture struct {
	apps            []string
	containersByApp map[string][]string
	httpByApp       map[string][]resourcelookup.AppEndpointPair
	networkByApp    map[string][]string
	dnsByApp        map[string][]resourcelookup.AppDNSPair
	jvmMethodsByApp map[string][]resourcelookup.AppMethodPair
	dbOpsByApp      map[string][]resourcelookup.AppDatabasePair
	mutatorByApp    map[string][]runtimeMutatorTarget
}

func TestEnumerateAllCandidates_LeafCount(t *testing.T) {
	// Tiny fixture: 1 app, 2 containers, 3 http endpoints, 2 network
	// targets, 1 dns domain, 2 jvm methods, 1 db op, 2 runtime mutators
	// on the same method.
	//
	// Expected leaf count:
	//   pod-level: 2 (PodKill + PodFailure) + 4*2 (Container+CPU+Mem+Time × 2 containers)
	//            = 2 + 8 = 10
	//   http: 9 chaos types × 3 endpoints = 27
	//   network: 6 × 2 = 12
	//   dns: 2 × 1 = 2
	//   jvm: 1 (GC) + 5 chaos types × 2 methods = 1 + 10 = 11
	//   db: 2 × 1 = 2
	//   mutator: 1 method × 2 mutator configs = 2
	//   total: 10 + 27 + 12 + 2 + 11 + 2 + 2 = 66
	teardown := installEnumerateFixture(t, enumerateFixture{
		apps: []string{"frontend"},
		containersByApp: map[string][]string{
			"frontend": {"frontend", "sidecar"},
		},
		httpByApp: map[string][]resourcelookup.AppEndpointPair{
			"frontend": {
				{AppName: "frontend", Method: "GET", Route: "/", ServerAddress: "frontend"},
				{AppName: "frontend", Method: "GET", Route: "/health", ServerAddress: "frontend"},
				{AppName: "frontend", Method: "POST", Route: "/api", ServerAddress: "frontend"},
			},
		},
		networkByApp: map[string][]string{
			"frontend": {"backend", "auth"},
		},
		dnsByApp: map[string][]resourcelookup.AppDNSPair{
			"frontend": {{AppName: "frontend", Domain: "external.example.com"}},
		},
		jvmMethodsByApp: map[string][]resourcelookup.AppMethodPair{
			"frontend": {
				{AppName: "frontend", ClassName: "com.example.A", MethodName: "doIt"},
				{AppName: "frontend", ClassName: "com.example.B", MethodName: "alsoDoIt"},
			},
		},
		dbOpsByApp: map[string][]resourcelookup.AppDatabasePair{
			"frontend": {
				{AppName: "frontend", DBName: "shop", TableName: "orders", OperationType: "select"},
			},
		},
		mutatorByApp: map[string][]runtimeMutatorTarget{
			"frontend": {
				{AppName: "frontend", ClassName: "com.example.A", MethodName: "doIt", MutationTypeName: "constant", MutationFrom: "1", MutationTo: "0"},
				{AppName: "frontend", ClassName: "com.example.A", MethodName: "doIt", MutationTypeName: "constant", MutationFrom: "1", MutationTo: "9"},
			},
		},
	})
	defer teardown()

	// sockshop is registered as a builtin system, and matchSystemInstance
	// resolves "sockshop0" to it. Use sockshop0 here so normalizeSystemSelection
	// doesn't reject the input on a fresh cluster-less test run.
	got, err := EnumerateAllCandidates(context.Background(), "sockshop0", "sockshop0")
	if err != nil {
		t.Fatalf("EnumerateAllCandidates returned error: %v", err)
	}

	const want = 66
	if len(got) != want {
		t.Fatalf("leaf count mismatch: want %d, got %d\n%s", want, len(got), debugByChaosType(got))
	}

	// Spot-check chaos-type breakdown.
	wantBreakdown := map[string]int{
		"PodKill":                  1,
		"PodFailure":               1,
		"ContainerKill":            2,
		"CPUStress":                2,
		"MemoryStress":             2,
		"TimeSkew":                 2,
		"HTTPRequestAbort":         3,
		"HTTPResponseAbort":        3,
		"HTTPRequestDelay":         3,
		"HTTPResponseDelay":        3,
		"HTTPResponseReplaceBody":  3,
		"HTTPResponsePatchBody":    3,
		"HTTPRequestReplacePath":   3,
		"HTTPRequestReplaceMethod": 3,
		"HTTPResponseReplaceCode":  3,
		"NetworkDelay":             2,
		"NetworkPartition":         2,
		"NetworkLoss":              2,
		"NetworkDuplicate":         2,
		"NetworkCorrupt":           2,
		"NetworkBandwidth":         2,
		"DNSError":                 1,
		"DNSRandom":                1,
		"JVMGarbageCollector":      1,
		"JVMLatency":               2,
		"JVMReturn":                2,
		"JVMException":             2,
		"JVMCPUStress":             2,
		"JVMMemoryStress":          2,
		"JVMMySQLLatency":          1,
		"JVMMySQLException":        1,
		"JVMRuntimeMutator":        2,
	}
	gotBreakdown := map[string]int{}
	for _, c := range got {
		gotBreakdown[c.ChaosType]++
	}
	for ct, want := range wantBreakdown {
		if gotBreakdown[ct] != want {
			t.Errorf("chaos_type=%s: want %d, got %d", ct, want, gotBreakdown[ct])
		}
	}

	// Validate every candidate carries the system+namespace+app context — agents
	// downstream depend on the JSON shape staying flat (see issue #181 caller
	// requirement: "{system, namespace, app, chaos_type, params}").
	for i, c := range got {
		if c.System == "" || c.Namespace == "" || c.App == "" || c.ChaosType == "" {
			t.Errorf("candidate %d missing identifiers: %+v", i, c)
		}
	}
}

func TestEnumerateAllCandidates_EmptyAppListGivesEmptySlice(t *testing.T) {
	teardown := installEnumerateFixture(t, enumerateFixture{
		apps: nil,
	})
	defer teardown()

	got, err := EnumerateAllCandidates(context.Background(), "sockshop0", "sockshop0")
	if err != nil {
		t.Fatalf("EnumerateAllCandidates returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result for app-less namespace, got %d candidates", len(got))
	}
}

func TestEnumerateAllCandidates_GatesChaosTypesOnEmptyResources(t *testing.T) {
	// App with no http endpoints / network / dns / jvm should produce only
	// the pod-level candidates (which are gated on containers).
	teardown := installEnumerateFixture(t, enumerateFixture{
		apps: []string{"only-pod"},
		containersByApp: map[string][]string{
			"only-pod": {"only-pod"},
		},
	})
	defer teardown()

	got, err := EnumerateAllCandidates(context.Background(), "sockshop0", "sockshop0")
	if err != nil {
		t.Fatalf("EnumerateAllCandidates returned error: %v", err)
	}

	// Want: PodKill, PodFailure, ContainerKill, CPUStress, MemoryStress, TimeSkew (× 1 container)
	// total: 2 + 4 = 6
	if len(got) != 6 {
		t.Fatalf("want 6 pod-level candidates, got %d\n%s", len(got), debugByChaosType(got))
	}
	for _, c := range got {
		if c.HTTPMethod != "" || c.Route != "" || c.TargetService != "" || c.Domain != "" || c.Class != "" {
			t.Errorf("expected pod-level candidate to have empty target identifiers, got %+v", c)
		}
	}
}

func TestEnumerateAllCandidates_RejectsUnknownSystem(t *testing.T) {
	teardown := installEnumerateFixture(t, enumerateFixture{apps: []string{"unused"}})
	defer teardown()

	_, err := EnumerateAllCandidates(context.Background(), "no-such-system-12345", "no-such-system-12345")
	if err == nil {
		t.Fatal("expected EnumerateAllCandidates to error on unknown system")
	}
}

// debugByChaosType is a t.Helper friend: turns a slice of candidates into a
// printable breakdown for assertion failure messages. Kept private — only
// the in-package tests need it.
func debugByChaosType(items []GuidedConfig) string {
	counts := map[string]int{}
	for _, c := range items {
		counts[c.ChaosType]++
	}
	out := ""
	for k, v := range counts {
		out += k + ": " + itoa(v) + "\n"
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	if neg {
		return "-" + digits
	}
	return digits
}
