package chaos

import "time"

// SeedsHTTP is the §11 step-2 Capability set for the HTTPChaos family.
// Schemas + observable_contract are lifted from
// `tools/capgen/output/capabilities.json`. All nine are `experimental`
// until conformance runs against a real cluster.
var SeedsHTTP = []Capability{
	{
		Name:               "http_request_abort",
		TargetSchema:       httpTargetSchema(),
		ParamSchema:        httpAbortParamSchema(),
		ObservableContract: httpAbortObservableContract("http_request_abort"),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "http_request_delay",
		TargetSchema:       httpTargetSchema(),
		ParamSchema:        httpDelayParamSchema(),
		ObservableContract: httpDelayObservableContract("http_request_delay"),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "http_request_replace_method",
		TargetSchema:       httpTargetSchema(),
		ParamSchema:        httpReplaceMethodParamSchema(),
		ObservableContract: httpReplaceMethodObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "http_request_replace_path",
		TargetSchema:       httpTargetSchema(),
		ParamSchema:        httpReplacePathParamSchema(),
		ObservableContract: httpReplacePathObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "http_response_abort",
		TargetSchema:       httpTargetSchema(),
		ParamSchema:        httpAbortParamSchema(),
		ObservableContract: httpAbortObservableContract("http_response_abort"),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "http_response_delay",
		TargetSchema:       httpTargetSchema(),
		ParamSchema:        httpDelayParamSchema(),
		ObservableContract: httpDelayObservableContract("http_response_delay"),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "http_response_patch_body",
		TargetSchema:       httpTargetSchema(),
		ParamSchema:        httpPatchBodyParamSchema(),
		ObservableContract: httpPatchBodyObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "http_response_replace_body",
		TargetSchema:       httpTargetSchema(),
		ParamSchema:        httpReplaceBodyParamSchema(),
		ObservableContract: httpReplaceBodyObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "http_response_replace_code",
		TargetSchema:       httpTargetSchema(),
		ParamSchema:        httpReplaceCodeParamSchema(),
		ObservableContract: httpReplaceCodeObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
}

// httpTargetSchema is shared by all 9 HTTP capabilities. method+path are
// required filters: chaos-mesh applies the action to every request
// matching the {port, method, path} tuple, so omitting them would
// silently broaden the blast radius beyond what callers intend.
func httpTargetSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"namespace", "app", "port", "method", "path"},
		"properties": map[string]any{
			"namespace": map[string]any{"type": "string", "minLength": 1},
			"app":       map[string]any{"type": "string", "minLength": 1},
			"port":      map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"method": map[string]any{
				"type": "string",
				"enum": []any{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE"},
			},
			"path": map[string]any{"type": "string", "minLength": 1},
		},
	}
}

func httpAbortParamSchema() JSONMap {
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

func httpDelayParamSchema() JSONMap {
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

func httpReplaceMethodParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"new_method"},
		"properties": map[string]any{
			"new_method": map[string]any{
				"type": "string",
				"enum": []any{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
			},
			"duration_s": durationParam(),
		},
	}
}

func httpReplacePathParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"new_path"},
		"properties": map[string]any{
			"new_path":   map[string]any{"type": "string", "minLength": 1},
			"duration_s": durationParam(),
		},
	}
}

func httpReplaceCodeParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"status_code"},
		"properties": map[string]any{
			"status_code": map[string]any{"type": "integer", "minimum": 100, "maximum": 599},
			"duration_s":  durationParam(),
		},
	}
}

func httpReplaceBodyParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{},
		"properties": map[string]any{
			"body_type": map[string]any{
				"type":    "string",
				"enum":    []any{"empty", "random"},
				"default": "empty",
			},
			"duration_s": durationParam(),
		},
	}
}

func httpPatchBodyParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{},
		"properties": map[string]any{
			"patch_json": map[string]any{
				"type":      "string",
				"minLength": 2,
				"default":   `{"foo":"bar"}`,
			},
			"duration_s": durationParam(),
		},
	}
}

func httpAbortObservableContract(name string) JSONMap {
	return JSONMap{
		"name": name,
		"contract": map[string]any{
			"baseline_window_s":  60,
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.error_rate on target endpoint > 0.9 during injection_window_s"},
			},
		},
	}
}

func httpDelayObservableContract(name string) JSONMap {
	return JSONMap{
		"name": name,
		"contract": map[string]any{
			"baseline_window_s":  60,
			"injection_window_s": 60,
			"tolerance":          map[string]any{"false_positive_rate": 0.05},
			"trace_assertions": []any{
				map[string]any{"assertion": "span.duration on target endpoint >= delay_ms * 0.9"},
				map[string]any{"assertion": "span.status_code unchanged from baseline distribution"},
			},
		},
	}
}

func httpReplaceMethodObservableContract() JSONMap {
	return JSONMap{
		"name": "http_request_replace_method",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.http.method on target endpoint == new_method OR error_rate > baseline + 0.5 (method not allowed)"},
			},
		},
	}
}

func httpReplacePathObservableContract() JSONMap {
	return JSONMap{
		"name": "http_request_replace_path",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.http.route on target endpoint changes to new_path OR error_rate > baseline + 0.5 (handler not found)"},
			},
		},
	}
}

func httpReplaceCodeObservableContract() JSONMap {
	return JSONMap{
		"name": "http_response_replace_code",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.http.status_code on target endpoint == status_code for >= 90% of spans during injection_window_s"},
			},
		},
	}
}

func httpReplaceBodyObservableContract() JSONMap {
	// Parse-error visibility depends on client instrumentation; some
	// clients accept empty body silently. Step 3 will gate the assertion
	// on observed client-side decode errors.
	return JSONMap{
		"name": "http_response_replace_body",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "client_side parse_error_rate on target endpoint > baseline + 0.5"},
			},
		},
	}
}

func httpPatchBodyObservableContract() JSONMap {
	// Body patches manifest as client-side schema errors, not directly
	// observable in server spans. Conformance probe is heuristic.
	return JSONMap{
		"name": "http_response_patch_body",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions":   []any{},
		},
	}
}
