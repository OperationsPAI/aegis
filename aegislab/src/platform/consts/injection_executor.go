package consts

import "strings"

// Per-system fault-injection executor flag. The full configcenter key is
// "aegis.injection.system.<system>.executor_authoritative"; unknown/empty
// values fall through to ExecutorPathChaosMeshDirect.
const (
	ExecutorFlagKeyPrefix = "aegis.injection.system."
	ExecutorFlagKeySuffix = ".executor_authoritative"

	ExecutorPathChaosMeshDirect = "chaos-mesh-direct"
	ExecutorPathChaosService    = "chaos-service"
)

// ExecutorFlagKey returns the full configcenter key (namespace + key) for the
// per-system executor flag.
func ExecutorFlagKey(system string) string {
	return ExecutorFlagKeyPrefix + strings.TrimSpace(system) + ExecutorFlagKeySuffix
}
