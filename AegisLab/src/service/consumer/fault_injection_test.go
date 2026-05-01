package consumer

import (
	"context"
	"errors"
	"testing"

	"aegis/consts"
	"aegis/dto"
	"aegis/model"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
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

// TestRecaptureGroundtruth_OverwritesPodNameWhenServiceMatches pins the
// RestartPedestal pod-rename fix: when the loop-time GT recorded pod
// `ts-order-service-7d6b-aaaa` and the helm-upgrade in between rolled it to
// `ts-order-service-7d6b-bbbb`, recapture must surface the new name without
// flagging a service mismatch (label-stable workloads keep the same service).
func TestRecaptureGroundtruth_OverwritesPodNameWhenServiceMatches(t *testing.T) {
	prior := model.Groundtruth{
		Service:   []string{"ts-order-service"},
		Pod:       []string{"ts-order-service-7d6b-aaaa"},
		Container: []string{"ts-order-service"},
	}
	getter := func(_ context.Context) (chaos.Groundtruth, error) {
		return chaos.Groundtruth{
			Service:   []string{"ts-order-service"},
			Pod:       []string{"ts-order-service-7d6b-bbbb"},
			Container: []string{"ts-order-service"},
		}, nil
	}

	fresh, mismatch, err := recaptureGroundtruth(context.Background(), getter, prior)
	require.NoError(t, err)
	require.False(t, mismatch, "service set is identical; mismatch flag must be false")
	require.Equal(t, []string{"ts-order-service-7d6b-bbbb"}, fresh.Pod, "pod name must reflect the post-recapture value, not the loop-time stale one")
	require.Equal(t, []string{"ts-order-service"}, fresh.Service)
	require.Equal(t, []string{"ts-order-service"}, fresh.Container)
}

// TestRecaptureGroundtruth_FlagsServiceMismatchAndStillReturnsFresh covers the
// pathological case where the spec resolves to a different service the second
// time (e.g. cache invalidation between calls). The fresh GT is what the CRD
// will actually target, so we persist it and let the caller log a warning.
func TestRecaptureGroundtruth_FlagsServiceMismatchAndStillReturnsFresh(t *testing.T) {
	prior := model.Groundtruth{Service: []string{"ts-order-service"}, Pod: []string{"a"}}
	getter := func(_ context.Context) (chaos.Groundtruth, error) {
		return chaos.Groundtruth{Service: []string{"ts-travel-service"}, Pod: []string{"b"}}, nil
	}

	fresh, mismatch, err := recaptureGroundtruth(context.Background(), getter, prior)
	require.NoError(t, err)
	require.True(t, mismatch, "service set differs; mismatch flag must be true")
	require.Equal(t, []string{"ts-travel-service"}, fresh.Service)
	require.Equal(t, []string{"b"}, fresh.Pod)
}

func TestRecaptureGroundtruth_PropagatesGetterError(t *testing.T) {
	prior := model.Groundtruth{Service: []string{"x"}}
	want := errors.New("kube list timeout")
	getter := func(_ context.Context) (chaos.Groundtruth, error) {
		return chaos.Groundtruth{}, want
	}

	_, _, err := recaptureGroundtruth(context.Background(), getter, prior)
	require.ErrorIs(t, err, want)
}
