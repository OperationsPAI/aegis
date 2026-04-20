package consumer

import (
	"testing"

	"aegis/consts"
	"aegis/dto"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/stretchr/testify/require"
)

func faultDurationPtr(v int) *int {
	return &v
}

func sampleInjectionPayload() map[string]any {
	return map[string]any{
		consts.InjectBenchmark:   dto.ContainerVersionItem{ID: 7, ContainerName: "clickhouse"},
		consts.InjectPreDuration: float64(5),
		consts.InjectGuidedConfigs: []guidedcli.GuidedConfig{{
			System:     "ts",
			SystemType: "ts",
			Namespace:  "ts",
			App:        "ts-order-service",
			ChaosType:  "PodKill",
			Duration:   faultDurationPtr(1),
		}},
		consts.InjectNamespace:  "ts",
		consts.InjectPedestal:   "ts",
		consts.InjectPedestalID: float64(11),
		consts.InjectLabels:     []dto.LabelItem{{Key: "experiment", Value: "guided-only"}},
		consts.InjectSystem:     "ts",
	}
}

func TestParseInjectionPayloadAcceptsGuidedConfigs(t *testing.T) {
	payload, err := parseInjectionPayload(sampleInjectionPayload())
	require.NoError(t, err)
	require.Len(t, payload.guidedConfigs, 1)
	require.Equal(t, "PodKill", payload.guidedConfigs[0].ChaosType)
	require.Equal(t, "ts", payload.namespace)
	require.Equal(t, 11, payload.pedestalID)
}

func TestParseInjectionPayloadRejectsLegacyNodes(t *testing.T) {
	payload := sampleInjectionPayload()
	delete(payload, consts.InjectGuidedConfigs)
	payload["nodes"] = []map[string]any{{
		"value": 4,
		"children": map[string]any{
			"4": map[string]any{"children": map[string]any{"0": map[string]any{"value": 1}}},
		},
	}}

	_, err := parseInjectionPayload(payload)
	require.ErrorContains(t, err, consts.InjectGuidedConfigs)
}
