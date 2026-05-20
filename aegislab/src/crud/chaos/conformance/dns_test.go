//go:build chaos_conformance

package conformance

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"aegis/crud/chaos"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

// TestDNSConformance covers dns_error + dns_random.
//
// Required env:
//   CONFORMANCE_NAMESPACE, CONFORMANCE_APP
//   CONFORMANCE_DNS_PATTERNS — comma-separated glob list (e.g. "*.example.com,foo.svc")
func TestDNSConformance(t *testing.T) {
	ns := os.Getenv("CONFORMANCE_NAMESPACE")
	app := os.Getenv("CONFORMANCE_APP")
	patternsRaw := os.Getenv("CONFORMANCE_DNS_PATTERNS")
	if ns == "" || app == "" || patternsRaw == "" {
		t.Skip("CONFORMANCE_NAMESPACE / CONFORMANCE_APP / CONFORMANCE_DNS_PATTERNS not set")
	}
	patterns := make([]any, 0)
	for _, p := range strings.Split(patternsRaw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			patterns = append(patterns, p)
		}
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

	target := map[string]any{"namespace": ns, "app": app, "domain_patterns": patterns}
	cases := []struct {
		capability string
		prefix     string
	}{
		{"dns_error", "aegis-dnserr"},
		{"dns_random", "aegis-dnsrand"},
	}
	gvr := chaos.ChaosMeshGroupVersionResourceForDNSChaos()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.capability, func(t *testing.T) {
			key := "conformance-" + tc.capability + "-" + ns + "-" + app
			c := Case{
				Capability:     tc.capability,
				IdempotencyKey: key,
				Namespace:      ns,
				Target:         target,
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
						return errDNSChaosStillPresent
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

const errDNSChaosStillPresent = errStr("DNSChaos CR still present after Destroy")
