package chaos

import "time"

var SeedsDNS = []Capability{
	{
		Name:               "dns_error",
		TargetSchema:       dnsTargetSchema(),
		ParamSchema:        dnsParamSchema(),
		ObservableContract: dnsErrorObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "dns_random",
		TargetSchema:       dnsTargetSchema(),
		ParamSchema:        dnsParamSchema(),
		ObservableContract: dnsRandomObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
}

func dnsTargetSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"namespace", "app", "domain_patterns"},
		"properties": map[string]any{
			"namespace": map[string]any{"type": "string", "minLength": 1},
			"app":       map[string]any{"type": "string", "minLength": 1},
			"domain_patterns": map[string]any{
				"type":     "array",
				"minItems": 1,
				"items":    map[string]any{"type": "string", "minLength": 1},
			},
		},
	}
}

func dnsParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{},
		"properties": map[string]any{
			"duration_s": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600, "default": 60},
		},
	}
}

func dnsErrorObservableContract() JSONMap {
	// DNS resolver caches can mask the effect for >= TTL; gRPC-only
	// callers bypass the chaos-mesh DNS shim entirely.
	return JSONMap{
		"name": "dns_error",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.error_rate on egress to matched domain > baseline + 0.5 (DNS lookup failure)"},
			},
		},
	}
}

func dnsRandomObservableContract() JSONMap {
	// random-IP effect manifests as connection timeouts or wrong-host TLS
	// errors; the signature depends entirely on caller retry/timeout
	// config — no robust assertion until step 3 wires per-caller probes.
	return JSONMap{
		"name": "dns_random",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions":   []any{},
		},
	}
}
