package injection

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"aegis/consts"
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
		Pedestal:  &dto.ContainerSpec{ContainerRef: dto.ContainerRef{Name: "ts", Version: "1.0.0"}},
		Benchmark: &dto.ContainerSpec{ContainerRef: dto.ContainerRef{Name: "bench", Version: "1.0.0"}},
		Specs:     [][]GuidedSpec{{cfg}},
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

// TestSubmitInjectionReqValidateRejectsMissingDuration is the issue-#176
// defense-in-depth assertion: when the resolver fails to default-fill
// Duration (i.e. the chaos-experiment builder errored and shipped an
// un-normalized config), the validator must point at the resolver as the
// likely cause, NOT regurgitate the generic "duration > 0" message.
func TestSubmitInjectionReqValidateRejectsMissingDuration(t *testing.T) {
	req := validSubmitInjectionReq()
	req.Specs[0][0].Duration = nil
	req.Specs[0][0].ChaosType = "JVMMemoryStress"

	err := req.Validate()
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "duration is missing")
	require.Contains(t, msg, "JVMMemoryStress")
	require.Contains(t, msg, "guided resolver")
	// Crucially: must NOT say "must be greater than 0" for the nil case —
	// that's the misleading message that issue #176 traced to.
	require.NotContains(t, msg, "duration must be greater than 0")
}

// TestSubmitInjectionReqValidateOverridesCallerSuppliedDuration is the
// hardcoded-time-window guarantee: any non-nil per-spec duration shipped by
// the caller (loop agent, manual curl, ...) is silently overwritten with
// consts.FixedAbnormalWindowMinutes so external clients can't drift the
// abnormal window. The chaos-experiment resolver's own default-fill keeps
// the legacy code path producing a non-nil pointer even when the CLI used
// to set --duration explicitly.
func TestSubmitInjectionReqValidateOverridesCallerSuppliedDuration(t *testing.T) {
	for _, in := range []int{0, 1, 9999} {
		t.Run(fmt.Sprintf("input=%d", in), func(t *testing.T) {
			req := validSubmitInjectionReq()
			v := in
			req.Specs[0][0].Duration = &v

			require.NoError(t, req.Validate())
			require.NotNil(t, req.Specs[0][0].Duration)
			require.Equal(t, consts.FixedAbnormalWindowMinutes, *req.Specs[0][0].Duration,
				"per-spec duration must be pinned to consts.FixedAbnormalWindowMinutes")
		})
	}
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

// TestMergeSpecServicesForDupCheck covers the #157 regression: a single
// spec whose groundtruth legitimately lists the same service twice
// (e.g. HTTP self-loop `GET /` on `front-end`) must NOT flag a self-
// duplicate, and cross-spec conflicts must still be reported.
func TestMergeSpecServicesForDupCheck(t *testing.T) {
	t.Run("single spec with repeated service does not self-duplicate", func(t *testing.T) {
		uniq := map[string]int{}
		warnings := mergeSpecServicesForDupCheck(uniq, []string{"front-end", "front-end"}, 0)
		require.Empty(t, warnings, "a spec's own repeated service should not warn against itself")
		require.Equal(t, map[string]int{"front-end": 0}, uniq)
	})

	t.Run("cross-spec duplicate still warns", func(t *testing.T) {
		uniq := map[string]int{}
		_ = mergeSpecServicesForDupCheck(uniq, []string{"front-end", "front-end"}, 0)
		warnings := mergeSpecServicesForDupCheck(uniq, []string{"front-end"}, 1)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0], "positions 0 and 1")
	})

	t.Run("cross-spec duplicate among multi-service specs", func(t *testing.T) {
		uniq := map[string]int{}
		_ = mergeSpecServicesForDupCheck(uniq, []string{"a", "b"}, 0)
		warnings := mergeSpecServicesForDupCheck(uniq, []string{"b", "c"}, 1)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0], "service 'b'")
		require.Contains(t, warnings[0], "positions 0 and 1")
	})

	t.Run("empty service names are ignored", func(t *testing.T) {
		uniq := map[string]int{}
		warnings := mergeSpecServicesForDupCheck(uniq, []string{"", "front-end", ""}, 0)
		require.Empty(t, warnings)
		require.Equal(t, map[string]int{"front-end": 0}, uniq)
	})
}

func TestFirstGuidedNamespace(t *testing.T) {
	require.Equal(t, "", firstGuidedNamespace(nil))
	require.Equal(t, "", firstGuidedNamespace([]guidedcli.GuidedConfig{{Namespace: "  "}}))
	require.Equal(t, "sockshop14", firstGuidedNamespace([]guidedcli.GuidedConfig{
		{Namespace: ""},
		{Namespace: "sockshop14"},
		{Namespace: "sockshop15"},
	}))
}
