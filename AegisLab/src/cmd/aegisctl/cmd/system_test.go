package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aegis/cmd/aegisctl/client"

	"gopkg.in/yaml.v3"
)

// sevenValidConfigs produces a synthetic dynamic_configs snippet with all
// seven injection.system.<name>.* rows correctly typed, using the staging
// data.yaml for `ts` as the model. Callers may drop or corrupt specific
// rows in-place to exercise negative cases.
func sevenValidConfigs(name string) []seedDynamicConfig {
	return []seedDynamicConfig{
		{Key: "injection.system." + name + ".count", DefaultValue: "1", ValueType: 2, Scope: 2, Category: "injection.system.count", Description: "Number of system"},
		{Key: "injection.system." + name + ".ns_pattern", DefaultValue: "^" + name + "\\d+$", ValueType: 0, Scope: 2, Category: "injection.system.ns_pattern"},
		{Key: "injection.system." + name + ".extract_pattern", DefaultValue: "^(" + name + ")(\\d+)$", ValueType: 0, Scope: 2, Category: "injection.system.extract_pattern"},
		{Key: "injection.system." + name + ".display_name", DefaultValue: "Test System", ValueType: 0, Scope: 2, Category: "injection.system.display_name"},
		{Key: "injection.system." + name + ".app_label_key", DefaultValue: "app", ValueType: 0, Scope: 2, Category: "injection.system.app_label_key"},
		{Key: "injection.system." + name + ".is_builtin", DefaultValue: "true", ValueType: 1, Scope: 2, Category: "injection.system.is_builtin"},
		{Key: "injection.system." + name + ".status", DefaultValue: "1", ValueType: 2, Scope: 2, Category: "injection.system.status"},
	}
}

func marshalSeed(t *testing.T, cfgs []seedDynamicConfig) *seedDoc {
	t.Helper()
	// Round-trip through YAML to exercise the real parse path and catch
	// tag-level regressions in seedDoc / seedDynamicConfig.
	doc := seedDoc{DynamicConfigs: cfgs}
	buf, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatalf("marshal synthetic seed: %v", err)
	}
	var parsed seedDoc
	if err := yaml.Unmarshal(buf, &parsed); err != nil {
		t.Fatalf("unmarshal synthetic seed: %v", err)
	}
	return &parsed
}

func TestSystemSeedValidatesCleanly(t *testing.T) {
	doc := marshalSeed(t, sevenValidConfigs("ts"))
	seed, err := extractSystemSeed(doc, "ts")
	if err != nil {
		t.Fatalf("extractSystemSeed: %v", err)
	}
	if err := validateSystemSeed(seed); err != nil {
		t.Fatalf("validateSystemSeed: unexpected error: %v", err)
	}
	if seed.Count != 1 {
		t.Errorf("Count: want 1, got %d", seed.Count)
	}
	if !seed.IsBuiltin {
		t.Errorf("IsBuiltin: want true")
	}
	if seed.Status != 1 {
		t.Errorf("Status: want 1, got %d", seed.Status)
	}
	if seed.DisplayName != "Test System" {
		t.Errorf("DisplayName: want %q, got %q", "Test System", seed.DisplayName)
	}
	if seed.AppLabelKey != "app" {
		t.Errorf("AppLabelKey: want %q, got %q", "app", seed.AppLabelKey)
	}
}

func TestSystemSeedMissingStatusRejected(t *testing.T) {
	cfgs := sevenValidConfigs("ts")
	// Drop the status row (last element).
	cfgs = cfgs[:len(cfgs)-1]
	doc := marshalSeed(t, cfgs)

	seed, err := extractSystemSeed(doc, "ts")
	if err != nil {
		t.Fatalf("extractSystemSeed: %v", err)
	}
	err = validateSystemSeed(seed)
	if err == nil {
		t.Fatal("validateSystemSeed: expected error for missing status, got nil")
	}
	if !strings.Contains(err.Error(), "status") {
		t.Errorf("error should mention missing 'status' key; got: %v", err)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mark the key as missing; got: %v", err)
	}
}

func TestSystemSeedWrongValueTypeRejected(t *testing.T) {
	cfgs := sevenValidConfigs("ts")
	// Corrupt the count row: claim it's a string (0) instead of int (2).
	for i := range cfgs {
		if strings.HasSuffix(cfgs[i].Key, ".count") {
			cfgs[i].ValueType = 0
			break
		}
	}
	doc := marshalSeed(t, cfgs)

	seed, err := extractSystemSeed(doc, "ts")
	if err != nil {
		t.Fatalf("extractSystemSeed: %v", err)
	}
	err = validateSystemSeed(seed)
	if err == nil {
		t.Fatal("validateSystemSeed: expected error for wrong value_type on count, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "injection.system.ts.count") {
		t.Errorf("error should pinpoint the offending key; got: %v", err)
	}
	if !strings.Contains(msg, "value_type") {
		t.Errorf("error should mention value_type; got: %v", err)
	}
}

// fakeAPIResp is a minimal envelope that mirrors client.APIResponse[T] for
// tests — avoids pulling generics-at-the-bench into the fake server.
type fakeAPIResp struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    any            `json:"data,omitempty"`
}

