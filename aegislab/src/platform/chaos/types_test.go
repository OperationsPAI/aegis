package chaos_test

import (
	"testing"

	localChaos "aegis/platform/chaos"

	chaosHandler "github.com/OperationsPAI/chaos-experiment/handler"
)

// TestTypeAliases verifies that local re-exports are true Go type aliases of the
// upstream chaos-experiment types. If any of these assignments stop compiling,
// phase 2a's "no behavior change" contract has been broken.
func TestTypeAliases(t *testing.T) {
	var _ chaosHandler.SystemType = localChaos.SystemType("")
	var _ chaosHandler.ChaosType = localChaos.ChaosType(0)
	var _ chaosHandler.Groundtruth = localChaos.Groundtruth{}
	var _ chaosHandler.InjectionConf = localChaos.InjectionConf{}
	var _ chaosHandler.SystemConfig = localChaos.SystemConfig{}

	var store localChaos.MetadataStore
	var _ chaosHandler.MetadataStore = store

	if localChaos.ChaosTypeMap == nil {
		t.Fatal("ChaosTypeMap re-export is nil")
	}
	if localChaos.ChaosNameMap == nil {
		t.Fatal("ChaosNameMap re-export is nil")
	}
	if localChaos.IsSystemRegistered == nil {
		t.Fatal("IsSystemRegistered re-export is nil")
	}
	if localChaos.SetMetadataStore == nil {
		t.Fatal("SetMetadataStore re-export is nil")
	}
}
