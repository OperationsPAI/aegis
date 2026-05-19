package chaos

import (
	"testing"
)

// TestHTTPRendererRegistry asserts that the 9 HTTP renderers register
// under their `http_*` capability names and that the registry routes
// lookups correctly. Regression here would silently drop a capability
// from the executor's SupportedCapabilities() surface.
func TestHTTPRendererRegistry(t *testing.T) {
	want := []string{
		"http_request_abort",
		"http_request_delay",
		"http_request_replace_method",
		"http_request_replace_path",
		"http_response_abort",
		"http_response_delay",
		"http_response_patch_body",
		"http_response_replace_body",
		"http_response_replace_code",
	}
	got := map[string]string{}
	for _, c := range registeredCapabilities() {
		got[c.Capability] = c.Maturity
	}
	for _, w := range want {
		if got[w] != CapExperimental {
			t.Errorf("renderer registry missing %q or wrong maturity (have %q)", w, got[w])
		}
		if _, err := lookupRenderer(w); err != nil {
			t.Errorf("lookup %s: %v", w, err)
		}
	}
}

// TestHTTPRequestDelayRender asserts the full CR shape for
// http_request_delay: spec.target="Request", filter fields populated,
// PodHttpChaosActions.Delay inlined at spec top level.
func TestHTTPRequestDelayRender(t *testing.T) {
	r, err := lookupRenderer("http_request_delay")
	if err != nil {
		t.Fatalf("lookupRenderer: %v", err)
	}
	target := map[string]any{
		"namespace": "ts",
		"app":       "ts-order-service",
		"port":      8080,
		"method":    "GET",
		"path":      "/api/v1/orders",
	}
	params := map[string]any{
		"delay_ms":   250,
		"duration_s": 30,
	}
	if err := r.ValidateTarget(target); err != nil {
		t.Fatalf("ValidateTarget: %v", err)
	}
	if err := r.ValidateParams(params); err != nil {
		t.Fatalf("ValidateParams: %v", err)
	}

	cr, err := r.RenderCR(SystemContext{}, "aegis-httpreqdelay-abc", "ts", target, params)
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	obj := cr.Object
	if obj["kind"] != "HTTPChaos" {
		t.Errorf("kind = %v, want HTTPChaos", obj["kind"])
	}
	spec := obj["spec"].(map[string]any)
	if spec["target"] != "Request" {
		t.Errorf("spec.target = %v, want Request", spec["target"])
	}
	if spec["method"] != "GET" {
		t.Errorf("spec.method = %v", spec["method"])
	}
	if spec["path"] != "/api/v1/orders" {
		t.Errorf("spec.path = %v", spec["path"])
	}
	if spec["port"].(int64) != 8080 {
		t.Errorf("spec.port = %v", spec["port"])
	}
	if spec["delay"] != "250ms" {
		t.Errorf("spec.delay = %v, want 250ms", spec["delay"])
	}
	if spec["duration"] != "30s" {
		t.Errorf("spec.duration = %v", spec["duration"])
	}
	sel := spec["selector"].(map[string]any)
	labels := sel["labelSelectors"].(map[string]any)
	if labels["app"] != "ts-order-service" {
		t.Errorf("selector.app = %v", labels["app"])
	}
}

// TestHTTPResponseReplaceCodeRender asserts the response-side replace
// path: spec.target="Response", spec.replace.code set, no other replace
// fields populated.
func TestHTTPResponseReplaceCodeRender(t *testing.T) {
	r, _ := lookupRenderer("http_response_replace_code")
	target := map[string]any{
		"namespace": "ts",
		"app":       "ts-user",
		"port":      8080,
		"method":    "POST",
		"path":      "/login",
	}
	params := map[string]any{"status_code": 503, "duration_s": 60}
	if err := r.ValidateTarget(target); err != nil {
		t.Fatalf("ValidateTarget: %v", err)
	}
	if err := r.ValidateParams(params); err != nil {
		t.Fatalf("ValidateParams: %v", err)
	}
	cr, err := r.RenderCR(SystemContext{}, "aegis-x", "ts", target, params)
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	spec := cr.Object["spec"].(map[string]any)
	if spec["target"] != "Response" {
		t.Errorf("spec.target = %v, want Response", spec["target"])
	}
	replace, ok := spec["replace"].(map[string]any)
	if !ok {
		t.Fatalf("spec.replace missing")
	}
	if replace["code"].(int64) != 503 {
		t.Errorf("spec.replace.code = %v, want 503", replace["code"])
	}
	if _, present := replace["body"]; present {
		t.Errorf("spec.replace.body must not be set for replace_code")
	}

	// Out-of-range status codes rejected.
	if err := r.ValidateParams(map[string]any{"status_code": 99}); err == nil {
		t.Error("ValidateParams should reject status_code < 100")
	}
	if err := r.ValidateParams(map[string]any{"status_code": 600}); err == nil {
		t.Error("ValidateParams should reject status_code > 599")
	}
}

