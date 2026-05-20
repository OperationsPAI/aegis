package consts

// Fault-injection executor path labels emitted on fault.injection.started.
// Deprecated: ExecutorPathChaosMeshDirect is no longer reachable (§11 step
// 5c removed the legacy chaos-mesh-direct dispatch). The remaining constant
// is consumed by the regression validator and the FaultInjectionStartedPayload
// field; will be dropped in phase 2 together with the payload field itself.
const (
	ExecutorPathChaosMeshDirect = "chaos-mesh-direct"
	ExecutorPathChaosService    = "chaos-service"
)
