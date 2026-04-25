package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/cmd/aegisctl/output"
)

// TestInjectCandidatesLsJSONOutput pins the agent-facing JSON contract: the
// CLI emits the candidates slice verbatim (not the {count, candidates}
// envelope) so callers can pipe straight into jq / Python without unwrapping.
func TestInjectCandidatesLsJSONOutput(t *testing.T) {
	// Backend stub that returns three candidates with mixed leaf shapes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/systems/by-name/sockshop/inject-candidates" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("namespace"); got != "sockshop1" {
			t.Errorf("namespace query = %q, want sockshop1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","data":{"count":3,"candidates":[
			{"system":"sockshop","namespace":"sockshop1","app":"frontend","chaos_type":"PodKill"},
			{"system":"sockshop","namespace":"sockshop1","app":"frontend","chaos_type":"HTTPRequestAbort","route":"/api","http_method":"POST"},
			{"system":"sockshop","namespace":"sockshop1","app":"frontend","chaos_type":"JVMLatency","class":"com.x.A","method":"doIt"}
		]}}`))
	}))
	defer srv.Close()

	oldServer, oldToken, oldOutput := flagServer, flagToken, flagOutput
	t.Cleanup(func() { flagServer, flagToken, flagOutput = oldServer, oldToken, oldOutput })
	flagServer = srv.URL
	flagToken = "test-token"
	flagOutput = string(output.FormatJSON)

	candidatesSystem = "sockshop"
	candidatesNamespace = "sockshop1"
	t.Cleanup(func() {
		candidatesSystem = ""
		candidatesNamespace = ""
	})

	got, runErr := captureStdout(t, func() error {
		return injectCandidatesLsCmd.RunE(injectCandidatesLsCmd, nil)
	})
	if runErr != nil {
		t.Fatalf("injectCandidatesLsCmd.RunE: %v", runErr)
	}

	// Must parse as JSON ARRAY of three candidates (not the envelope).
	var arr []map[string]any
	if err := json.Unmarshal([]byte(got), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, got)
	}
	if len(arr) != 3 {
		t.Fatalf("want 3 candidates, got %d", len(arr))
	}
	if arr[0]["chaos_type"] != "PodKill" {
		t.Errorf("first candidate chaos_type = %v, want PodKill", arr[0]["chaos_type"])
	}
	if arr[1]["route"] != "/api" || arr[1]["http_method"] != "POST" {
		t.Errorf("HTTP candidate has wrong target fields: %+v", arr[1])
	}
	if arr[2]["class"] != "com.x.A" || arr[2]["method"] != "doIt" {
		t.Errorf("JVM candidate has wrong target fields: %+v", arr[2])
	}
}

func TestInjectCandidatesLsRequiresFlags(t *testing.T) {
	candidatesSystem = ""
	candidatesNamespace = ""
	t.Cleanup(func() {
		candidatesSystem = ""
		candidatesNamespace = ""
	})

	if err := injectCandidatesLsCmd.RunE(injectCandidatesLsCmd, nil); err == nil {
		t.Fatal("expected error when --system/--namespace are missing")
	} else if !strings.Contains(err.Error(), "--system is required") {
		t.Errorf("error should mention --system; got %v", err)
	}

	candidatesSystem = "sockshop"
	if err := injectCandidatesLsCmd.RunE(injectCandidatesLsCmd, nil); err == nil {
		t.Fatal("expected error when --namespace is missing")
	} else if !strings.Contains(err.Error(), "--namespace is required") {
		t.Errorf("error should mention --namespace; got %v", err)
	}
}

