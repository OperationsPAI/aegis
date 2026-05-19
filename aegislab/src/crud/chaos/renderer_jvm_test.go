package chaos

import (
	"testing"
)

// TestJVMRendererRegistry locks in that the 8 JVM renderers register
// under their `jvm_*` capability names. jvm_runtime_mutator is
// deliberately absent — no chaos-mesh JVMChaos action maps to it.
func TestJVMRendererRegistry(t *testing.T) {
	want := []string{
		"jvm_cpu_stress",
		"jvm_gc",
		"jvm_memory_stress",
		"jvm_method_exception",
		"jvm_method_latency",
		"jvm_method_return",
		"jvm_mysql_exception",
		"jvm_mysql_latency",
	}
	got := map[string]string{}
	for _, c := range registeredCapabilities() {
		got[c.Capability] = c.Maturity
	}
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Errorf("renderer registry missing capability %q", w)
		}
		if got[w] != CapExperimental {
			t.Errorf("%s must be %q, got %q", w, CapExperimental, got[w])
		}
	}
	if _, ok := got["jvm_runtime_mutator"]; ok {
		t.Error("jvm_runtime_mutator must not be registered (no chaos-mesh JVMChaos action)")
	}
	if _, err := lookupRenderer("jvm_method_latency"); err != nil {
		t.Errorf("lookup jvm_method_latency: %v", err)
	}
}

// TestJVMMethodLatencyRender exercises the latency action end-to-end —
// it carries the largest set of method-targeted fields (class, method,
// latency, port, duration) and is the most likely to break on a
// JVMChaosSpec shape regression.
func TestJVMMethodLatencyRender(t *testing.T) {
	r, err := lookupRenderer("jvm_method_latency")
	if err != nil {
		t.Fatalf("lookupRenderer: %v", err)
	}
	target := map[string]any{
		"namespace": "ts",
		"app":       "ts-order-service",
		"class":     "com.example.OrderService",
		"method":    "createOrder",
	}
	params := map[string]any{
		"delay_ms":   1500,
		"duration_s": 90,
	}
	if err := r.ValidateTarget(target); err != nil {
		t.Fatalf("ValidateTarget: %v", err)
	}
	if err := r.ValidateParams(params); err != nil {
		t.Fatalf("ValidateParams: %v", err)
	}
	cr, err := r.RenderCR("aegis-jvmlat-abc", "ts", target, params)
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	obj := cr.Object
	if obj["kind"] != "JVMChaos" {
		t.Errorf("kind = %v, want JVMChaos", obj["kind"])
	}
	spec := obj["spec"].(map[string]any)
	if spec["action"] != "latency" {
		t.Errorf("action = %v, want latency", spec["action"])
	}
	if spec["class"] != "com.example.OrderService" {
		t.Errorf("class = %v", spec["class"])
	}
	if spec["method"] != "createOrder" {
		t.Errorf("method = %v", spec["method"])
	}
	if spec["latency"] != int64(1500) {
		t.Errorf("latency = %v (%T), want 1500", spec["latency"], spec["latency"])
	}
	if spec["duration"] != "90s" {
		t.Errorf("duration = %v, want 90s", spec["duration"])
	}
	if spec["port"] != int64(9277) {
		t.Errorf("port = %v, want 9277", spec["port"])
	}
	sel := spec["selector"].(map[string]any)
	labels := sel["labelSelectors"].(map[string]any)
	if labels["app"] != "ts-order-service" {
		t.Errorf("selector.app = %v", labels["app"])
	}
}

// TestJVMMySQLExceptionRender exercises the mysql action — the only
// action that pulls fields from BOTH target (db_name/table/sql_type) and
// params (mysql_connector). A regression in attachMySQLCommon would
// silently strip the database filter and inject on every query.
func TestJVMMySQLExceptionRender(t *testing.T) {
	r, err := lookupRenderer("jvm_mysql_exception")
	if err != nil {
		t.Fatalf("lookupRenderer: %v", err)
	}
	target := map[string]any{
		"namespace": "ts",
		"app":       "ts-order-service",
		"class":     "com.example.OrderRepository",
		"method":    "findByUser",
		"db_name":   "ts",
		"table":     "orders",
		"sql_type":  "select",
	}
	params := map[string]any{
		"mysql_connector": "8",
		"duration_s":      60,
	}
	if err := r.ValidateTarget(target); err != nil {
		t.Fatalf("ValidateTarget: %v", err)
	}
	if err := r.ValidateParams(params); err != nil {
		t.Fatalf("ValidateParams: %v", err)
	}
	cr, err := r.RenderCR("aegis-jvmmysqlexc-abc", "ts", target, params)
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	spec := cr.Object["spec"].(map[string]any)
	if spec["action"] != "mysql" {
		t.Errorf("action = %v, want mysql", spec["action"])
	}
	if spec["database"] != "ts" {
		t.Errorf("database = %v", spec["database"])
	}
	if spec["table"] != "orders" {
		t.Errorf("table = %v", spec["table"])
	}
	if spec["sqlType"] != "select" {
		t.Errorf("sqlType = %v", spec["sqlType"])
	}
	if spec["mysqlConnectorVersion"] != "8" {
		t.Errorf("mysqlConnectorVersion = %v", spec["mysqlConnectorVersion"])
	}
	if spec["exception"] == "" || spec["exception"] == nil {
		t.Error("exception must be non-empty for action=mysql exception")
	}
}

