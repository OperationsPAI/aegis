package chaos

import (
	"testing"
)

// TestJVMRendererRegistry locks in that the 8 JVM renderers register under
// their `jvm_*` capability names. jvm_runtime_mutator also registers, but via
// runtimeMutatorRenderer (RuntimeMutatorChaos), not as a JVMChaos action — see
// TestRuntimeMutatorRender.
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
	for _, cap := range jvmCapabilities {
		if cap == "jvm_runtime_mutator" {
			t.Error("jvm_runtime_mutator must not be in jvmCapabilities (RuntimeMutatorChaos, not JVMChaos)")
		}
	}
	if _, err := lookupRenderer("jvm_method_latency"); err != nil {
		t.Errorf("lookup jvm_method_latency: %v", err)
	}
}

// TestRuntimeMutatorRender verifies jvm_runtime_mutator renders a
// RuntimeMutatorChaos CR with the action and mutation fields mapped 1:1 from
// the target. constant carries from/to; operator/string carry strategy.
func TestRuntimeMutatorRender(t *testing.T) {
	r, err := lookupRenderer("jvm_runtime_mutator")
	if err != nil {
		t.Fatalf("lookupRenderer: %v", err)
	}
	if r.GVR().Resource != "runtimemutatorchaos" {
		t.Errorf("GVR.Resource = %q, want runtimemutatorchaos", r.GVR().Resource)
	}

	constTarget := map[string]any{
		"namespace": "ts", "app": "ts-order-service",
		"class": "order.OrderController", "method": "create",
		"mutation_type_name": "constant", "mutation_type": 0,
		"mutation_from": `"/create"`, "mutation_to": `"mutated_/create"`,
	}
	if err := r.ValidateTarget(constTarget); err != nil {
		t.Fatalf("ValidateTarget(constant): %v", err)
	}
	cr, err := r.RenderCR(SystemContext{}, "aegis-jvmmut-abc", "ts", constTarget, map[string]any{"duration_s": 60})
	if err != nil {
		t.Fatalf("RenderCR(constant): %v", err)
	}
	if cr.Object["kind"] != "RuntimeMutatorChaos" {
		t.Errorf("kind = %v, want RuntimeMutatorChaos", cr.Object["kind"])
	}
	spec := cr.Object["spec"].(map[string]any)
	if spec["action"] != "constant" {
		t.Errorf("action = %v, want constant", spec["action"])
	}
	if spec["class"] != "order.OrderController" || spec["method"] != "create" {
		t.Errorf("class/method = %v/%v", spec["class"], spec["method"])
	}
	if spec["from"] != `"/create"` || spec["to"] != `"mutated_/create"` {
		t.Errorf("from/to = %v/%v", spec["from"], spec["to"])
	}
	if _, ok := spec["strategy"]; ok {
		t.Error("constant action must not carry strategy")
	}
	if spec["duration"] != "60s" {
		t.Errorf("duration = %v, want 60s", spec["duration"])
	}
	sel := spec["selector"].(map[string]any)["labelSelectors"].(map[string]any)
	if sel["app"] != "ts-order-service" {
		t.Errorf("selector.app = %v", sel["app"])
	}

	strTarget := map[string]any{
		"namespace": "ts", "app": "ts-order-service",
		"class": "order.OrderController", "method": "create",
		"mutation_type_name": "string", "mutation_type": 2,
		"mutation_strategy": "reverse",
	}
	cr2, err := r.RenderCR(SystemContext{}, "aegis-jvmmut-def", "ts", strTarget, nil)
	if err != nil {
		t.Fatalf("RenderCR(string): %v", err)
	}
	spec2 := cr2.Object["spec"].(map[string]any)
	if spec2["action"] != "string" {
		t.Errorf("action = %v, want string", spec2["action"])
	}
	if spec2["strategy"] != "reverse" {
		t.Errorf("strategy = %v, want reverse", spec2["strategy"])
	}
	if _, ok := spec2["from"]; ok {
		t.Error("string action must not carry from")
	}
}

// TestRuntimeMutatorValidateTarget locks ValidateTarget to the fork's
// RuntimeMutatorChaos admission webhook (constant needs from+to & rejects
// strategy; operator/string need strategy & reject from/to). Without this, a
// malformed import point passes server validation but is rejected opaquely at
// cluster apply.
func TestRuntimeMutatorValidateTarget(t *testing.T) {
	r, err := lookupRenderer("jvm_runtime_mutator")
	if err != nil {
		t.Fatalf("lookupRenderer: %v", err)
	}
	base := func(extra map[string]any) map[string]any {
		t := map[string]any{"namespace": "ts", "app": "a", "class": "C", "method": "m"}
		for k, v := range extra {
			t[k] = v
		}
		return t
	}
	cases := []struct {
		name    string
		target  map[string]any
		wantErr bool
	}{
		{"constant ok", base(map[string]any{"mutation_type_name": "constant", "mutation_from": `"x"`, "mutation_to": `"y"`}), false},
		{"constant missing to", base(map[string]any{"mutation_type_name": "constant", "mutation_from": `"x"`}), true},
		{"constant with strategy", base(map[string]any{"mutation_type_name": "constant", "mutation_from": `"x"`, "mutation_to": `"y"`, "mutation_strategy": "empty"}), true},
		{"string ok", base(map[string]any{"mutation_type_name": "string", "mutation_strategy": "reverse"}), false},
		{"string missing strategy", base(map[string]any{"mutation_type_name": "string"}), true},
		{"string with from", base(map[string]any{"mutation_type_name": "string", "mutation_strategy": "reverse", "mutation_from": `"x"`}), true},
		{"operator ok", base(map[string]any{"mutation_type_name": "operator", "mutation_strategy": "add_to_sub"}), false},
		{"unknown action", base(map[string]any{"mutation_type_name": "bogus"}), true},
	}
	for _, tc := range cases {
		err := r.ValidateTarget(tc.target)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
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
	cr, err := r.RenderCR(SystemContext{}, "aegis-jvmlat-abc", "ts", target, params)
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
	cr, err := r.RenderCR(SystemContext{}, "aegis-jvmmysqlexc-abc", "ts", target, params)
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
		"namespace": "ts", "app": "ts-order",
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
			cr, err := r.RenderCR(SystemContext{}, "aegis-x", "ts", tc.target, tc.params)
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

// TestDeriveHandleNamespaceOnly_JVM mirrors the §8 contract from
// renderer_network_test.go for the JVM family: DeriveHandle must accept
// namespace-only target (full target shape is enforced at Apply). Even
// method-targeted caps that require class+method at Apply must NOT
// require them for handle derivation — otherwise a row that failed mid-
// Apply has no handle and is unrecoverable (ADR-0004).
func TestDeriveHandleNamespaceOnly_JVM(t *testing.T) {
	e := &ChaosMeshExecutor{}
	target := map[string]any{"namespace": "ts"}
	for _, cap := range jvmCapabilities {
		if _, err := e.DeriveHandle(cap, "key-"+cap, "ns0", target); err != nil {
			t.Errorf("%s DeriveHandle with namespace-only target: %v", cap, err)
		}
		if _, err := e.DeriveHandle(cap, "key", "ns0", map[string]any{}); err == nil {
			t.Errorf("%s DeriveHandle should reject empty target", cap)
		}
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
		{"jvm_mysql_latency", map[string]any{"namespace": "ts", "app": "a", "db_name": "ts", "table": "orders"}, false},
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
