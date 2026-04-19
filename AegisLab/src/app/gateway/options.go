package gateway

import (
	"aegis/app"
	chaos "aegis/infra/chaos"
	k8s "aegis/infra/k8s"
	"aegis/internalclient/orchestratorclient"
	"aegis/internalclient/systemclient"
	container "aegis/module/container"
	execution "aegis/module/execution"
	group "aegis/module/group"
	injection "aegis/module/injection"
	metric "aegis/module/metric"
	notification "aegis/module/notification"
	system "aegis/module/system"
	systemmetric "aegis/module/systemmetric"
	task "aegis/module/task"
	trace "aegis/module/trace"

	"go.uber.org/fx"
)

// Options builds the dedicated api-gateway runtime.
func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.ObserveOptions(),
		app.DataOptions(),
		app.CoordinationOptions(),
		app.BuildInfraOptions(),
		chaos.Module,
		k8s.Module,
		app.ProducerHTTPOptions(port),
		app.RequireConfiguredTargets(
			"api-gateway",
			app.RequiredConfigTarget{Name: "orchestrator-service", PrimaryKey: "clients.orchestrator.target", LegacyKey: "orchestrator.grpc.target"},
			app.RequiredConfigTarget{Name: "system-service", PrimaryKey: "clients.system.target", LegacyKey: "system.grpc.target"},
		),
		orchestratorclient.Module,
		systemclient.Module,
		fx.Decorate(func(local execution.HandlerService, remote *orchestratorclient.Client) execution.HandlerService {
			return remoteAwareExecutionService{
				HandlerService: local,
				orchestrator:   remote,
			}
		}),
		fx.Decorate(func(local injection.HandlerService, remote *orchestratorclient.Client) injection.HandlerService {
			return remoteAwareInjectionService{
				HandlerService: local,
				orchestrator:   remote,
			}
		}),
		fx.Decorate(func(local task.HandlerService, remote *orchestratorclient.Client) task.HandlerService {
			return remoteAwareTaskService{
				HandlerService: local,
				orchestrator:   remote,
			}
		}),
		fx.Decorate(func(local trace.HandlerService, remote *orchestratorclient.Client) trace.HandlerService {
			return remoteAwareTraceService{
				HandlerService: local,
				orchestrator:   remote,
			}
		}),
		fx.Decorate(func(local group.HandlerService, remote *orchestratorclient.Client) group.HandlerService {
			return remoteAwareGroupService{
				HandlerService: local,
				orchestrator:   remote,
			}
		}),
		fx.Decorate(func(local notification.HandlerService, remote *orchestratorclient.Client) notification.HandlerService {
			return remoteAwareNotificationService{
				HandlerService: local,
				orchestrator:   remote,
			}
		}),
		fx.Decorate(func(local metric.HandlerService, orchestrator *orchestratorclient.Client, containerSvc container.HandlerService) metric.HandlerService {
			return remoteAwareMetricService{
				HandlerService: local,
				orchestrator:   orchestrator,
				containerSvc:   containerSvc,
			}
		}),
		fx.Decorate(func(local system.HandlerService, remote *systemclient.Client) system.HandlerService {
			return remoteAwareSystemService{
				HandlerService: local,
				system:         remote,
			}
		}),
		fx.Decorate(func(local systemmetric.HandlerService, remote *systemclient.Client) systemmetric.HandlerService {
			return remoteAwareSystemMetricService{
				HandlerService: local,
				system:         remote,
			}
		}),
	)
}
