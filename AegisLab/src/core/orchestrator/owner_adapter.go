package consumer

import (
	"context"
	"fmt"

	"aegis/platform/dto"
	"aegis/clients/runtime"
	execution "aegis/core/domain/execution"
	injection "aegis/core/domain/injection"

	"go.uber.org/fx"
)

// ExecutionOwner captures the execution owner operations used by runtime code.
type ExecutionOwner interface {
	CreateExecution(context.Context, *execution.RuntimeCreateExecutionReq) (int, error)
	GetExecution(context.Context, int) (*execution.ExecutionDetailResp, error)
	UpdateExecutionState(context.Context, *execution.RuntimeUpdateExecutionStateReq) error
}

// InjectionOwner captures the injection owner operations used by runtime code.
type InjectionOwner interface {
	CreateInjection(context.Context, *injection.RuntimeCreateInjectionReq) (*dto.InjectionItem, error)
	UpdateInjectionState(context.Context, *injection.RuntimeUpdateInjectionStateReq) error
	UpdateInjectionTimestamps(context.Context, *injection.RuntimeUpdateInjectionTimestampReq) (*dto.InjectionItem, error)
}

// localExecutionOwner delegates directly to the in-process execution.Service.
type localExecutionOwner struct {
	svc *execution.Service
}

func (a localExecutionOwner) CreateExecution(ctx context.Context, req *execution.RuntimeCreateExecutionReq) (int, error) {
	if a.svc == nil {
		return 0, fmt.Errorf("missing execution owner service")
	}
	return a.svc.CreateExecutionRecord(ctx, req)
}

func (a localExecutionOwner) GetExecution(ctx context.Context, executionID int) (*execution.ExecutionDetailResp, error) {
	if a.svc == nil {
		return nil, fmt.Errorf("missing execution owner service")
	}
	return a.svc.GetExecution(ctx, executionID)
}

func (a localExecutionOwner) UpdateExecutionState(ctx context.Context, req *execution.RuntimeUpdateExecutionStateReq) error {
	if a.svc == nil {
		return fmt.Errorf("missing execution owner service")
	}
	return a.svc.UpdateExecutionState(ctx, req)
}

// localInjectionOwner delegates to the injection.Writer port. The only
// translation is the method rename: injection.Writer.CreateInjectionRecord →
// InjectionOwner.CreateInjection. Orchestrator callers hold InjectionOwner
// (the interface), never the concrete *injection.Service.
type localInjectionOwner struct {
	svc injection.Writer
}

func (a localInjectionOwner) CreateInjection(ctx context.Context, req *injection.RuntimeCreateInjectionReq) (*dto.InjectionItem, error) {
	return a.svc.CreateInjectionRecord(ctx, req)
}

func (a localInjectionOwner) UpdateInjectionState(ctx context.Context, req *injection.RuntimeUpdateInjectionStateReq) error {
	return a.svc.UpdateInjectionState(ctx, req)
}

func (a localInjectionOwner) UpdateInjectionTimestamps(ctx context.Context, req *injection.RuntimeUpdateInjectionTimestampReq) (*dto.InjectionItem, error) {
	return a.svc.UpdateInjectionTimestamps(ctx, req)
}

// remoteExecutionOwner proxies to the api-gateway via the runtime intake
// gRPC (runtime-worker → api-gateway direction).
type remoteExecutionOwner struct {
	client *runtimeclient.Client
}

func (a remoteExecutionOwner) CreateExecution(ctx context.Context, req *execution.RuntimeCreateExecutionReq) (int, error) {
	if a.client == nil || !a.client.IntakeEnabled() {
		return 0, fmt.Errorf("runtime intake client is not configured")
	}
	return a.client.CreateExecution(ctx, req)
}

func (a remoteExecutionOwner) GetExecution(ctx context.Context, executionID int) (*execution.ExecutionDetailResp, error) {
	if a.client == nil || !a.client.IntakeEnabled() {
		return nil, fmt.Errorf("runtime intake client is not configured")
	}
	return a.client.GetExecution(ctx, executionID)
}

func (a remoteExecutionOwner) UpdateExecutionState(ctx context.Context, req *execution.RuntimeUpdateExecutionStateReq) error {
	if a.client == nil || !a.client.IntakeEnabled() {
		return fmt.Errorf("runtime intake client is not configured")
	}
	return a.client.UpdateExecutionState(ctx, req)
}

type remoteInjectionOwner struct {
	client *runtimeclient.Client
}

func (a remoteInjectionOwner) CreateInjection(ctx context.Context, req *injection.RuntimeCreateInjectionReq) (*dto.InjectionItem, error) {
	if a.client == nil || !a.client.IntakeEnabled() {
		return nil, fmt.Errorf("runtime intake client is not configured")
	}
	return a.client.CreateInjection(ctx, req)
}

func (a remoteInjectionOwner) UpdateInjectionState(ctx context.Context, req *injection.RuntimeUpdateInjectionStateReq) error {
	if a.client == nil || !a.client.IntakeEnabled() {
		return fmt.Errorf("runtime intake client is not configured")
	}
	return a.client.UpdateInjectionState(ctx, req)
}

func (a remoteInjectionOwner) UpdateInjectionTimestamps(ctx context.Context, req *injection.RuntimeUpdateInjectionTimestampReq) (*dto.InjectionItem, error) {
	if a.client == nil || !a.client.IntakeEnabled() {
		return nil, fmt.Errorf("runtime intake client is not configured")
	}
	return a.client.UpdateInjectionTimestamps(ctx, req)
}

// NewExecutionOwner is used by in-process runtimes (both / consumer / gateway-collocated)
// that wire execution.Service directly.
func NewExecutionOwner(svc *execution.Service) ExecutionOwner {
	return localExecutionOwner{svc: svc}
}

// NewInjectionOwner wires the injection.Writer port into the InjectionOwner
// adapter used by in-process runtimes (both / consumer / gateway-collocated).
// fx resolves injection.Writer from the injection module's AsWriter provider,
// so orchestrator never holds a direct reference to *injection.Service.
func NewInjectionOwner(svc injection.Writer) InjectionOwner {
	return localInjectionOwner{svc: svc}
}

// RemoteOwnerOptions forces the dedicated runtime-worker-service path to use
// the preserved runtime-intake gRPC seam (runtime-worker → api-gateway) for
// execution and injection state writes.
func RemoteOwnerOptions() fx.Option {
	return fx.Options(
		fx.Decorate(func(_ ExecutionOwner, client *runtimeclient.Client) ExecutionOwner {
			return remoteExecutionOwner{client: client}
		}),
		fx.Decorate(func(_ InjectionOwner, client *runtimeclient.Client) InjectionOwner {
			return remoteInjectionOwner{client: client}
		}),
	)
}
