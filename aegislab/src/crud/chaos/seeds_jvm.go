package chaos

import "time"

// SeedsJVM is the §11 step-2 Capability set for the JVM family.
// Schemas + observable_contract are lifted from
// `tools/capgen/output/capabilities.json`. `jvm_runtime_mutator` is a
// RuntimeMutatorChaos capability (OperationsPAI fork), rendered by
// renderer_runtimemutator — not a JVMChaos action — but it is seeded here
// so the catalog admits jvm_runtime_mutator points on import.
var SeedsJVM = []Capability{
	{
		Name:               "jvm_runtime_mutator",
		TargetSchema:       jvmRuntimeMutatorTargetSchema(),
		ParamSchema:        jvmRuntimeMutatorParamSchema(),
		ObservableContract: jvmRuntimeMutatorObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "jvm_cpu_stress",
		TargetSchema:       jvmMethodTargetSchema(),
		ParamSchema:        jvmCPUStressParamSchema(),
		ObservableContract: jvmCPUStressObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "jvm_gc",
		TargetSchema:       jvmAppOnlyTargetSchema(),
		ParamSchema:        jvmDurationOnlyParamSchema(),
		ObservableContract: jvmGCObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "jvm_memory_stress",
		TargetSchema:       jvmMethodTargetSchema(),
		ParamSchema:        jvmMemoryStressParamSchema(),
		ObservableContract: jvmMemoryStressObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "jvm_method_exception",
		TargetSchema:       jvmMethodTargetSchema(),
		ParamSchema:        jvmMethodExceptionParamSchema(),
		ObservableContract: jvmMethodExceptionObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "jvm_method_latency",
		TargetSchema:       jvmMethodTargetSchema(),
		ParamSchema:        jvmMethodLatencyParamSchema(),
		ObservableContract: jvmMethodLatencyObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "jvm_method_return",
		TargetSchema:       jvmMethodTargetSchema(),
		ParamSchema:        jvmMethodReturnParamSchema(),
		ObservableContract: jvmMethodReturnObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "jvm_mysql_exception",
		TargetSchema:       jvmMySQLTargetSchema(),
		ParamSchema:        jvmMySQLExceptionParamSchema(),
		ObservableContract: jvmMySQLExceptionObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "jvm_mysql_latency",
		TargetSchema:       jvmMySQLTargetSchema(),
		ParamSchema:        jvmMySQLLatencyParamSchema(),
		ObservableContract: jvmMySQLLatencyObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
}

func jvmAppOnlyTargetSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"namespace", "app"},
		"properties": map[string]any{
			"namespace": map[string]any{"type": "string", "minLength": 1},
			"app":       map[string]any{"type": "string", "minLength": 1},
		},
	}
}

func jvmMethodTargetSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"namespace", "app", "class", "method"},
		"properties": map[string]any{
			"namespace": map[string]any{"type": "string", "minLength": 1},
			"app":       map[string]any{"type": "string", "minLength": 1},
			"class":     map[string]any{"type": "string", "minLength": 1},
			"method":    map[string]any{"type": "string", "minLength": 1},
		},
	}
}

func jvmMySQLTargetSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"namespace", "app", "db_name", "table"},
		"properties": map[string]any{
			"namespace": map[string]any{"type": "string", "minLength": 1},
			"app":       map[string]any{"type": "string", "minLength": 1},
			"db_name":   map[string]any{"type": "string", "minLength": 1},
			"table":     map[string]any{"type": "string", "minLength": 1},
			"sql_type": map[string]any{
				"type":    "string",
				"enum":    []any{"all", "select", "insert", "update", "delete", "replace"},
				"default": "all",
			},
		},
	}
}

// jvmRuntimeMutatorTargetSchema mirrors tools/capgen jvmRuntimeMutatorTarget:
// the mutation identity (type_name + from/to/strategy) lives in the target
// so distinct mutations are distinct catalog points.
func jvmRuntimeMutatorTargetSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"namespace", "app", "class", "method", "mutation_type_name"},
		"properties": map[string]any{
			"namespace":          map[string]any{"type": "string", "minLength": 1},
			"app":                map[string]any{"type": "string", "minLength": 1},
			"class":              map[string]any{"type": "string", "minLength": 1},
			"method":             map[string]any{"type": "string", "minLength": 1},
			"mutation_type_name": map[string]any{"type": "string", "minLength": 1},
			"mutation_type":      map[string]any{"type": "integer"},
			"mutation_from":      map[string]any{"type": "string"},
			"mutation_to":        map[string]any{"type": "string"},
			"mutation_strategy":  map[string]any{"type": "string"},
			"description":        map[string]any{"type": "string"},
		},
	}
}

func jvmRuntimeMutatorParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{},
		"properties": map[string]any{
			"duration_s": durationParam(),
		},
	}
}

func jvmRuntimeMutatorObservableContract() JSONMap {
	return JSONMap{
		"name":     "jvm_runtime_mutator",
		"contract": "TODO: mutator effect is app-semantic (changed constant, flipped operator, swapped string); no generic trace assertion holds.",
	}
}

func jvmDurationOnlyParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{},
		"properties": map[string]any{
			"duration_s": durationParam(),
		},
	}
}

func jvmCPUStressParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"cpu_count"},
		"properties": map[string]any{
			"cpu_count":  map[string]any{"type": "integer", "minimum": 1, "maximum": 16, "default": 1},
			"duration_s": durationParam(),
		},
	}
}

func jvmMemoryStressParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{},
		"properties": map[string]any{
			"memory_type": map[string]any{
				"type":    "string",
				"enum":    []any{"heap", "stack"},
				"default": "heap",
			},
			"duration_s": durationParam(),
		},
	}
}

func jvmMethodLatencyParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"delay_ms"},
		"properties": map[string]any{
			"delay_ms":   map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
			"duration_s": durationParam(),
		},
	}
}

// exception_mode (default|random) from capgen is intentionally omitted:
// the renderer always emits jvmDefaultException(), so admitting the knob
// in the schema would silently fall back to default for "random".
func jvmMethodExceptionParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{},
		"properties": map[string]any{
			"duration_s": durationParam(),
		},
	}
}

// value_mode (default|random) from capgen is intentionally omitted: the
// renderer only branches on return_type, so "random" would silently
// no-op.
func jvmMethodReturnParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"return_type"},
		"properties": map[string]any{
			"return_type": map[string]any{
				"type": "string",
				"enum": []any{"string", "int"},
			},
			"duration_s": durationParam(),
		},
	}
}

func jvmMySQLLatencyParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"delay_ms"},
		"properties": map[string]any{
			"delay_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
			"mysql_connector": map[string]any{
				"type":    "string",
				"enum":    []any{"5", "8"},
				"default": "8",
			},
			"duration_s": durationParam(),
		},
	}
}

func jvmMySQLExceptionParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{},
		"properties": map[string]any{
			"mysql_connector": map[string]any{
				"type":    "string",
				"enum":    []any{"5", "8"},
				"default": "8",
			},
			"duration_s": durationParam(),
		},
	}
}

func jvmCPUStressObservableContract() JSONMap {
	return JSONMap{
		"name": "jvm_cpu_stress",
		"contract": map[string]any{
			"injection_window_s": 60,
			"metric_assertions": []any{
				map[string]any{"assertion": "container_cpu_usage_seconds_total rate increases by >= cpu_count * 0.5 cores"},
			},
		},
	}
}

func jvmGCObservableContract() JSONMap {
	return JSONMap{
		"name": "jvm_gc",
		"contract": map[string]any{
			"injection_window_s": 60,
			"metric_assertions": []any{
				map[string]any{"assertion": "jvm_gc_collection_seconds_count increases by >= 1 within injection_window_s"},
			},
		},
	}
}

func jvmMemoryStressObservableContract() JSONMap {
	return JSONMap{
		"name": "jvm_memory_stress",
		"contract": map[string]any{
			"injection_window_s": 60,
			"metric_assertions": []any{
				map[string]any{"assertion": "jvm_memory_used_bytes{area=heap|nonheap} increases substantially over baseline"},
			},
		},
	}
}

func jvmMethodExceptionObservableContract() JSONMap {
	return JSONMap{
		"name": "jvm_method_exception",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span(class.method) error_rate > baseline + 0.5 during injection_window_s"},
			},
		},
	}
}

func jvmMethodLatencyObservableContract() JSONMap {
	return JSONMap{
		"name": "jvm_method_latency",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span representing method invocation has duration >= delay_ms * 0.9"},
			},
		},
	}
}

func jvmMethodReturnObservableContract() JSONMap {
	// return-value override has no universal signature; downstream effect
	// depends on whether callers validate the value. Step 3 will gate on
	// system-specific functional checks.
	return JSONMap{
		"name": "jvm_method_return",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions":   []any{},
		},
	}
}

func jvmMySQLExceptionObservableContract() JSONMap {
	return JSONMap{
		"name": "jvm_mysql_exception",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "db span on (db_name, table, sql_type) error_rate > baseline + 0.8"},
			},
		},
	}
}

func jvmMySQLLatencyObservableContract() JSONMap {
	return JSONMap{
		"name": "jvm_mysql_latency",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "db span on (db_name, table, sql_type) duration >= delay_ms * 0.9"},
			},
		},
	}
}
