package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChaosCapabilityList_ParsesCatalog(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":[` +
			`{"name":"pod_kill","status":"stable"},` +
			`{"name":"network_delay","status":"experimental"}` +
			`]}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	if err := runChaosCapabilityList(nil, nil); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if gotPath != "/v1beta/capabilities" {
		t.Errorf("path = %q; want /v1beta/capabilities", gotPath)
	}
}

func TestChaosCapabilityGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":404,"message":"chaos: capability not found"}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	if err := runChaosCapabilityGet(nil, []string{"nonexistent"}); err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}
