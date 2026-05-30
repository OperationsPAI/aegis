package chaos

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	for _, cap := range jvmCapabilities {
		RegisterRenderer(jvmRenderer{capability: cap})
	}
}

// jvm_runtime_mutator is not a JVMChaos action (latency;return;exception;
// stress;gc;ruleData;mysql). In the OperationsPAI fork it ships as a separate
// RuntimeMutatorChaos CRD, rendered by runtimeMutatorRenderer
// (renderer_runtimemutator.go).
var jvmCapabilities = []string{
	"jvm_cpu_stress",
	"jvm_gc",
	"jvm_memory_stress",
	"jvm_method_exception",
	"jvm_method_latency",
	"jvm_method_return",
	"jvm_mysql_exception",
	"jvm_mysql_latency",
}

const (
	jvmChaosResource = "jvmchaos"
	// Default Byteman agent port; matches chaos-mesh JVMCommonSpec default.
	jvmAgentDefaultPort = int32(9277)
)

var jvmChaosGVR = schema.GroupVersionResource{
	Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: jvmChaosResource,
}

func ChaosMeshGroupVersionResourceForJVMChaos() schema.GroupVersionResource {
	return jvmChaosGVR
}

type jvmRenderer struct {
	capability string
}

func (r jvmRenderer) Capability() string { return r.capability }

func (jvmRenderer) Maturity() string { return CapExperimental }

func (r jvmRenderer) HandlePrefix() string {
	switch r.capability {
	case "jvm_cpu_stress":
		return "aegis-jvmcpu"
	case "jvm_gc":
		return "aegis-jvmgc"
	case "jvm_memory_stress":
		return "aegis-jvmmem"
	case "jvm_method_exception":
		return "aegis-jvmexc"
	case "jvm_method_latency":
		return "aegis-jvmlat"
	case "jvm_method_return":
		return "aegis-jvmret"
	case "jvm_mysql_exception":
		return "aegis-jvmmysqlexc"
	case "jvm_mysql_latency":
		return "aegis-jvmmysqllat"
	}
	return "aegis-jvm"
}

func (jvmRenderer) GVR() schema.GroupVersionResource { return jvmChaosGVR }

func (r jvmRenderer) ValidateForHandle(target map[string]any) error {
	if ns, _ := target["namespace"].(string); ns == "" {
		return fmt.Errorf("chaos-mesh %s: target.namespace is required", r.capability)
	}
	return nil
}

func (r jvmRenderer) ValidateTarget(target map[string]any) error {
	cap := r.capability
	for _, k := range []string{"namespace", "app"} {
		if v, _ := target[k].(string); v == "" {
			return fmt.Errorf("chaos-mesh %s: target.%s is required", cap, k)
		}
	}
	if r.needsClassMethod() {
		for _, k := range []string{"class", "method"} {
			if v, _ := target[k].(string); v == "" {
				return fmt.Errorf("chaos-mesh %s: target.%s is required", cap, k)
			}
		}
	}
	return nil
}

func (r jvmRenderer) needsClassMethod() bool {
	switch r.capability {
	case "jvm_gc", "jvm_mysql_latency", "jvm_mysql_exception":
		return false
	}
	return true
}

func (r jvmRenderer) ValidateParams(params map[string]any) error {
	cap := r.capability
	switch cap {
	case "jvm_cpu_stress":
		if _, err := getInt(params, "cpu_count"); err != nil {
			return fmt.Errorf("chaos-mesh %s: params.cpu_count is required: %w", cap, err)
		}
	case "jvm_method_latency", "jvm_mysql_latency":
		if _, err := getInt(params, "delay_ms"); err != nil {
			return fmt.Errorf("chaos-mesh %s: params.delay_ms is required: %w", cap, err)
		}
	case "jvm_method_return":
		rt, _ := params["return_type"].(string)
		if rt != "string" && rt != "int" {
			return fmt.Errorf("chaos-mesh %s: params.return_type must be \"string\" or \"int\"", cap)
		}
	}
	return nil
}

