package consumer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/dto"

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

func TestFaultInjectionStartedPayload_CarriesExecutorPath(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		executor string
	}{
		{"chaos-service path", "fault_injection.go", "ExecutorPathChaosService"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			astFile, err := parser.ParseFile(fset, tc.file, nil, parser.ParseComments)
			require.NoError(t, err)

			var found bool
			ast.Inspect(astFile, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "withEvent" || len(call.Args) != 2 {
					return true
				}
				eventArg, ok := call.Args[0].(*ast.SelectorExpr)
				if !ok || eventArg.Sel.Name != "EventFaultInjectionStarted" {
					return true
				}
				lit, ok := call.Args[1].(*ast.CompositeLit)
				if !ok {
					t.Fatalf("EventFaultInjectionStarted emit in %s must pass a struct literal, got %T", tc.file, call.Args[1])
				}
				ltype, ok := lit.Type.(*ast.SelectorExpr)
				require.True(t, ok, "payload literal in %s must be a qualified type", tc.file)
				require.Equal(t, "FaultInjectionStartedPayload", ltype.Sel.Name)

				var pathValue string
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "ExecutorPath" {
						continue
					}
					if sel, ok := kv.Value.(*ast.SelectorExpr); ok {
						pathValue = sel.Sel.Name
					}
				}
				require.Equal(t, tc.executor, pathValue,
					"EventFaultInjectionStarted emit in %s must set ExecutorPath=%s", tc.file, tc.executor)
				found = true
				return false
			})
			require.True(t, found, "no EventFaultInjectionStarted withEvent call found in %s", tc.file)
		})
	}
}