// newFakeSystemServer stubs the two endpoints the enable/disable path hits:
// GET /api/v2/systems (for name→id resolution) and PUT /api/v2/systems/{id}.
// The last PUT body is stored in the returned *capturedPut so tests can
// assert the wire shape.
type capturedPut struct {
	path string
	body map[string]any
}

func newFakeSystemServer(t *testing.T, systems []chaosSystemResp) (*httptest.Server, *capturedPut) {
	t.Helper()
	captured := &capturedPut{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/systems", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		resp := fakeAPIResp{
			Code: 200,
			Data: client.PaginatedData[chaosSystemResp]{
				Items: systems,
				Pagination: client.Pagination{
					Page: 1, Size: len(systems), Total: len(systems), TotalPages: 1,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/v2/systems/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		captured.path = r.URL.Path
		captured.body = map[string]any{}
		_ = json.Unmarshal(raw, &captured.body)
		// Echo back a minimal system; the CLI only reads .data.Name/.data.ID.
		var echo chaosSystemResp
		if len(systems) > 0 {
			echo = systems[0]
		}
		resp := fakeAPIResp{Code: 200, Data: echo}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, captured
}

// TestSystemEnableSendsStatusOne pins the wire shape: PUT to /{id} with
// exactly {"status": 1}. If this ever changes, the backend contract pinned
// by UpdateChaosSystemReq.Status must be revisited in lockstep.
func TestSystemEnableSendsStatusOne(t *testing.T) {
	srv, captured := newFakeSystemServer(t, []chaosSystemResp{
		{ID: 42, Name: "ts"},
	})
	c := client.NewClient(srv.URL, "tok", 5*time.Second)

	if _, err := setSystemStatus(c, "ts", 1); err != nil {
		t.Fatalf("setSystemStatus(enable): %v", err)
	}
	if captured.path != "/api/v2/systems/42" {
		t.Errorf("PUT path = %q, want /api/v2/systems/42", captured.path)
	}
	got, ok := captured.body["status"]
	if !ok {
		t.Fatalf("PUT body missing status: %+v", captured.body)
	}
	// JSON decodes numbers as float64 by default.
	if n, _ := got.(float64); int(n) != 1 {
		t.Errorf("PUT body status = %v, want 1", got)
	}
	if len(captured.body) != 1 {
		t.Errorf("PUT body should only carry status; got %+v", captured.body)
	}
}

// TestSystemDisableSendsStatusZero mirrors the enable test for the disable
// wire shape ({"status": 0}).
func TestSystemDisableSendsStatusZero(t *testing.T) {
	srv, captured := newFakeSystemServer(t, []chaosSystemResp{
		{ID: 7, Name: "hr"},
	})
	c := client.NewClient(srv.URL, "tok", 5*time.Second)

	if _, err := setSystemStatus(c, "hr", 0); err != nil {
		t.Fatalf("setSystemStatus(disable): %v", err)
	}
	if captured.path != "/api/v2/systems/7" {
		t.Errorf("PUT path = %q, want /api/v2/systems/7", captured.path)
	}
	got, ok := captured.body["status"]
	if !ok {
		t.Fatalf("PUT body missing status: %+v", captured.body)
	}
	if n, _ := got.(float64); int(n) != 0 {
		t.Errorf("PUT body status = %v, want 0", got)
	}
}

// TestSystemEnableUnknownNameListsKnown pins that trying to enable an
// unregistered system surfaces the registered names in the error. Keeps the
// UX guard documented in the task brief.
func TestSystemEnableUnknownNameListsKnown(t *testing.T) {
	srv, _ := newFakeSystemServer(t, []chaosSystemResp{
		{ID: 1, Name: "ts"}, {ID: 2, Name: "hr"},
	})
	c := client.NewClient(srv.URL, "tok", 5*time.Second)

	_, err := setSystemStatus(c, "does-not-exist", 1)
	if err == nil {
		t.Fatal("setSystemStatus: expected error for unknown name, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "does-not-exist") {
		t.Errorf("error should mention the missing name; got %v", err)
	}
	if !strings.Contains(msg, "ts") || !strings.Contains(msg, "hr") {
		t.Errorf("error should list known names ts, hr; got %v", err)
	}
}
