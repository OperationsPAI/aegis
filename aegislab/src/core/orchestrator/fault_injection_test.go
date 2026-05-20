package consumer

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/model"

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

// TestCatalogPreflight_HoistedAboveNamespaceLock pins the M4 fix (step 5b R3):
// runCatalogPreflight must be invoked BEFORE monitor.CheckNamespaceToInject in
// executeFaultInjection. Holding the ns lock across the preflight pins it for
// up to catalogPreflightTimeout × N (≈ 5s × 10 = 50s for a 10-spec batch) and
// serialises concurrent injects on the same ns for an HTTP call that touches
// no lock-protected state.
//
// We assert on the AST source-order rather than spinning up a real Redis +
// monitor + httptest stack: the only thing that can regress is the lexical
// order of the two calls inside executeFaultInjection, and an AST check is
// both deterministic and survives signature drift better than a behavioural
// test with extensive mocking.
func TestCatalogPreflight_HoistedAboveNamespaceLock(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fault_injection.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err, "parse fault_injection.go")

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Name.Name == "executeFaultInjection" {
			fn = fd
			break
		}
	}
	require.NotNil(t, fn, "executeFaultInjection function not found")

	var preflightPos, checkLockPos token.Pos
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if f.Name == "runCatalogPreflight" && !preflightPos.IsValid() {
				preflightPos = call.Pos()
			}
		case *ast.SelectorExpr:
			if f.Sel.Name == "CheckNamespaceToInject" && !checkLockPos.IsValid() {
				checkLockPos = call.Pos()
			}
		}
		return true
	})

	require.True(t, preflightPos.IsValid(), "runCatalogPreflight call site not found in executeFaultInjection")
	require.True(t, checkLockPos.IsValid(), "CheckNamespaceToInject call site not found in executeFaultInjection")
	require.Less(t, int(preflightPos), int(checkLockPos),
		"runCatalogPreflight must precede CheckNamespaceToInject (M4 hoist); "+
			"otherwise the catalog read pins the ns lock for ~5s per spec in the batch")
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
