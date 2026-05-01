package consumer

import (
	"testing"
	"time"

	"aegis/dto"

	"github.com/stretchr/testify/assert"
)

// TestGetAlgoJobEnvVars_PassesPedestalAsBenchmarkSystem locks in the contract
// that the algo Job receives BENCHMARK_SYSTEM=<pedestal> when the datapack
// has a pedestal. The detector entrypoint reads this env var to pick the
// pedestal-specific entrance service; if it's missing the detector silently
// defaults to "ts" and fails every non-train-ticket run with
// "No entrance traffic found in normal or abnormal trace data".
func TestGetAlgoJobEnvVars_PassesPedestalAsBenchmarkSystem(t *testing.T) {
	now := time.Now()
	payload := &executionPayload{
		algorithm: dto.ContainerVersionItem{
			ID:            1,
			Name:          "1.0.0",
			ContainerName: "detector",
		},
		datapack: dto.InjectionItem{
			ID:        42,
			Name:      "batch-hs-001",
			StartTime: now.Add(-10 * time.Minute),
			EndTime:   now,
			Pedestal:  "hs",
		},
	}

	envVars, err := getAlgoJobEnvVars("task-1", 7, "/data", "/exp", payload)
	assert.NoError(t, err)

	got := map[string]string{}
	for _, e := range envVars {
		got[e.Name] = e.Value
	}
	assert.Equal(t, "hs", got["BENCHMARK_SYSTEM"],
		"detector job must receive pedestal name as BENCHMARK_SYSTEM; missing/wrong value silently mis-routes non-ts datapacks")
}

// TestGetAlgoJobEnvVars_OmitsBenchmarkSystemWhenPedestalUnknown verifies that
// when the datapack has no pedestal association (manual upload), the env var
// is left unset rather than defaulting to a misleading value. The detector
// entrypoint will then fail-fast with a clear error instead of silently
// running against the wrong pedestal.
func TestGetAlgoJobEnvVars_OmitsBenchmarkSystemWhenPedestalUnknown(t *testing.T) {
	now := time.Now()
	payload := &executionPayload{
		algorithm: dto.ContainerVersionItem{ID: 1, Name: "1.0.0", ContainerName: "detector"},
		datapack: dto.InjectionItem{
			ID:        99,
			Name:      "manual-upload-1",
			StartTime: now.Add(-1 * time.Minute),
			EndTime:   now,
		},
	}

	envVars, err := getAlgoJobEnvVars("task-2", 8, "/data", "/exp", payload)
	assert.NoError(t, err)

	for _, e := range envVars {
		assert.NotEqual(t, "BENCHMARK_SYSTEM", e.Name,
			"BENCHMARK_SYSTEM must not be set when pedestal is unknown")
	}
}
