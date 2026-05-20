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

// TestHTTPConformance runs DeriveHandle → Apply → Status → Destroy for
// each of the 9 HTTPChaos capabilities against a real cluster.
//
// Required env:
//   CONFORMANCE_NAMESPACE — namespace that hosts the target app
//   CONFORMANCE_HTTP_APP — `app` label of the proxied pod
//   CONFORMANCE_HTTP_PORT — listening port (default 8080)
//   CONFORMANCE_HTTP_METHOD — request method filter (default GET)
//   CONFORMANCE_HTTP_PATH — path filter (default /)
//
// Observe currently only asserts the HTTPChaos CR exists. The full
// per-capability probe (span.duration for delay, error_rate for abort,
// etc.) is captured by `tools/capgen/output/conformance_cases.json` and
// wired in by a subsequent step.
func TestHTTPConformance(t *testing.T) {
	ns := os.Getenv("CONFORMANCE_NAMESPACE")
	app := os.Getenv("CONFORMANCE_HTTP_APP")
	if ns == "" || app == "" {
		t.Skip("CONFORMANCE_NAMESPACE / CONFORMANCE_HTTP_APP not set")
	}
	port := os.Getenv("CONFORMANCE_HTTP_PORT")
	if port == "" {
		port = "8080"
	}
	method := os.Getenv("CONFORMANCE_HTTP_METHOD")
	if method == "" {
		method = "GET"
	}
	path := os.Getenv("CONFORMANCE_HTTP_PATH")
	if path == "" {
		path = "/"
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	portInt := atoiOrPanic(port)

	cases := []struct {
		capability string
		params     map[string]any
	}{
		{"http_request_abort", map[string]any{"duration_s": 30}},
		{"http_request_delay", map[string]any{"delay_ms": 250, "duration_s": 30}},
		{"http_request_replace_method", map[string]any{"new_method": "POST", "duration_s": 30}},
		{"http_request_replace_path", map[string]any{"new_path": "/aegis-replaced", "duration_s": 30}},
		{"http_response_abort", map[string]any{"duration_s": 30}},
		{"http_response_delay", map[string]any{"delay_ms": 250, "duration_s": 30}},
		{"http_response_patch_body", map[string]any{"duration_s": 30}},
		{"http_response_replace_body", map[string]any{"body_type": "empty", "duration_s": 30}},
		{"http_response_replace_code", map[string]any{"status_code": 503, "duration_s": 30}},
	}

	gvr := chaos.ChaosMeshGroupVersionResourceForHTTPChaos()

	for _, tc := range cases {
		tc := tc
		t.Run(tc.capability, func(t *testing.T) {
			idempotencyKey := "conformance-" + tc.capability + "-" + ns + "-" + app
			target := map[string]any{
				"namespace": ns,
				"app":       app,
				"port":      portInt,
				"method":    method,
				"path":      path,
			}

			c := Case{
				Capability:     tc.capability,
				IdempotencyKey: idempotencyKey,
				Namespace:      ns,
				Target:         target,
				Params:         tc.params,
				Observe: func(ctx context.Context) error {
					prefix := httpHandlePrefixFor(tc.capability)
					name, err := chaos.DeriveChaosMeshCRName(prefix, idempotencyKey)
					if err != nil {
						return err
					}
					_, err = dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					return err
				},
				PostDestroy: func(ctx context.Context) error {
					prefix := httpHandlePrefixFor(tc.capability)
					name, _ := chaos.DeriveChaosMeshCRName(prefix, idempotencyKey)
					_, err := dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					if err == nil {
						return errHTTPChaosStillPresent
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

func httpHandlePrefixFor(capability string) string {
	switch capability {
	case "http_request_abort":
		return "aegis-httpreqabort"
	case "http_request_delay":
		return "aegis-httpreqdelay"
	case "http_request_replace_method":
		return "aegis-httpreqmethod"
	case "http_request_replace_path":
		return "aegis-httpreqpath"
	case "http_response_abort":
		return "aegis-httprespabort"
	case "http_response_delay":
		return "aegis-httprespdelay"
	case "http_response_patch_body":
		return "aegis-httprespatch"
	case "http_response_replace_body":
		return "aegis-httprespbody"
	case "http_response_replace_code":
		return "aegis-httprespcode"
	}
	return "aegis-http"
}

func atoiOrPanic(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			panic("CONFORMANCE_HTTP_PORT must be numeric: " + s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

const errHTTPChaosStillPresent = errStr("HTTPChaos CR still present after Destroy")
