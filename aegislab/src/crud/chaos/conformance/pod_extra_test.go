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

// TestPodChaosExtraConformance covers container_kill + pod_failure.
//
// Required env:
//   CONFORMANCE_NAMESPACE — namespace whose pods are safe to perturb
//   CONFORMANCE_APP       — app label that selects one or more pods
//   CONFORMANCE_CONTAINER — container name (required for container_kill)
func TestPodChaosExtraConformance(t *testing.T) {
	ns := os.Getenv("CONFORMANCE_NAMESPACE")
	app := os.Getenv("CONFORMANCE_APP")
	container := os.Getenv("CONFORMANCE_CONTAINER")
	if ns == "" || app == "" {
		t.Skip("CONFORMANCE_NAMESPACE / CONFORMANCE_APP not set")
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cases := []struct {
		capability string
		prefix     string
		target     map[string]any
	}{
		{"pod_failure", "aegis-podfail", map[string]any{"namespace": ns, "app": app}},
	}
	if container != "" {
		cases = append(cases, struct {
			capability string
			prefix     string
			target     map[string]any
		}{
			"container_kill", "aegis-ctnkill",
			map[string]any{"namespace": ns, "app": app, "container": container},
		})
	}

	gvr := chaos.ChaosMeshGroupVersionResourceForPodChaos()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.capability, func(t *testing.T) {
			key := "conformance-" + tc.capability + "-" + ns + "-" + app
			c := Case{
				Capability:     tc.capability,
				IdempotencyKey: key,
				Target:         tc.target,
				Params:         map[string]any{"duration_s": 30},
				Observe: func(ctx context.Context) error {
					name, err := chaos.DeriveChaosMeshCRName(tc.prefix, key)
					if err != nil {
						return err
					}
					_, err = dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					return err
				},
				PostDestroy: func(ctx context.Context) error {
					name, _ := chaos.DeriveChaosMeshCRName(tc.prefix, key)
					_, err := dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					if err == nil {
						return errPodChaosStillPresent
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
