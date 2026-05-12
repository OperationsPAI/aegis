package worker

import (
	"context"

	buildkit "aegis/platform/buildkit"
	etcd "aegis/platform/etcd"
	helm "aegis/platform/helm"
	k8s "aegis/platform/k8s"
	redis "aegis/platform/redis"
	commonservice "aegis/service/common"
	"aegis/service/consumer"
	"aegis/service/initialization"

	"go.uber.org/fx"
	"gorm.io/gorm"
)

var Module = fx.Module("worker",
	fx.Provide(newLifecycle),
	fx.Invoke(registerLifecycle),
)

type Params struct {
	fx.In

	DB                   *gorm.DB
	RedisGateway         *redis.Gateway
	BuildKit             *buildkit.Gateway
	Helm                 *helm.Gateway
	K8sGateway           *k8s.Gateway
	Controller           *k8s.Controller
	Etcd                 *etcd.Gateway
	Monitor              consumer.NamespaceMonitor
	RestartLimiter       *consumer.TokenBucketRateLimiter `name:"restart_limiter"`
	WarmingLimiter       *consumer.TokenBucketRateLimiter `name:"warming_limiter"`
	BuildLimiter         *consumer.TokenBucketRateLimiter `name:"build_limiter"`
	BuildDatapackLimiter *consumer.TokenBucketRateLimiter `name:"build_datapack_limiter"`
	AlgoLimiter          *consumer.TokenBucketRateLimiter `name:"algo_limiter"`
	BatchManager   *consumer.FaultBatchManager
	ExecutionOwner consumer.ExecutionOwner
	InjectionOwner consumer.InjectionOwner
	TaskRegistry   *consumer.TaskRegistry
}

type Lifecycle struct {
	params    Params
	StartFunc func(context.Context) error
	StopFunc  func()
}

func newLifecycle(params Params) *Lifecycle {
	return &Lifecycle{params: params}
}

func (r *Lifecycle) start(ctx context.Context) error {
	if r.StartFunc != nil {
		return r.StartFunc(ctx)
	}
	params := r.params
	if err := initialization.InitializeConsumer(
		ctx,
		params.DB,
		params.Controller,
		params.Monitor,
		params.RedisGateway,
		params.Etcd,
		commonservice.NewConfigUpdateListener(ctx, params.DB, params.Etcd),
		params.RestartLimiter,
		params.WarmingLimiter,
		params.BuildLimiter,
		params.BuildDatapackLimiter,
		params.AlgoLimiter,
	); err != nil {
		return err
	}
	if err := params.RedisGateway.InitConcurrencyLock(ctx); err != nil {
		return err
	}

	go consumer.StartScheduler(ctx, params.RedisGateway)
	go consumer.ConsumeTasks(ctx, consumer.RuntimeDeps{
		DB:                       params.DB,
		Monitor:                  params.Monitor,
		RestartRateLimiter:       params.RestartLimiter,
		NsWarmingRateLimiter:     params.WarmingLimiter,
		BuildRateLimiter:         params.BuildLimiter,
		BuildDatapackRateLimiter: params.BuildDatapackLimiter,
		AlgorithmRateLimiter:     params.AlgoLimiter,
		RedisGateway:             params.RedisGateway,
		K8sGateway:               params.K8sGateway,
		BuildKitGateway:          params.BuildKit,
		HelmGateway:              params.Helm,
		FaultBatchManager:        params.BatchManager,
		ExecutionOwner:           params.ExecutionOwner,
		InjectionOwner:           params.InjectionOwner,
		TaskRegistry:             params.TaskRegistry,
		FreshnessProbe:           consumer.NewClickHouseFreshnessProbe(),
	})
	return nil
}

func (r *Lifecycle) stop() {
	if r.StopFunc != nil {
		r.StopFunc()
	}
}

func registerLifecycle(lc fx.Lifecycle, runner *Lifecycle) {
	var (
		workerCtx context.Context
		cancel    context.CancelFunc
	)

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			workerCtx, cancel = context.WithCancel(context.WithoutCancel(ctx))
			return runner.start(workerCtx)
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			runner.stop()
			return nil
		},
	})
}
