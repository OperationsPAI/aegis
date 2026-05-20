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

// TestStressConformance covers cpu_stress + memory_stress.
//
// Required env:
//   CONFORMANCE_NAMESPACE / CONFORMANCE_APP / CONFORMANCE_CONTAINER
func TestStressConformance(t *testing.T) {
	ns := os.Getenv("CONFORMANCE_NAMESPACE")
	app := os.Getenv("CONFORMANCE_APP")
	container := os.Getenv("CONFORMANCE_CONTAINER")
	if ns == "" || app == "" || container == "" {
		t.Skip("CONFORMANCE_NAMESPACE / CONFORMANCE_APP / CONFORMANCE_CONTAINER not set")
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

	target := map[string]any{"namespace": ns, "app": app, "container": container}
	cases := []struct {
		capability string
		prefix     string
		params     map[string]any
	}{
		{"cpu_stress", "aegis-cpustress", map[string]any{"load_pct": 50, "workers": 1, "duration_s": 30}},
		{"memory_stress", "aegis-memstress", map[string]any{"size_mib": 64, "workers": 1, "duration_s": 30}},
	}
	gvr := chaos.ChaosMeshGroupVersionResourceForStressChaos()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.capability, func(t *testing.T) {
			key := "conformance-" + tc.capability + "-" + ns + "-" + app
			c := Case{
				Capability:     tc.capability,
				IdempotencyKey: key,
				Namespace:      ns,
				Target:         target,
				Params:         tc.params,
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
						return errStressChaosStillPresent
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

const errStressChaosStillPresent = errStr("StressChaos CR still present after Destroy")
