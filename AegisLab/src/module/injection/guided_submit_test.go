package injection

import (
	"context"
	"encoding/json"
	"testing"

	"aegis/dto"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/stretchr/testify/require"
)

func guidedDurationPtr(v int) *int {
	return &v
}

func sampleGuidedConfig() guidedcli.GuidedConfig {
	return guidedcli.GuidedConfig{
		System:     "ts",
		SystemType: "ts",
		Namespace:  "ts",
		App:        "ts-order-service",
		ChaosType:  "PodKill",
		Duration:   guidedDurationPtr(1),
	}
}

func validSubmitInjectionReq() *SubmitInjectionReq {
	cfg := GuidedSpec(sampleGuidedConfig())
	return &SubmitInjectionReq{
		Pedestal:    &dto.ContainerSpec{ContainerRef: dto.ContainerRef{Name: "ts", Version: "1.0.0"}},
		Benchmark:   &dto.ContainerSpec{ContainerRef: dto.ContainerRef{Name: "bench", Version: "1.0.0"}},
		Interval:    10,
		PreDuration: 1,
		Specs:       [][]GuidedSpec{{cfg}},
	}
}

func TestGuidedSpecRejectsFriendlyFaultSpec(t *testing.T) {
	var req SubmitInjectionReq
	err := json.Unmarshal([]byte(`{"specs":[[{"type":"CPUStress","duration":"60s"}]]}`), &req)
	require.ErrorContains(t, err, "FriendlyFaultSpec payloads are no longer accepted")
}

func TestGuidedSpecRejectsRawNodePayload(t *testing.T) {
	var req SubmitInjectionReq
	err := json.Unmarshal([]byte(`{"specs":[[{"value":4,"children":{"4":{"children":{"0":{"value":1}}}}}]]}`), &req)
	require.ErrorContains(t, err, "raw chaos.Node payloads are no longer accepted")
}

func TestGuidedSpecRejectsMixedGuidedLegacyFields(t *testing.T) {
	var req SubmitInjectionReq
	err := json.Unmarshal([]byte(`{"specs":[[{"chaos_type":"PodKill","type":"CPUStress"}]]}`), &req)
	require.ErrorContains(t, err, "mixed guided/legacy fault spec fields are not supported")
}

func TestSubmitInjectionReqGuidedSpecs(t *testing.T) {
	cfg := sampleGuidedConfig()
	body, err := json.Marshal(map[string]any{
		"specs": [][]guidedcli.GuidedConfig{{cfg}},
	})
	require.NoError(t, err)

	var req SubmitInjectionReq
	require.NoError(t, json.Unmarshal(body, &req))

	configs := req.GuidedSpecs()
	require.Len(t, configs, 1)
	require.Len(t, configs[0], 1)
	require.Equal(t, cfg.ChaosType, configs[0][0].ChaosType)
}

func TestSubmitInjectionReqValidateRejectsEmptyBatch(t *testing.T) {
	req := validSubmitInjectionReq()
	req.Specs = [][]GuidedSpec{{}}

	err := req.Validate()
	require.ErrorContains(t, err, "must contain at least one guided config")
}

func TestParseBatchGuidedSpecs(t *testing.T) {
	cfg := sampleGuidedConfig()

	item, warning, err := parseBatchGuidedSpecs(context.Background(), "ts", 0, []guidedcli.GuidedConfig{cfg})
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, 1, item.faultDuration)
	require.Len(t, item.guidedConfigs, 1)
	require.Equal(t, cfg.ChaosType, item.guidedConfigs[0].ChaosType)
}

func TestParseBatchGuidedSpecsWarnsOnDuplicateServices(t *testing.T) {
	cfg := sampleGuidedConfig()

	item, warning, err := parseBatchGuidedSpecs(context.Background(), "ts", 2, []guidedcli.GuidedConfig{cfg, cfg})
	require.NoError(t, err)
	require.NotNil(t, item)
	require.Contains(t, warning, "duplicate service injections")
}
