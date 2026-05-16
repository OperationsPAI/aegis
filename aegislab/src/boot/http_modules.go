package app

import (
	"context"

	blobclient "aegis/clients/blob"
	configcenterclient "aegis/clients/configcenter"
	gateway "aegis/clients/gateway"
	notificationclient "aegis/clients/notification"
	chaossystem "aegis/core/domain/chaossystem"
	container "aegis/core/domain/container"
	dataset "aegis/core/domain/dataset"
	execution "aegis/core/domain/execution"
	group "aegis/core/domain/group"
	injection "aegis/core/domain/injection"
	pedestal "aegis/core/domain/pedestal"
	task "aegis/core/domain/task"
	configcenter "aegis/crud/admin/configcenter"
	ratelimiter "aegis/crud/admin/ratelimiter"
	widget "aegis/crud/admin/widget"
	label "aegis/crud/iam/label"
	project "aegis/crud/iam/project"
	team "aegis/crud/iam/team"
	notification "aegis/crud/messaging/notification"
	evaluation "aegis/crud/observability/evaluation"
	metric "aegis/crud/observability/metric"
	observation "aegis/crud/observability/observation"
	sdk "aegis/crud/observability/sdk"
	system "aegis/crud/observability/system"
	systemmetric "aegis/crud/observability/systemmetric"
	trace "aegis/crud/observability/trace"
	blob "aegis/crud/storage/blob"
	"aegis/platform/router"

	"go.uber.org/fx"
)

// apiHTTPModules is the canonical list of HTTP-registering fx.Modules
// the aegis-api aegis-api binary serves.
//
// What's NOT in this list — and why:
//
//   - iam/user, iam/auth, iam/rbac, iam/sso, clients/sso
//     IAM is owned by the standalone aegis-sso process (boot/sso). The
//     aegis-api reaches SSO through the middleware.TokenVerifier /
//     middleware.PermissionChecker interfaces, which clients/sso provides
//     when wired into binaries that need to *call* SSO (see ssoclient.Module
//     in producer.go). The IAM modules themselves only mount their HTTP
//     routes + own their PermissionRegistrar / RoleGrantsRegistrar inside
//     the SSO binary's fx graph.
//
// To add a new HTTP module to the aegis-api: import it above and append
// `xyz.Module` to the slice. There is intentionally no generator.
func apiHTTPModules() []fx.Option {
	return []fx.Option{
		// core/domain
		chaossystem.Module,
		container.Module,
		dataset.Module,
		execution.Module,
		group.Module,
		injection.Module,
		pedestal.Module,
		task.Module,

		// crud/iam (non-SSO-owned)
		label.Module,
		project.Module,
		team.Module,

		// crud/observability
		evaluation.Module,
		metric.Module,
		observation.Module,
		sdk.Module,
		system.Module,
		systemmetric.Module,
		trace.Module,

		// crud/storage
		blob.Module,

		// crud/messaging
		notification.Module,

		// crud/admin
		configcenter.Module,
		ratelimiter.Module,
		widget.Module,

		// in-process clients (HTTP/gRPC adapters consumed by handlers)
		blobclient.Module,
		configcenterclient.Module,
		gateway.Module,
		notificationclient.Module,

		// HTTP router (registrar aggregator + gin engine)
		router.Module,
	}
}

// apiHTTPModuleNames is the parallel list of module identifiers used
// by smoke / registry tests. Kept in sync with apiHTTPModules above.
func apiHTTPModuleNames() []string {
	return []string{
		"chaossystem", "container", "dataset", "execution", "group",
		"injection", "pedestal", "task",
		"label", "project", "team",
		"evaluation", "metric", "observation", "sdk", "system",
		"systemmetric", "trace",
		"blob",
		"notification",
		"configcenter", "ratelimiter", "widget",
		"blobclient", "configcenterclient", "gateway", "notificationclient",
	}
}

//go:generate echo "no generator: edit apiHTTPModules above by hand"

// taskCancellerAdapter bridges *task.Service into the narrow
// injection.TaskCanceller seam so injection cancel can cascade-cancel the
// task that backs the injection without injection.Service importing the
// task package (and risking the reverse import cycle).
type taskCancellerAdapter struct{ svc *task.Service }

func (a taskCancellerAdapter) CancelTask(ctx context.Context, taskID string) (*injection.CancelInjectionTaskResult, error) {
	r, err := a.svc.CancelTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return &injection.CancelInjectionTaskResult{
		TaskID:            r.TaskID,
		State:             r.State,
		Message:           r.Message,
		DeletedPodChaos:   r.DeletedPodChaos,
		RemovedRedisTasks: r.RemovedRedisTasks,
	}, nil
}

func newTaskCanceller(svc *task.Service) injection.TaskCanceller {
	return taskCancellerAdapter{svc: svc}
}

// chaosSystemWriterAdapter bridges chaossystem.Writer (admin-scoped, broad)
// to the narrow injection.ChaosSystemWriter the injection module needs for
// the #156 namespace-count bump. Defined at the app level so the injection
// package can avoid importing chaossystem (which would close the
// chaossystem→initialization→consumer→execution→injection import cycle).
func chaosSystemWriterAdapter(w chaossystem.Writer) injection.ChaosSystemWriter {
	return w
}

func ExecutionInjectionOwnerModules() fx.Option {
	return fx.Options(
		chaossystem.Module,
		container.Module,
		dataset.Module,
		execution.Module,
		injection.Module,
		label.Module,
		// injection/container/dataset constructors switch on
		// `jfs.backend` and need a blob.Client when backend = "s3".
		// blob.Module provides the in-process Service that LocalClient
		// wraps. Filesystem mode still works without exercising it.
		blob.Module,
		blobclient.Module,
		fx.Provide(chaosSystemWriterAdapter),
		fx.Provide(newTaskCanceller),
	)
}

func ProducerHTTPModules() fx.Option {
	return fx.Options(
		fx.Options(apiHTTPModules()...),
		fx.Provide(chaosSystemWriterAdapter),
		fx.Provide(newTaskCanceller),
	)
}
