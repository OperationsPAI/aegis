package guidedcli

import (
	"github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

// Re-export registration / metadata-store entry points so external callers
// (notably aegislab) can drive the chaos-experiment runtime registry without
// importing the handler package directly.

type (
	SystemConfig             = handler.SystemConfig
	MetadataStore            = handler.MetadataStore
	ServiceEndpointData      = handler.ServiceEndpointData
	DatabaseOperationData    = handler.DatabaseOperationData
	GRPCOperationData        = handler.GRPCOperationData
	JavaClassMethodData      = handler.JavaClassMethodData
	RuntimeMutatorTargetData = handler.RuntimeMutatorTargetData
	NetworkPairData          = handler.NetworkPairData
)

func RegisterSystem(cfg SystemConfig) error   { return handler.RegisterSystem(cfg) }
func UpdateSystem(cfg SystemConfig) error     { return handler.UpdateSystem(cfg) }
func UnregisterSystem(name string) error      { return handler.UnregisterSystem(name) }
func IsSystemRegistered(name string) bool     { return handler.IsSystemRegistered(name) }
func SetMetadataStore(store MetadataStore)    { handler.SetMetadataStore(store) }

func GetAllSystemTypes() []string {
	systems := systemconfig.GetAllRegisteredSystems()
	out := make([]string, len(systems))
	for i, s := range systems {
		out[i] = s.String()
	}
	return out
}
