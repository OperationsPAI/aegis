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

// TestPodKillConformance is the §9 conformance test for the pod_kill
// Capability against the Chaos-Mesh executor. Build-tag-gated so it
// runs only against a real kind / VKE cluster.
//
// Required env:
//   CONFORMANCE_NAMESPACE — namespace whose pods can be safely killed
//   CONFORMANCE_APP       — app label that selects one or more pods
//   KUBECONFIG (or in-cluster) for the dynamic client
//
// To run:
//   go test -tags chaos_conformance -run TestPodKillConformance \
//       -v ./crud/chaos/conformance/...
func TestPodKillConformance(t *testing.T) {
	ns := os.Getenv("CONFORMANCE_NAMESPACE")
	app := os.Getenv("CONFORMANCE_APP")
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c := Case{
		Capability:     "pod_kill",
		IdempotencyKey: "conformance-pod-kill-" + ns + "-" + app,
		Target:         map[string]any{"namespace": ns, "app": app},
		Params:         map[string]any{"duration_s": 30},
		Observe: func(ctx context.Context) error {
			// Minimal contract: a PodChaos CR with our derived name exists
			// in the target namespace. Full contract (pod.restart_count
			// increases) lives behind a real observability tap.
			name, err := chaos.DeriveChaosMeshCRName("pod-kill", "conformance-pod-kill-"+ns+"-"+app)
			if err != nil {
				return err
			}
			gvr := chaos.ChaosMeshGroupVersionResourceForPodChaos()
			_, err = dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			return err
		},
		PostDestroy: func(ctx context.Context) error {
			name, _ := chaos.DeriveChaosMeshCRName("pod-kill", "conformance-pod-kill-"+ns+"-"+app)
			gvr := chaos.ChaosMeshGroupVersionResourceForPodChaos()
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
}

type errStr string

func (e errStr) Error() string { return string(e) }

const errPodChaosStillPresent = errStr("PodChaos CR still present after Destroy")
