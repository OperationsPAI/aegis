package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestReconcileDir_FailedImportSkipsItsSystemSweep: when one system has a
// failing import, that system's active set is no longer authoritative, so its
// sweep must be skipped — while a cleanly-imported system is still swept.
func TestReconcileDir_FailedImportSkipsItsSystemSweep(t *testing.T) {
	resetManifestTestState(t)

	dir := t.TempDir()
	writeManifest(t, filepath.Join(dir, "ts-frontend.yaml"), "ts", "frontend")
	writeManifest(t, filepath.Join(dir, "media-cart.yaml"), "media", "cart")

	var (
		mu          sync.Mutex
		sweptSystem = map[string]bool{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/points/import"):
			if strings.Contains(r.URL.Path, "/systems/media/") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"boom"}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"upserted":1,"superseded":0,"point_ids":["aaa"]}}`))
		case strings.HasSuffix(r.URL.Path, "/points/sweep"):
			mu.Lock()
			if i := strings.Index(r.URL.Path, "/systems/"); i >= 0 {
				rest := r.URL.Path[i+len("/systems/"):]
				sys := rest[:strings.Index(rest, "/")]
				sweptSystem[sys] = true
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{"data":{"deprecated":3}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("AEGIS_CHAOS_SERVER", srv.URL)

	var rc int
	stdout, err := captureStdout(t, func() error {
		rc = executeArgs([]string{"manifest", "reconcile-dir", "--output", "json", dir})
		return nil
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if rc == 0 {
		t.Fatalf("expected non-zero exit because media import failed, got 0")
	}

	mu.Lock()
	defer mu.Unlock()
	if !sweptSystem["ts"] {
		t.Errorf("ts imported cleanly and must be swept; swept=%v", sweptSystem)
	}
	if sweptSystem["media"] {
		t.Errorf("media import failed; its sweep must be skipped, swept=%v", sweptSystem)
	}
	if !strings.Contains(stdout, `"system": "media"`) || !strings.Contains(stdout, `"swept": false`) {
		t.Errorf("summary must report media as not swept; got: %s", stdout)
	}
}

// TestGroupManifestsByService_MergesSiblingFiles pins the #505 reconcile fix:
// manifestgen emits one file per capability family for a service, all sharing
// metadata and replace_scope=service. Imported separately each file's
// service-scope supersede clobbered its siblings' points, leaving whole
// families (network/dns/jvm_runtime_mutator) 0-active. reconcile-dir now merges
// every file for the same (system, service, instance, chart_version) into one
// import request so the service-scope supersede sees the union of points.
func TestGroupManifestsByService_MergesSiblingFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	const meta = `apiVersion: aegis-chaos/v1beta
kind: PointManifest
metadata:
  system: "hs"
  service: "frontend"
  instance: "seed"
  chart_version: "seed-genesis"
spec:
  replace_scope: "service"
  points:
`
	write("frontend.yaml", meta+`    - capability: "pod_kill"
      target: {namespace: "hs", app: "frontend"}
`)
	write("frontend-network-A1b.yaml", meta+`    - capability: "network_delay"
      target: {namespace: "hs", source_app: "frontend", target_service: "user"}
`)
	write("attractions.yaml", `apiVersion: aegis-chaos/v1beta
kind: PointManifest
metadata: {system: "hs", service: "attractions", instance: "seed", chart_version: "seed-genesis"}
spec:
  replace_scope: "service"
  points:
    - capability: "pod_kill"
      target: {namespace: "hs", app: "attractions"}
`)

	var files []string
	for _, n := range []string{"frontend.yaml", "frontend-network-A1b.yaml", "attractions.yaml"} {
		files = append(files, filepath.Join(dir, n))
	}

	skipped := 0
	groups, fails := groupManifestsByService(files, "", &skipped)
	if len(fails) != 0 {
		t.Fatalf("unexpected classify failures: %v", fails)
	}
	if skipped != 0 {
		t.Fatalf("unexpected skipped=%d", skipped)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 service groups, got %d", len(groups))
	}

	byService := map[string][]string{}
	for _, g := range groups {
		spec := g.req.GetSpec()
		caps := make([]string, 0, len(spec.GetPoints()))
		for _, p := range spec.GetPoints() {
			caps = append(caps, p.GetCapability())
		}
		md := g.req.GetMetadata()
		byService[md.GetService()] = caps
	}

	front := byService["frontend"]
	hasNetwork, hasPod := false, false
	for _, c := range front {
		switch c {
		case "network_delay":
			hasNetwork = true
		case "pod_kill":
			hasPod = true
		}
	}
	if len(front) != 2 || !hasNetwork || !hasPod {
		t.Fatalf("frontend group must union both files (pod_kill + network_delay); got %v", front)
	}
	if caps := byService["attractions"]; len(caps) != 1 {
		t.Fatalf("attractions group must stay separate with 1 point; got %v", caps)
	}
}

func writeManifest(t *testing.T, path, system, service string) {
	t.Helper()
	doc := `apiVersion: aegis-chaos/v1beta
kind: PointManifest
metadata:
  system: ` + system + `
  service: ` + service + `
  instance: default
  chart_version: v1.0.0
spec:
  replace_scope: none
  points:
    - capability: pod_kill
      target: {}
`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
