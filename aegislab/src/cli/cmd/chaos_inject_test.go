package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChaosInjectSubmit_POSTsAcceptedResponse(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"code":202,"message":"Injection accepted","data":{` +
			`"id":"01H0000000000000000000000A","point_id":"p1",` +
			`"status":"running","executor_name":"chaos-mesh",` +
			`"executor_handle":"PodChaos/exp/foo","idempotency_key":"k1"}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	chaosInjectPointID = "p1"
	chaosInjectNamespace = "ns0"
	chaosInjectParams = `{"duration_s":5}`
	chaosInjectIdemKey = "k1"
	chaosInjectCallerMeta = `{"src":"test"}`

	if err := runChaosInjectSubmit(nil, nil); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if gotBody["namespace"] != "ns0" {
		t.Errorf("body.namespace = %v; want ns0", gotBody["namespace"])
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", gotMethod)
	}
	if gotPath != "/v1beta/injections" {
		t.Errorf("path = %q; want /v1beta/injections", gotPath)
	}
	if gotBody["point_id"] != "p1" || gotBody["idempotency_key"] != "k1" {
		t.Errorf("body missing required fields: %v", gotBody)
	}
	params, _ := gotBody["params"].(map[string]any)
	if params["duration_s"] != float64(5) {
		t.Errorf("params.duration_s = %v; want 5", params["duration_s"])
	}
}

func TestChaosInjectDestroy_DELETEReturnsCancelled(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{` +
			`"id":"01H1","status":"cancelled","point_id":"p1",` +
			`"executor_name":"chaos-mesh","idempotency_key":"k1"}}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	if err := runChaosInjectDestroy(nil, []string{"01H1"}); err != nil {
		t.Fatalf("destroy failed: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q; want DELETE", gotMethod)
	}
	if gotPath != "/v1beta/injections/01H1" {
		t.Errorf("path = %q; want /v1beta/injections/01H1", gotPath)
	}
}

func TestChaosInjectGet_ServerError_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":500,"message":"boom"}`))
	}))
	defer srv.Close()

	chaosTestSetup(t, srv.URL)
	defer resetChaosFlags()

	if err := runChaosInjectGet(nil, []string{"missing"}); err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}
