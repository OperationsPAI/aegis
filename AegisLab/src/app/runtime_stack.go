package app

import (
	chaos "aegis/infra/chaos"
	k8s "aegis/infra/k8s"
	runtimeinfra "aegis/infra/runtime"
	controller "aegis/interface/controller"
	grpcruntime "aegis/interface/grpc/runtime"
	receiver "aegis/interface/receiver"
	worker "aegis/interface/worker"
	"aegis/internalclient/runtimeclient"
	"aegis/service/consumer"

	"go.uber.org/fx"
)

// RuntimeWorkerStackOptions provides the task / controller / grpc plumbing
// shared by the runtime-worker-service binary and by the collocated
// consumer / both modes.
//
// In collocated modes (consumer / both) the runtimeclient has no targets
// configured, so ExecutionOwner / InjectionOwner resolve to the local
// execution.Service / injection.Service wired by ExecutionInjectionOwnerModules.
//
// In the dedicated runtime-worker-service binary, consumer.RemoteOwnerOptions
// decorates the owners to route through the runtime-intake gRPC client
// (runtime-worker → api-gateway); see app/runtime/options.go.
func RuntimeWorkerStackOptions() fx.Option {
	return fx.Options(
		runtimeinfra.Module,
		chaos.Module,
		k8s.Module,
		runtimeclient.Module,
		consumer.Module,
		fx.Provide(
			consumer.NewMonitor,
			fx.Annotate(consumer.NewRestartPedestalRateLimiter, fx.ResultTags(`name:"restart_limiter"`)),
			fx.Annotate(consumer.NewNamespaceWarmingRateLimiter, fx.ResultTags(`name:"warming_limiter"`)),
			fx.Annotate(consumer.NewBuildContainerRateLimiter, fx.ResultTags(`name:"build_limiter"`)),
			fx.Annotate(consumer.NewAlgoExecutionRateLimiter, fx.ResultTags(`name:"algo_limiter"`)),
			consumer.NewFaultBatchManager,
			consumer.NewExecutionOwner,
			consumer.NewInjectionOwner,
		),
		worker.Module,
		controller.Module,
		grpcruntime.Module,
		receiver.Module,
	)
}
