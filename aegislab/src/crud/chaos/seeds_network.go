package chaos

import "time"

// SeedsNetwork is the §11 step-2 Capability set for the Network family.
// Schemas + observable_contract are lifted verbatim from
// `tools/capgen/output/capabilities.json` (capgen is the source of truth
// for Capability shape). All six are `experimental` until conformance
// runs against a real cluster.
var SeedsNetwork = []Capability{
	{
		Name:               "network_delay",
		TargetSchema:       networkTargetSchema(),
		ParamSchema:        networkDelayParamSchema(),
		ObservableContract: networkDelayObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "network_loss",
		TargetSchema:       networkTargetSchema(),
		ParamSchema:        networkLossParamSchema(),
		ObservableContract: networkLossObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "network_duplicate",
		TargetSchema:       networkTargetSchema(),
		ParamSchema:        networkDuplicateParamSchema(),
		ObservableContract: networkDuplicateObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "network_corrupt",
		TargetSchema:       networkTargetSchema(),
		ParamSchema:        networkCorruptParamSchema(),
		ObservableContract: networkCorruptObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "network_bandwidth",
		TargetSchema:       networkTargetSchema(),
		ParamSchema:        networkBandwidthParamSchema(),
		ObservableContract: networkBandwidthObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "network_partition",
		TargetSchema:       networkTargetSchema(),
		ParamSchema:        networkPartitionParamSchema(),
		ObservableContract: networkPartitionObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
}

func networkTargetSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"namespace", "source_app", "target_service"},
		"properties": map[string]any{
			"namespace":      map[string]any{"type": "string", "minLength": 1},
			"source_app":     map[string]any{"type": "string", "minLength": 1},
			"target_service": map[string]any{"type": "string", "minLength": 1},
			// `from` / `both` would require swapping selector ↔ target in
			// the rendered CR (chaos-mesh interprets `selector` as the
			// destination when direction=from). Step 2 ships `to` only —
			// expand once a benchmark needs the inverse direction.
			"direction": map[string]any{
				"type":    "string",
				"enum":    []any{"to"},
				"default": "to",
			},
		},
	}
}

func durationParam() map[string]any {
	return map[string]any{
		"type": "integer", "minimum": 1, "maximum": 3600, "default": 60,
	}
}

func correlationParam() map[string]any {
	return map[string]any{
		"type": "integer", "minimum": 0, "maximum": 100, "default": 0,
	}
}

func networkDelayParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"latency_ms"},
		"properties": map[string]any{
			"latency_ms":      map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
			"jitter_ms":       map[string]any{"type": "integer", "minimum": 0},
			"correlation_pct": correlationParam(),
			"duration_s":      durationParam(),
		},
	}
}

func networkLossParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"loss_pct"},
		"properties": map[string]any{
			"loss_pct":        map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"correlation_pct": correlationParam(),
			"duration_s":      durationParam(),
		},
	}
}

func networkDuplicateParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"duplicate_pct"},
		"properties": map[string]any{
			"duplicate_pct":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"correlation_pct": correlationParam(),
			"duration_s":      durationParam(),
		},
	}
}

func networkCorruptParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"corrupt_pct"},
		"properties": map[string]any{
			"corrupt_pct":     map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"correlation_pct": correlationParam(),
			"duration_s":      durationParam(),
		},
	}
}

func networkBandwidthParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		// chaos-mesh BandwidthSpec marks limit + buffer non-optional (no
		// omitempty + Minimum=1); leaving them off makes the apiserver
		// webhook reject the CR. They're workload-sensitive so no default.
		"required": []any{"rate_kbps", "limit", "buffer"},
		"properties": map[string]any{
			"rate_kbps":  map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000},
			"limit":      map[string]any{"type": "integer", "minimum": 1},
			"buffer":     map[string]any{"type": "integer", "minimum": 1},
			"duration_s": durationParam(),
		},
	}
}

func networkPartitionParamSchema() JSONMap {
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

func networkDelayObservableContract() JSONMap {
	return JSONMap{
		"name": "network_delay",
		"contract": map[string]any{
			"baseline_window_s":  60,
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.duration on cross-service call from source_app to target_service >= latency_ms * 0.5"},
			},
		},
	}
}

func networkLossObservableContract() JSONMap {
	return JSONMap{
		"name": "network_loss",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.error_rate or retry_count on edge(source_app -> target_service) > baseline + min(loss_pct/200, 0.3)"},
			},
		},
	}
}

func networkDuplicateObservableContract() JSONMap {
	// No robust trace signal for duplicate — TCP absorbs dup-ACKs and
	// spans rarely encode UDP reorder. Contract hardens in step 3
	// (capability catalog migration) once we wire node netdev_tx counters.
	return JSONMap{
		"name": "network_duplicate",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions":   []any{},
		},
	}
}

func networkCorruptObservableContract() JSONMap {
	return JSONMap{
		"name": "network_corrupt",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.error_rate on edge(source_app -> target_service) > baseline + corrupt_pct/300 (TCP checksum-fail retransmits)"},
			},
		},
	}
}

func networkBandwidthObservableContract() JSONMap {
	// Assertion is sensitive to payload size — only payloads above the
	// `buffer` threshold exhibit the cap. Step 3 will gate the assertion
	// on observed payload_bytes.
	return JSONMap{
		"name": "network_bandwidth",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.duration on cross-service call increases proportional to payload_bytes / rate_kbps"},
			},
		},
	}
}

func networkPartitionObservableContract() JSONMap {
	return JSONMap{
		"name": "network_partition",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions": []any{
				map[string]any{"assertion": "span.error_rate on edge(source_app -> target_service) > 0.9 during injection_window_s"},
			},
		},
	}
}
