package chaos

import "time"

var SeedsTime = []Capability{
	{
		Name:               "time_skew",
		TargetSchema:       timeSkewTargetSchema(),
		ParamSchema:        timeSkewParamSchema(),
		ObservableContract: timeSkewObservableContract(),
		Status:             CapExperimental,
		CreatedAt:          time.Now().UTC(),
	},
}

func timeSkewTargetSchema() JSONMap {
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

func timeSkewParamSchema() JSONMap {
	return JSONMap{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"offset_s"},
		"properties": map[string]any{
			"offset_s":   map[string]any{"type": "integer", "minimum": -3600, "maximum": 3600},
			"duration_s": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600, "default": 60},
		},
	}
}

func timeSkewObservableContract() JSONMap {
	// capgen's note: time-skew has no universal trace signature; effects
	// surface only in app-specific timestamp use (JWT expiry, log skew,
	// scheduled jobs). The most reliable probe is process-level clock
	// comparison via debug exec, not telemetry — left to step 3.
	return JSONMap{
		"name": "time_skew",
		"contract": map[string]any{
			"injection_window_s": 60,
			"trace_assertions":   []any{},
		},
	}
}
