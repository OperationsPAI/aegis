package chaos

import "time"

// SeedsPodChaosExtra carries the two PodChaos siblings of pod_kill —
// container_kill and pod_failure — lifted from
// `tools/capgen/output/capabilities.json`. Both ship `experimental`.
var SeedsPodChaosExtra = []Capability{
	{
		Name:               "container_kill",
		TargetSchema:       containerKillTargetSchema(),
		ParamSchema:        podExtraParamSchema(),
		ObservableContract: containerKillObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
	{
		Name:               "pod_failure",
		TargetSchema:       podFailureTargetSchema(),
		ParamSchema:        podExtraParamSchema(),
		ObservableContract: podFailureObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
}

func containerKillTargetSchema() JSONMap {
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

func podFailureTargetSchema() JSONMap {
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

func podExtraParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{},
		"properties": map[string]any{
			"duration_s": map[string]any{
				"type": "integer", "minimum": 1, "maximum": 3600, "default": 60,
			},
		},
	}
}

func containerKillObservableContract() JSONMap {
	return JSONMap{
		"name": "container_kill",
		"contract": map[string]any{
			"injection_window_s": 60,
			"k8s_assertions": []any{
				map[string]any{"assertion": "target_pod.container[name=target.container].restart_count increases by >= 1 within injection_window_s"},
			},
			"tolerance": map[string]any{"false_positive_rate": 0.05},
		},
	}
}

func podFailureObservableContract() JSONMap {
	return JSONMap{
		"name": "pod_failure",
		"contract": map[string]any{
			"injection_window_s": 60,
			"k8s_assertions": []any{
				map[string]any{"assertion": "target_pod.ready_condition == false for >= duration_s * 0.9"},
			},
			"tolerance": map[string]any{"false_positive_rate": 0.05},
		},
	}
}
