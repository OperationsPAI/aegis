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

func TestTimeSkewConformance(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	key := "conformance-time-skew-" + ns + "-" + app
	gvr := chaos.ChaosMeshGroupVersionResourceForTimeChaos()
	c := Case{
		Capability:     "time_skew",
		IdempotencyKey: key,
		Target:         map[string]any{"namespace": ns, "app": app, "container": container},
		Params:         map[string]any{"offset_s": 60, "duration_s": 30},
		Observe: func(ctx context.Context) error {
			name, err := chaos.DeriveChaosMeshCRName("aegis-timeskew", key)
			if err != nil {
				return err
			}
			_, err = dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			return err
		},
		PostDestroy: func(ctx context.Context) error {
			name, _ := chaos.DeriveChaosMeshCRName("aegis-timeskew", key)
			_, err := dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			if err == nil {
				return errTimeChaosStillPresent
			}
			return nil
		},
	}
	r := h.Run(ctx, c)
	if !r.Passed() {
		t.Fatalf("conformance failed: %+v", r)
	}
}

const errTimeChaosStillPresent = errStr("TimeChaos CR still present after Destroy")