func (r jvmRenderer) RenderCR(sysCtx SystemContext, name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error) {
	app, _ := target["app"].(string)

	action, err := r.action()
	if err != nil {
		return nil, err
	}

	spec := map[string]any{
		"action": action,
		"mode":   "all",
		"selector": map[string]any{
			"namespaces": []any{namespace},
			"labelSelectors": map[string]any{
				sysCtx.LabelKey(): app,
			},
		},
		"port": int64(jvmAgentDefaultPort),
	}

	if d := durationFromParams(params); d != "" {
		spec["duration"] = d
	}

	if r.needsClassMethod() {
		class, _ := target["class"].(string)
		method, _ := target["method"].(string)
		spec["class"] = class
		spec["method"] = method
	}

	if err := r.attachActionParams(spec, target, params); err != nil {
		return nil, err
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "JVMChaos",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "aegis-chaos",
				"aegis-chaos/capability":       r.capability,
			},
		},
		"spec": spec,
	}}, nil
}

func (r jvmRenderer) action() (string, error) {
	switch r.capability {
	case "jvm_cpu_stress", "jvm_memory_stress":
		return "stress", nil
	case "jvm_gc":
		return "gc", nil
	case "jvm_method_latency":
		return "latency", nil
	case "jvm_method_exception":
		return "exception", nil
	case "jvm_method_return":
		return "return", nil
	case "jvm_mysql_latency", "jvm_mysql_exception":
		return "mysql", nil
	}
	return "", fmt.Errorf("chaos-mesh: no JVMChaos action for capability %q", r.capability)
}

func (r jvmRenderer) attachActionParams(spec map[string]any, target, params map[string]any) error {
	switch r.capability {
	case "jvm_cpu_stress":
		cpu, _ := getInt(params, "cpu_count")
		spec["cpuCount"] = int64(cpu)
	case "jvm_memory_stress":
		mt, _ := params["memory_type"].(string)
		if mt == "" {
			mt = "heap"
		}
		spec["memType"] = mt
	case "jvm_gc":
		// no action-specific fields
	case "jvm_method_latency":
		latency, _ := getInt(params, "delay_ms")
		spec["latency"] = int64(latency)
	case "jvm_method_exception":
		spec["exception"] = jvmDefaultException()
	case "jvm_method_return":
		rt, _ := params["return_type"].(string)
		spec["returnValue"] = jvmDefaultReturnValue(rt)
	case "jvm_mysql_latency":
		r.attachMySQLCommon(spec, target, params)
		latency, _ := getInt(params, "delay_ms")
		spec["latency"] = int64(latency)
	case "jvm_mysql_exception":
		r.attachMySQLCommon(spec, target, params)
		spec["exception"] = jvmDefaultMySQLException()
	}
	return nil
}

func (jvmRenderer) attachMySQLCommon(spec map[string]any, target, params map[string]any) {
	if v, _ := target["db_name"].(string); v != "" {
		spec["database"] = v
	}
	if v, _ := target["table"].(string); v != "" {
		spec["table"] = v
	}
	if v, _ := target["sql_type"].(string); v != "" {
		spec["sqlType"] = v
	}
	connector, _ := params["mysql_connector"].(string)
	if connector == "" {
		connector = "8"
	}
	spec["mysqlConnectorVersion"] = connector
}

// jvmDefaultException is the value chaos-mesh treats as a Java throw expression.
func jvmDefaultException() string {
	return `java.io.IOException("BOOM")`
}

// jvmDefaultMySQLException returns the message string chaos-mesh wraps
// in a SQLException for action=mysql. Unlike `exception`, the mysql path
// takes a plain message, not a constructor expression.
func jvmDefaultMySQLException() string {
	return "aegis-injected MySQL fault"
}

func jvmDefaultReturnValue(returnType string) string {
	switch returnType {
	case "int":
		return "42"
	case "string":
		return `"chaos"`
	}
	return `"chaos"`
}
