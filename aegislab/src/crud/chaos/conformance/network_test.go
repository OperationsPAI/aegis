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

// TestNetworkConformance runs DeriveHandle → Apply → Status → Destroy
// for each of the 6 NetworkChaos capabilities against a real cluster.
//
// Required env:
//   CONFORMANCE_NAMESPACE — namespace that hosts the source/target apps
//   CONFORMANCE_SOURCE_APP — `app` label of the source pod
//   CONFORMANCE_TARGET_SERVICE — `app` label of the callee
//   KUBECONFIG (or in-cluster) for the dynamic client
//
// Observe currently only asserts the NetworkChaos CR exists. The full
// per-capability probe (trace.span.duration_ms for delay, edge error
// rate for loss/corrupt/partition, etc.) is captured by
// `tools/capgen/output/conformance_cases.json` and is wired in by a
// subsequent step.
func TestNetworkConformance(t *testing.T) {
	ns := os.Getenv("CONFORMANCE_NAMESPACE")
	src := os.Getenv("CONFORMANCE_SOURCE_APP")
	dst := os.Getenv("CONFORMANCE_TARGET_SERVICE")
	if ns == "" || src == "" || dst == "" {
		t.Skip("CONFORMANCE_NAMESPACE / CONFORMANCE_SOURCE_APP / CONFORMANCE_TARGET_SERVICE not set")
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cases := []struct {
		capability string
		params     map[string]any
	}{
		{"network_delay", map[string]any{"latency_ms": 100, "jitter_ms": 10, "correlation_pct": 25, "duration_s": 30}},
		{"network_loss", map[string]any{"loss_pct": 25, "correlation_pct": 25, "duration_s": 30}},
		{"network_duplicate", map[string]any{"duplicate_pct": 25, "correlation_pct": 25, "duration_s": 30}},
		{"network_corrupt", map[string]any{"corrupt_pct": 25, "correlation_pct": 25, "duration_s": 30}},
		{"network_bandwidth", map[string]any{"rate_kbps": 1024, "limit": 20480, "buffer": 10240, "duration_s": 30}},
		{"network_partition", map[string]any{"duration_s": 30}},
	}

	gvr := chaos.ChaosMeshGroupVersionResourceForNetworkChaos()

	for _, tc := range cases {
		tc := tc
		t.Run(tc.capability, func(t *testing.T) {
			idempotencyKey := "conformance-" + tc.capability + "-" + ns + "-" + src + "-" + dst
			target := map[string]any{
				"namespace":      ns,
				"source_app":     src,
				"target_service": dst,
				"direction":      "to",
			}

			c := Case{
				Capability:     tc.capability,
				IdempotencyKey: idempotencyKey,
				Namespace:      ns,
				Target:         target,
				Params:         tc.params,
				Observe: func(ctx context.Context) error {
					// TODO: real probe per capgen's conformance_cases.json —
					// delay → trace.span.duration_ms; loss/corrupt/partition →
					// trace.edge.error_or_retry_rate; bandwidth →
					// trace.span.duration_ms with large payloads; duplicate has
					// no robust trace signal.
					prefix := networkHandlePrefixFor(tc.capability)
					name, err := chaos.DeriveChaosMeshCRName(prefix, idempotencyKey)
					if err != nil {
						return err
					}
					_, err = dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					return err
				},
				PostDestroy: func(ctx context.Context) error {
					prefix := networkHandlePrefixFor(tc.capability)
					name, _ := chaos.DeriveChaosMeshCRName(prefix, idempotencyKey)
					_, err := dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					if err == nil {
						return errNetworkChaosStillPresent
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

func networkHandlePrefixFor(capability string) string {
	switch capability {
	case "network_delay":
		return "aegis-netdelay"
	case "network_loss":
		return "aegis-netloss"
	case "network_duplicate":
		return "aegis-netdup"
	case "network_corrupt":
		return "aegis-netcorrupt"
	case "network_bandwidth":
		return "aegis-netbw"
	case "network_partition":
		return "aegis-netpart"
	}
	return "aegis-net"
}

const errNetworkChaosStillPresent = errStr("NetworkChaos CR still present after Destroy")
