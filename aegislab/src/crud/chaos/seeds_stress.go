package chaos

import "time"

var SeedsStress = []Capability{
	{
		Name:               "cpu_stress",
		TargetSchema:       stressTargetSchema(),
		ParamSchema:        cpuStressParamSchema(),
		ObservableContract: cpuStressObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "memory_stress",
		TargetSchema:       stressTargetSchema(),
		ParamSchema:        memoryStressParamSchema(),
		ObservableContract: memoryStressObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
}

func stressTargetSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"namespace", "app", "container"},
		"properties": map[string]any{
			"namespace": map[string]any{"type": "string", "minLength": 1},
			"app":       map[string]any{"type": "string", "minLength": 1},
			"container": map[string]any{"type": "string", "minLength": 1},
		},
	}
}

func cpuStressParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"load_pct"},
		"properties": map[string]any{
			"load_pct":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"workers":    map[string]any{"type": "integer", "minimum": 1, "maximum": 8, "default": 1},
			"duration_s": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600, "default": 60},
		},
	}
}

func memoryStressParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"size_mib"},
		"properties": map[string]any{
			"size_mib":   map[string]any{"type": "integer", "minimum": 1, "maximum": 65536},
			"workers":    map[string]any{"type": "integer", "minimum": 1, "maximum": 8, "default": 1},
			"duration_s": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600, "default": 60},
		},
	}
}

func cpuStressObservableContract() JSONMap {
	return JSONMap{
		"name": "cpu_stress",
		"contract": map[string]any{
			"baseline_window_s":  60,
			"injection_window_s": 60,
			"metric_assertions": []any{
				map[string]any{"assertion": "container_cpu_usage_seconds_total rate increases by >= load_pct * 0.5 over baseline"},
			},
			"tolerance": map[string]any{"false_positive_rate": 0.1},
		},
	}
}

func memoryStressObservableContract() JSONMap {
	return JSONMap{
		"name": "memory_stress",
		"contract": map[string]any{
			"baseline_window_s":  60,
			"injection_window_s": 60,
			"metric_assertions": []any{
				map[string]any{"assertion": "container_memory_working_set_bytes increases by >= size_mib * 0.7 * MiB over baseline"},
			},
		},
	}
}
