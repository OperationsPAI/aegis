package consumer

import (
	buildkit "aegis/infra/buildkit"
	helm "aegis/infra/helm"
	k8s "aegis/infra/k8s"
	redis "aegis/infra/redis"

	"gorm.io/gorm"
)

type RuntimeDeps struct {
	DB                 *gorm.DB
	Monitor            NamespaceMonitor
	RestartRateLimiter *TokenBucketRateLimiter
	// NsWarmingRateLimiter gates the post-helm-apply workload-readiness
	// probe in RestartPedestal. Decoupled from RestartRateLimiter so the
	// "API server hammer" bound stays small (default 5) while the
	// "namespaces cold-starting in parallel" bound can be much larger
	// (default 30). See PR #205.
	NsWarmingRateLimiter *TokenBucketRateLimiter
	BuildRateLimiter     *TokenBucketRateLimiter
	// BuildDatapackRateLimiter caps concurrent BuildDatapack tasks. Each
	// BuildDatapack Job issues ~30 ClickHouse queries; without this cap
	// the inject-loop fan-out has overrun ClickHouse's max_concurrent_queries
	// ceiling. Default 8 is a conservative bound (~240 concurrent CH queries)
	// that fits inside the bumped 2000 ceiling with plenty of headroom for
	// other consumers. See also rate_limiting.max_concurrent_build_datapack.
	BuildDatapackRateLimiter *TokenBucketRateLimiter
	AlgorithmRateLimiter     *TokenBucketRateLimiter
	RedisGateway         *redis.Gateway
	K8sGateway           *k8s.Gateway
	BuildKitGateway      *buildkit.Gateway
	HelmGateway          *helm.Gateway
	FaultBatchManager    *FaultBatchManager
	ExecutionOwner       ExecutionOwner
	InjectionOwner       InjectionOwner
	// TaskRegistry is the framework-aggregated dispatch table; nil in
	// tests that don't exercise dispatchTask. See service/consumer.Module.
	TaskRegistry *TaskRegistry
	// FreshnessProbe gates BuildDatapack on otel.otel_traces ingestion
	// freshness so prepare_inputs.py does not race the OTel exporter
	// retry queue and produce empty abnormal_traces.parquet (issue #210).
	// Optional in tests; when nil, the executor skips the pre-flight
	// (preserves pre-PR behavior).
	FreshnessProbe FreshnessProbe
}
