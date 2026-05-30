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
