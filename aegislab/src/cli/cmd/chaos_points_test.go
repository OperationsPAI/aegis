package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"
)

// TestChaosPointsList_FiltersForwarded asserts every CLI flag becomes the
// matching query-string param on GET /v1beta/systems/{sys}/points. A
// regression here would silently widen the listing — a real risk because
// the SDK builder swallows unknown query names without erroring.
func TestChaosPointsList_FiltersForwarded(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotQuery  url.Values
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{` +
			`"points":[{"id":"abc1234567890def","system_name":"otel-demo","service_name":"cart","capability_name":"pod_kill","status":"active","source":"import","target":{"app":"cart","namespace":"otel-demo"},"created_at":"2026-05-19T00:00:00Z","updated_at":"2026-05-19T00:00:00Z"}],` +
			`"total":1,"limit":50,"offset":10}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	chaosPointsSystem = "otel-demo"
	chaosPointsService = "cart"
	chaosPointsCapability = "pod_kill"
	chaosPointsStatus = "active"
	chaosPointsLimit = 50
	chaosPointsOffset = 10

	if err := runChaosPointsList(nil, nil); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q; want GET", gotMethod)
	}
	if gotPath != "/v1beta/systems/otel-demo/points" {
		t.Errorf("path = %q; want /v1beta/systems/otel-demo/points", gotPath)
	}
	want := map[string]string{
		"service":    "cart",
		"capability": "pod_kill",
		"status":     "active",
		"limit":      "50",
		"offset":     "10",
	}
	for k, v := range want {
		if got := gotQuery.Get(k); got != v {
			t.Errorf("query[%q] = %q; want %q", k, got, v)
		}
	}
}

// TestChaosPointsList_EmptyResult asserts the renderer copes with an
// empty points list (no panic on nil-deref of Total/Limit/Offset).
func TestChaosPointsList_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{"points":[],"total":0,"limit":100,"offset":0}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()
	chaosPointsSystem = "empty-sys"

	if err := runChaosPointsList(nil, nil); err != nil {
		t.Fatalf("empty list failed: %v", err)
	}
}

// TestChaosPointsList_RequiresSystem guards the precondition check —
// system must be set before reaching the SDK.
func TestChaosPointsList_RequiresSystem(t *testing.T) {
	chaosTestSetup(t, "http://127.0.0.1:1")
	defer resetChaosFlags()
	if err := runChaosPointsList(nil, nil); err == nil {
		t.Fatal("expected error when --system missing")
	}
}

// TestChaosPointsExport_WritesOneFilePerService asserts the export command
// hits the right path, forwards include_superseded, and writes one valid
// PointManifest YAML per manifest in the server response. Critically: each
// generated YAML must round-trip through `aegisctl manifest import` —
// covered structurally here by re-parsing the YAML with the same PointManifest
// schema the import path uses.
func TestChaosPointsExport_WritesOneFilePerService(t *testing.T) {
	var (
		gotPath  string
		gotQuery url.Values
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{"manifests":[
		  {"apiVersion":"aegis-chaos/v1beta","kind":"PointManifest",
		   "metadata":{"system":"otel-demo","service":"cart","instance":"default","chart_version":"v1"},
		   "spec":{"replace_scope":"service","points":[
		     {"capability":"http_response_abort","target":{"namespace":"otel-demo","app":"cart","method":"GET","path":"/cart","port":8080}}
		   ]}},
		  {"apiVersion":"aegis-chaos/v1beta","kind":"PointManifest",
		   "metadata":{"system":"otel-demo","service":"checkout","instance":"default","chart_version":"v1"},
		   "spec":{"replace_scope":"service","points":[
		     {"capability":"network_delay","target":{"namespace":"otel-demo","source_app":"checkout","target_service":"payment"}}
		   ]}}
		]}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()
	defer func() {
		chaosPointsExportSystem = ""
		chaosPointsExportIncludeSuperseded = false
	}()

	chaosPointsExportSystem = "otel-demo"
	chaosPointsExportIncludeSuperseded = true

	outDir := t.TempDir()
	if err := runChaosPointsExport(nil, []string{outDir}); err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if gotPath != "/v1beta/systems/otel-demo/points/export" {
		t.Errorf("path = %q; want /v1beta/systems/otel-demo/points/export", gotPath)
	}
	if gotQuery.Get("include_superseded") != "true" {
		t.Errorf("include_superseded forwarding broken: query=%v", gotQuery)
	}

	// Both manifest files must exist and parse as valid PointManifest YAML
	// matching the import-side schema.
	for _, svc := range []string{"cart", "checkout"} {
		p := filepath.Join(outDir, "otel-demo", svc+"-export.yaml")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var m struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Metadata   struct {
				System, Service, Instance, ChartVersion string
			} `yaml:"metadata"`
			Spec struct {
				ReplaceScope string `yaml:"replace_scope"`
				Points       []struct {
					Capability string                 `yaml:"capability"`
					Target     map[string]interface{} `yaml:"target"`
				}
			} `yaml:"spec"`
		}
		if err := sigsyaml.Unmarshal(raw, &m); err != nil {
			t.Fatalf("parse %s: %v\n%s", p, err, string(raw))
		}
		if m.APIVersion != "aegis-chaos/v1beta" || m.Kind != "PointManifest" {
			t.Errorf("%s: bad envelope apiVersion=%q kind=%q", p, m.APIVersion, m.Kind)
		}
		if m.Metadata.System != "otel-demo" || m.Metadata.Service != svc {
			t.Errorf("%s: metadata mismatch %+v", p, m.Metadata)
		}
		if len(m.Spec.Points) == 0 {
			t.Errorf("%s: spec.points empty", p)
		}
	}
}

// TestChaosPointsExport_RequiresSystem mirrors the list-cmd guard so
// invocations without --system fail before hitting the server.
func TestChaosPointsExport_RequiresSystem(t *testing.T) {
	chaosTestSetup(t, "http://127.0.0.1:1")
	defer resetChaosFlags()
	defer func() { chaosPointsExportSystem = "" }()

	if err := runChaosPointsExport(nil, []string{t.TempDir()}); err == nil {
		t.Fatal("expected error when --system missing")
	}
}
