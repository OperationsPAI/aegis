package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/cli/config"
)

func TestChaosSystemRegister_PUTsCorrectPayload(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
		gotAuth   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{` +
			`"name":"otel-demo","ns_pattern":"otel-demo",` +
			`"app_label_key":"app.kubernetes.io/name","enabled":true,` +
			`"max_concurrent_injections":5}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	chaosSysName = "otel-demo"
	chaosSysNsPattern = "otel-demo"
	chaosSysAppLabelKey = "app.kubernetes.io/name"
	chaosSysEnabled = true
	chaosSysMaxConc = 0

	if err := runChaosSystemRegister(nil, nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q; want PUT", gotMethod)
	}
	if gotPath != "/v1beta/systems/otel-demo" {
		t.Errorf("path = %q; want /v1beta/systems/otel-demo", gotPath)
	}
	if gotBody["ns_pattern"] != "otel-demo" {
		t.Errorf("ns_pattern = %v; want otel-demo", gotBody["ns_pattern"])
	}
	if gotBody["app_label_key"] != "app.kubernetes.io/name" {
		t.Errorf("app_label_key = %v; want app.kubernetes.io/name", gotBody["app_label_key"])
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization = %q; want Bearer …", gotAuth)
	}
}

func TestChaosSystemGet_NotFound_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":404,"message":"chaos: system not found"}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	err := runChaosSystemGet(nil, []string{"missing"})
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

// chaosTestSetup prepares the package-level globals so tests can call the
// run* funcs directly without going through cobra (which would otherwise
// LoadConfig + try to expand a real context).
func chaosTestSetup(t *testing.T, srvURL string) {
	t.Helper()
	cfg = &config.Config{}
	flagChaosServer = srvURL
	flagToken = "test-bearer"
	flagOutput = "json"
}

func resetChaosFlags() {
	flagChaosServer = ""
	flagToken = ""
	flagOutput = ""
	chaosSysName = ""
	chaosSysNsPattern = ""
	chaosSysAppLabelKey = ""
	chaosSysEnabled = false
	chaosSysMaxConc = 0
	chaosInjectPointID = ""
	chaosInjectParams = ""
	chaosInjectIdemKey = ""
	chaosInjectCallerMeta = ""
	chaosInjectExecutor = ""
}
