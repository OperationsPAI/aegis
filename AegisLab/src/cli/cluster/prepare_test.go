package cluster

import (
	"context"
	"testing"
)

func TestLocalE2EPrepareRunner_DryRunDoesNotMutate(t *testing.T) {
	env := &fakeEnv{
		cfg: Config{K8sNamespace: "exp", ServiceAccount: "rcabench-sa", ExperimentPVC: "rcabench-juicefs-experiment-storage"},
		k8s: &fakeK8s{
			namespaces:      map[string]bool{},
			serviceAccounts: map[string]bool{},
			pvcs:            map[string]struct{ exists, bound bool }{},
		},
		etd: &fakeEtcd{values: map[string]string{
			"/rcabench/config/consumer/rate_limiting.max_concurrent_restarts": "2",
		}},
	}

	runner := LocalE2EPrepareRunner()
	results, err := runner.Run(context.Background(), env, false)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected prepare results")
	}

	assertPrepareOutcome(t, results, "k8s.namespace", PrepareCreate, false)
	assertPrepareOutcome(t, results, "k8s.service-account", PrepareCreate, false)
	assertPrepareOutcome(t, results, "k8s.experiment-pvc", PrepareCreate, false)
	assertPrepareOutcome(t, results, "etcd.rcabench.config.consumer.rate_limiting.max_concurrent_restarts", PrepareUpdate, false)

	if env.k8s.namespaces["exp"] {
		t.Fatalf("dry-run should not create namespace")
	}
	if env.k8s.serviceAccounts["exp/rcabench-sa"] {
		t.Fatalf("dry-run should not create service account")
	}
	if env.k8s.pvcs["exp/rcabench-juicefs-experiment-storage"].exists {
		t.Fatalf("dry-run should not create PVC")
	}
	if got := env.etd.values["/rcabench/config/consumer/rate_limiting.max_concurrent_restarts"]; got != "2" {
		t.Fatalf("dry-run mutated etcd value to %q", got)
	}
	if env.etd.puts["/rcabench/config/consumer/rate_limiting.max_concurrent_restarts"] != 0 {
		t.Fatalf("dry-run should not write etcd")
	}
}

func TestLocalE2EPrepareRunner_ApplyIsIdempotent(t *testing.T) {
	env := &fakeEnv{
		cfg: Config{K8sNamespace: "exp", ServiceAccount: "rcabench-sa", ExperimentPVC: "rcabench-juicefs-experiment-storage"},
		k8s: &fakeK8s{
			namespaces:      map[string]bool{},
			serviceAccounts: map[string]bool{},
			pvcs:            map[string]struct{ exists, bound bool }{},
		},
		etd: &fakeEtcd{values: map[string]string{
			"/rcabench/config/consumer/rate_limiting.max_concurrent_restarts": "2",
		}},
	}

	runner := LocalE2EPrepareRunner()
	first, err := runner.Run(context.Background(), env, true)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	assertPrepareOutcome(t, first, "k8s.namespace", PrepareCreate, true)
	assertPrepareOutcome(t, first, "k8s.service-account", PrepareCreate, true)
	assertPrepareOutcome(t, first, "k8s.experiment-pvc", PrepareCreate, true)
	assertPrepareOutcome(t, first, "etcd.rcabench.config.consumer.rate_limiting.max_concurrent_restarts", PrepareUpdate, true)

	if !env.k8s.namespaces["exp"] {
		t.Fatalf("apply should create namespace")
	}
	if !env.k8s.serviceAccounts["exp/rcabench-sa"] {
		t.Fatalf("apply should create service account")
	}
	if !env.k8s.pvcs["exp/rcabench-juicefs-experiment-storage"].exists {
		t.Fatalf("apply should create PVC")
	}
	if got := env.etd.values["/rcabench/config/consumer/rate_limiting.max_concurrent_restarts"]; got != "5" {
		t.Fatalf("apply should update etcd value, got %q", got)
	}

	second, err := runner.Run(context.Background(), env, true)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	for _, res := range second {
		if res.Outcome != PrepareSkip {
			t.Fatalf("second run should be idempotent, got %s for %s (%s)", res.Outcome, res.ID, res.Detail)
		}
		if res.Applied {
			t.Fatalf("skip result should not report applied=true on second run for %s", res.ID)
		}
	}
}

func assertPrepareOutcome(t *testing.T, results []PrepareResult, id string, wantOutcome PrepareOutcome, wantApplied bool) {
	t.Helper()
	for _, res := range results {
		if res.ID != id {
			continue
		}
		if res.Outcome != wantOutcome {
			t.Fatalf("%s outcome = %s, want %s (detail=%q)", id, res.Outcome, wantOutcome, res.Detail)
		}
		if res.Applied != wantApplied {
			t.Fatalf("%s applied = %v, want %v", id, res.Applied, wantApplied)
		}
		return
	}
	t.Fatalf("missing result for %s", id)
}
