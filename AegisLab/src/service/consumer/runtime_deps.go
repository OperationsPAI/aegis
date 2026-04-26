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
	AlgorithmRateLimiter *TokenBucketRateLimiter
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
}
