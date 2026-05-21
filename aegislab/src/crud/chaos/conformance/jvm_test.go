//go:build chaos_conformance

package conformance

import (
	"context"
	"os"
	"testing"
	"time"

	"aegis/crud/chaos"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

// TestJVMConformance runs DeriveHandle → Apply → Status → Destroy for
// each of the 8 JVMChaos capabilities against a real cluster.
//
// Required env:
//   CONFORMANCE_NAMESPACE — namespace that hosts the target Java app
//   CONFORMANCE_JVM_APP — `app` label of the Java pod (must have
//     chaos-mesh Byteman agent injected on port 9277)
//   CONFORMANCE_JVM_CLASS — fully-qualified class for method-targeted caps
//   CONFORMANCE_JVM_METHOD — method on that class
//   CONFORMANCE_JVM_DB — MySQL database name (for jvm_mysql_* caps)
//   CONFORMANCE_JVM_TABLE — MySQL table name (for jvm_mysql_* caps)
//   KUBECONFIG (or in-cluster) for the dynamic client
//
// Observe currently only asserts the JVMChaos CR exists. The per-cap
// trace / metric probes are wired in by a later step (see
// `tools/capgen/output/conformance_cases.json`).
func TestJVMConformance(t *testing.T) {
	ns := os.Getenv("CONFORMANCE_NAMESPACE")
	app := os.Getenv("CONFORMANCE_JVM_APP")
	class := os.Getenv("CONFORMANCE_JVM_CLASS")
	method := os.Getenv("CONFORMANCE_JVM_METHOD")
	if ns == "" || app == "" || class == "" || method == "" {
		t.Skip("CONFORMANCE_NAMESPACE / CONFORMANCE_JVM_APP / CONFORMANCE_JVM_CLASS / CONFORMANCE_JVM_METHOD not set")
	}
	db := os.Getenv("CONFORMANCE_JVM_DB")
	table := os.Getenv("CONFORMANCE_JVM_TABLE")

	cfg, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		t.Fatalf("load kubeconfig: %v", err)
	}
	rc, err := clientcmd.NewNonInteractiveClientConfig(*cfg, "", nil, nil).ClientConfig()
	if err != nil {
		t.Fatalf("client config: %v", err)
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		t.Fatalf("dynamic client: %v", err)
	}

	exec := chaos.NewChaosMeshExecutor(dyn)
	h := NewHarness(exec)
	h.ObserveWait = 60 * time.Second
	h.DestroyWait = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	methodTarget := map[string]any{
		"namespace": ns, "app": app, "class": class, "method": method,
	}
	appOnly := map[string]any{"namespace": ns, "app": app}
	mysqlTarget := map[string]any{
		"namespace": ns, "app": app,
		"db_name": db, "table": table, "sql_type": "all",
	}

	cases := []struct {
		capability string
		target     map[string]any
		params     map[string]any
		needMySQL  bool
	}{
		{"jvm_cpu_stress", methodTarget, map[string]any{"cpu_count": 1, "duration_s": 30}, false},
		{"jvm_gc", appOnly, map[string]any{"duration_s": 30}, false},
		{"jvm_memory_stress", methodTarget, map[string]any{"memory_type": "heap", "duration_s": 30}, false},
		{"jvm_method_exception", methodTarget, map[string]any{"duration_s": 30}, false},
		{"jvm_method_latency", methodTarget, map[string]any{"delay_ms": 500, "duration_s": 30}, false},
		{"jvm_method_return", methodTarget, map[string]any{"return_type": "string", "duration_s": 30}, false},
		{"jvm_mysql_exception", mysqlTarget, map[string]any{"mysql_connector": "8", "duration_s": 30}, true},
		{"jvm_mysql_latency", mysqlTarget, map[string]any{"delay_ms": 500, "mysql_connector": "8", "duration_s": 30}, true},
	}

	gvr := chaos.ChaosMeshGroupVersionResourceForJVMChaos()

	for _, tc := range cases {
		tc := tc
		t.Run(tc.capability, func(t *testing.T) {
			if tc.needMySQL && (db == "" || table == "") {
				t.Skip("CONFORMANCE_JVM_DB / CONFORMANCE_JVM_TABLE not set")
			}
			idempotencyKey := "conformance-" + tc.capability + "-" + ns + "-" + app

			c := Case{
				Capability:     tc.capability,
				IdempotencyKey: idempotencyKey,
				Namespace:      ns,
				Target:         tc.target,
				Params:         tc.params,
				Observe: func(ctx context.Context) error {
					prefix := jvmHandlePrefixFor(tc.capability)
					name, err := chaos.DeriveChaosMeshCRName(prefix, idempotencyKey)
					if err != nil {
						return err
					}
					_, err = dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					return err
				},
				PostDestroy: func(ctx context.Context) error {
					prefix := jvmHandlePrefixFor(tc.capability)
					name, _ := chaos.DeriveChaosMeshCRName(prefix, idempotencyKey)
					_, err := dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					if err == nil {
						return errJVMChaosStillPresent
					}
					return nil
				},
			}

			r := h.Run(ctx, c)
			if !r.Passed() {
				t.Fatalf("conformance failed: %+v", r)
			}
		})
	}
}

func jvmHandlePrefixFor(capability string) string {
	switch capability {
	case "jvm_cpu_stress":
		return "aegis-jvmcpu"
	case "jvm_gc":
		return "aegis-jvmgc"
	case "jvm_memory_stress":
		return "aegis-jvmmem"
	case "jvm_method_exception":
		return "aegis-jvmexc"
	case "jvm_method_latency":
		return "aegis-jvmlat"
	case "jvm_method_return":
		return "aegis-jvmret"
	case "jvm_mysql_exception":
		return "aegis-jvmmysqlexc"
	case "jvm_mysql_latency":
		return "aegis-jvmmysqllat"
	}
	return "aegis-jvm"
}

const errJVMChaosStillPresent = errStr("JVMChaos CR still present after Destroy")
