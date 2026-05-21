package chaos

import (
	"testing"

	guidedcli "aegis/platform/chaos"
)

// TestGuidedToChaosTarget_JVMMysql pins the contract that guided submit
// for jvm_mysql_* emits a target shape that passes the renderer's
// ValidateTarget — the same gate the production POST path runs. Before
// the schema fix the case branch shared with jvm_method_* produced
// {namespace, app, class, method}, which the new jvmMySQLTargetSchema
// rejects (additionalProperties:false + db_name/table required).
func TestGuidedToChaosTarget_JVMMysql(t *testing.T) {
	cfg := guidedcli.GuidedConfig{
		System:    "ts",
		App:       "ts-order-service",
		Database:  "ts",
		Table:     "orders",
		Operation: "SELECT",
	}
	for _, cap := range []string{"jvm_mysql_latency", "jvm_mysql_exception"} {
		target, err := GuidedToChaosTarget(cap, cfg)
		if err != nil {
			t.Fatalf("%s: GuidedToChaosTarget: %v", cap, err)
		}
		if _, hasClass := target["class"]; hasClass {
			t.Errorf("%s: target must not carry class (schema rejects)", cap)
		}
		if target["db_name"] != "ts" || target["table"] != "orders" || target["sql_type"] != "select" {
			t.Errorf("%s: target = %v", cap, target)
		}
		r, err := lookupRenderer(cap)
		if err != nil {
			t.Fatalf("%s: lookupRenderer: %v", cap, err)
		}
		if err := r.ValidateTarget(target); err != nil {
			t.Errorf("%s: ValidateTarget rejected guided-emitted target: %v", cap, err)
		}
	}

	missing := guidedcli.GuidedConfig{System: "ts", App: "ts-order-service"}
	if _, err := GuidedToChaosTarget("jvm_mysql_latency", missing); err == nil {
		t.Error("expected error when database/table missing")
	}
}