// TestHTTPRenderersTargetMapping table-drives all 9 capabilities and
// asserts:
//  1. spec.target is "Request" vs "Response" per the action prefix.
//  2. the correct action sub-block (abort/delay/replace/patch) appears.
//  3. ValidateParams enforces the documented required field.
func TestHTTPRenderersTargetMapping(t *testing.T) {
	target := map[string]any{
		"namespace": "ts",
		"app":       "svc",
		"port":      8080,
		"method":    "GET",
		"path":      "/x",
	}
	cases := []struct {
		capability   string
		wantTarget   string
		wantSpecKey  string // top-level spec field set by attachAction
		validParams  map[string]any
		missingField string
	}{
		{"http_request_abort", "Request", "abort", map[string]any{}, ""},
		{"http_request_delay", "Request", "delay", map[string]any{"delay_ms": 100}, "delay_ms"},
		{"http_request_replace_method", "Request", "replace", map[string]any{"new_method": "POST"}, "new_method"},
		{"http_request_replace_path", "Request", "replace", map[string]any{"new_path": "/y"}, "new_path"},
		{"http_response_abort", "Response", "abort", map[string]any{}, ""},
		{"http_response_delay", "Response", "delay", map[string]any{"delay_ms": 100}, "delay_ms"},
		{"http_response_patch_body", "Response", "patch", map[string]any{}, ""},
		{"http_response_replace_body", "Response", "replace", map[string]any{}, ""},
		{"http_response_replace_code", "Response", "replace", map[string]any{"status_code": 500}, "status_code"},
	}
	for _, tc := range cases {
		t.Run(tc.capability, func(t *testing.T) {
			r, err := lookupRenderer(tc.capability)
			if err != nil {
				t.Fatalf("lookupRenderer: %v", err)
			}
			if err := r.ValidateTarget(target); err != nil {
				t.Fatalf("ValidateTarget: %v", err)
			}
			if err := r.ValidateParams(tc.validParams); err != nil {
				t.Fatalf("ValidateParams(valid): %v", err)
			}
			cr, err := r.RenderCR(SystemContext{}, "aegis-x", "ts", target, tc.validParams)
			if err != nil {
				t.Fatalf("RenderCR: %v", err)
			}
			spec := cr.Object["spec"].(map[string]any)
			if spec["target"] != tc.wantTarget {
				t.Errorf("spec.target = %v, want %v", spec["target"], tc.wantTarget)
			}
			if _, ok := spec[tc.wantSpecKey]; !ok {
				t.Errorf("spec.%s sub-block missing", tc.wantSpecKey)
			}

			if tc.missingField != "" {
				bad := map[string]any{}
				for k, v := range tc.validParams {
					if k != tc.missingField {
						bad[k] = v
					}
				}
				if err := r.ValidateParams(bad); err == nil {
					t.Errorf("ValidateParams should reject missing %s", tc.missingField)
				}
			}
		})
	}
}

// TestHTTPTargetValidation locks the boundary checks on target shape:
// namespace/app/port/method/path are all required, port must be in
// 1..65535.
func TestHTTPTargetValidation(t *testing.T) {
	r, _ := lookupRenderer("http_request_abort")
	bad := []map[string]any{
		{"app": "a", "port": 8080, "method": "GET", "path": "/"},                                       // no namespace
		{"namespace": "ns", "port": 8080, "method": "GET", "path": "/"},                                // no app
		{"namespace": "ns", "app": "a", "method": "GET", "path": "/"},                                  // no port
		{"namespace": "ns", "app": "a", "port": 8080, "path": "/"},                                     // no method
		{"namespace": "ns", "app": "a", "port": 8080, "method": "GET"},                                 // no path
		{"namespace": "ns", "app": "a", "port": 0, "method": "GET", "path": "/"},                       // port=0
		{"namespace": "ns", "app": "a", "port": 70000, "method": "GET", "path": "/"},                   // port too high
	}
	for i, tgt := range bad {
		if err := r.ValidateTarget(tgt); err == nil {
			t.Errorf("case %d should error: %v", i, tgt)
		}
	}
}

// TestHTTPPatchBodyJSONValidation locks the JSON-validity gate for
// patch_json — chaos-mesh's PodHttpChaosPatchBodyAction expects
// type=JSON,value=<json literal>; invalid JSON would silently no-op.
func TestHTTPPatchBodyJSONValidation(t *testing.T) {
	r, _ := lookupRenderer("http_response_patch_body")
	if err := r.ValidateParams(map[string]any{"patch_json": "{not json"}); err == nil {
		t.Error("ValidateParams should reject malformed JSON")
	}
	if err := r.ValidateParams(map[string]any{"patch_json": `{"foo":1}`}); err != nil {
		t.Errorf("ValidateParams should accept valid JSON: %v", err)
	}
	if err := r.ValidateParams(map[string]any{}); err != nil {
		t.Errorf("ValidateParams should accept empty params (default applies): %v", err)
	}
}

// TestHTTPDeriveHandleNamespaceOnly extends D1's contract that
// DeriveHandle requires only the fields the CR name depends on
// (namespace). Full target shape is enforced at Apply.
func TestHTTPDeriveHandleNamespaceOnly(t *testing.T) {
	e := &ChaosMeshExecutor{}
	target := map[string]any{"namespace": "ts"} // no app/port/method/path
	for _, capability := range []string{
		"http_request_abort", "http_request_delay",
		"http_request_replace_method", "http_request_replace_path",
		"http_response_abort", "http_response_delay",
		"http_response_patch_body", "http_response_replace_body",
		"http_response_replace_code",
	} {
		if _, err := e.DeriveHandle(capability, "key-"+capability, target); err != nil {
			t.Errorf("%s DeriveHandle with namespace-only target: %v", capability, err)
		}
		if _, err := e.DeriveHandle(capability, "key", map[string]any{}); err == nil {
			t.Errorf("%s DeriveHandle should reject empty target", capability)
		}
	}
}