// TestJVMRenderersActionMapping is a table-driven sweep over all 8 JVM
// capabilities asserting (1) spec.action matches the chaos-mesh
// JVMChaosAction enum and (2) the action-specific sub-field is present.
func TestJVMRenderersActionMapping(t *testing.T) {
	methodTarget := map[string]any{
		"namespace": "ts", "app": "ts-order", "class": "C", "method": "m",
	}
	mysqlTarget := map[string]any{
		"namespace": "ts", "app": "ts-order", "class": "C", "method": "m",
		"db_name": "ts", "table": "orders", "sql_type": "select",
	}
	appOnly := map[string]any{"namespace": "ts", "app": "ts-order"}

	cases := []struct {
		capability  string
		target      map[string]any
		params      map[string]any
		wantAction  string
		wantSubKey  string
		wantSubVal  any
	}{
		{"jvm_cpu_stress", methodTarget, map[string]any{"cpu_count": 2}, "stress", "cpuCount", int64(2)},
		{"jvm_memory_stress", methodTarget, map[string]any{"memory_type": "heap"}, "stress", "memType", "heap"},
		{"jvm_gc", appOnly, map[string]any{}, "gc", "", nil},
		{"jvm_method_latency", methodTarget, map[string]any{"delay_ms": 200}, "latency", "latency", int64(200)},
		{"jvm_method_exception", methodTarget, map[string]any{}, "exception", "exception", nil},
		{"jvm_method_return", methodTarget, map[string]any{"return_type": "int"}, "return", "returnValue", "42"},
		{"jvm_mysql_latency", mysqlTarget, map[string]any{"delay_ms": 200}, "mysql", "latency", int64(200)},
		{"jvm_mysql_exception", mysqlTarget, map[string]any{}, "mysql", "exception", nil},
	}
	for _, tc := range cases {
		t.Run(tc.capability, func(t *testing.T) {
			r, err := lookupRenderer(tc.capability)
			if err != nil {
				t.Fatalf("lookupRenderer: %v", err)
			}
			if err := r.ValidateTarget(tc.target); err != nil {
				t.Fatalf("ValidateTarget: %v", err)
			}
			if err := r.ValidateParams(tc.params); err != nil {
				t.Fatalf("ValidateParams: %v", err)
			}
			cr, err := r.RenderCR("aegis-x", "ts", tc.target, tc.params)
			if err != nil {
				t.Fatalf("RenderCR: %v", err)
			}
			spec := cr.Object["spec"].(map[string]any)
			if spec["action"] != tc.wantAction {
				t.Errorf("action = %v, want %v", spec["action"], tc.wantAction)
			}
			if tc.wantSubKey != "" {
				v, ok := spec[tc.wantSubKey]
				if !ok {
					t.Errorf("missing spec.%s", tc.wantSubKey)
				} else if tc.wantSubVal != nil && v != tc.wantSubVal {
					t.Errorf("spec.%s = %v (%T), want %v (%T)", tc.wantSubKey, v, v, tc.wantSubVal, tc.wantSubVal)
				}
			}
		})
	}
}

// TestJVMTargetValidation covers boundary checks: namespace/app required
// for all, class/method required for every cap EXCEPT jvm_gc.
func TestJVMTargetValidation(t *testing.T) {
	cases := []struct {
		capability string
		target     map[string]any
		wantErr    bool
	}{
		{"jvm_gc", map[string]any{"namespace": "ts", "app": "a"}, false},
		{"jvm_gc", map[string]any{"app": "a"}, true},
		{"jvm_gc", map[string]any{"namespace": "ts"}, true},
		{"jvm_method_latency", map[string]any{"namespace": "ts", "app": "a", "class": "C", "method": "m"}, false},
		{"jvm_method_latency", map[string]any{"namespace": "ts", "app": "a"}, true},
		{"jvm_method_latency", map[string]any{"namespace": "ts", "app": "a", "class": "C"}, true},
		{"jvm_mysql_latency", map[string]any{"namespace": "ts", "app": "a", "class": "C", "method": "m"}, false},
	}
	for _, tc := range cases {
		r, err := lookupRenderer(tc.capability)
		if err != nil {
			t.Fatalf("lookupRenderer %s: %v", tc.capability, err)
		}
		err = r.ValidateTarget(tc.target)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s ValidateTarget(%v) err=%v, wantErr=%v", tc.capability, tc.target, err, tc.wantErr)
		}
	}
}

// TestJVMParamValidation covers the per-action required param: cpu_count
// for cpu_stress, delay_ms for *_latency, return_type for method_return.
func TestJVMParamValidation(t *testing.T) {
	cases := []struct {
		capability string
		params     map[string]any
		wantErr    bool
	}{
		{"jvm_cpu_stress", map[string]any{"cpu_count": 1}, false},
		{"jvm_cpu_stress", map[string]any{}, true},
		{"jvm_method_latency", map[string]any{"delay_ms": 100}, false},
		{"jvm_method_latency", map[string]any{}, true},
		{"jvm_mysql_latency", map[string]any{"delay_ms": 100}, false},
		{"jvm_mysql_latency", map[string]any{}, true},
		{"jvm_method_return", map[string]any{"return_type": "string"}, false},
		{"jvm_method_return", map[string]any{"return_type": "bool"}, true},
		{"jvm_method_return", map[string]any{}, true},
		{"jvm_gc", map[string]any{}, false},
		{"jvm_memory_stress", map[string]any{}, false},
		{"jvm_method_exception", map[string]any{}, false},
		{"jvm_mysql_exception", map[string]any{}, false},
	}
	for _, tc := range cases {
		r, _ := lookupRenderer(tc.capability)
		err := r.ValidateParams(tc.params)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s ValidateParams(%v) err=%v, wantErr=%v", tc.capability, tc.params, err, tc.wantErr)
		}
	}
}
