package runtimeclient

import (
	"context"
	"encoding/json"
	"fmt"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/httpx"
	execution "aegis/core/domain/execution"
	injection "aegis/core/domain/injection"
	systemmetric "aegis/crud/observability/systemmetric"
	task "aegis/core/domain/task"
	runtimev1 "aegis/platform/proto/runtime/v1"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// Client is the bi-directional runtime client used by the api-gateway
// (querying runtime-worker for queue/status) and by the runtime-worker
// (writing execution/injection state back into api-gateway).
//
// Both peers share one client type because the seam is symmetric — the
// gRPC ServiceDescs (RuntimeService + RuntimeIntakeService) are both
// registered against whichever peer serves the given direction.
type Client struct {
	queryTarget  string
	intakeTarget string

	queryConn  *grpc.ClientConn
	intakeConn *grpc.ClientConn

	query  runtimev1.RuntimeServiceClient
	intake runtimev1.RuntimeIntakeServiceClient
}

// NewClient constructs a Client. Both endpoints are optional; each
// capability (query vs intake) reports Enabled() independently.
func NewClient(lc fx.Lifecycle) (*Client, error) {
	client := &Client{
		queryTarget:  resolveQueryTarget(),
		intakeTarget: resolveIntakeTarget(),
	}

	if client.queryTarget != "" {
		conn, err := grpc.NewClient(
			client.queryTarget,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(httpx.UnaryClientRequestIDInterceptor()),
		)
		if err != nil {
			return nil, fmt.Errorf("create runtime query grpc client: %w", err)
		}
		client.queryConn = conn
		client.query = runtimev1.NewRuntimeServiceClient(conn)
	}

	if client.intakeTarget != "" {
		conn, err := grpc.NewClient(
			client.intakeTarget,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(httpx.UnaryClientRequestIDInterceptor()),
		)
		if err != nil {
			return nil, fmt.Errorf("create runtime intake grpc client: %w", err)
		}
		client.intakeConn = conn
		client.intake = runtimev1.NewRuntimeIntakeServiceClient(conn)
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			var firstErr error
			if client.queryConn != nil {
				if err := client.queryConn.Close(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			if client.intakeConn != nil {
				if err := client.intakeConn.Close(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
	})

	return client, nil
}

func resolveQueryTarget() string {
	target := config.GetString("clients.runtime.target")
	if target == "" {
		target = config.GetString("runtime_worker.grpc.target")
	}
	return target
}

func resolveIntakeTarget() string {
	target := config.GetString("clients.runtime_intake.target")
	if target == "" {
		target = config.GetString("runtime_intake.grpc.target")
	}
	return target
}

// Enabled reports whether the api-gateway→runtime-worker query channel
// is configured. Kept for callers (systemmetric) that only want the
// query capability.
func (c *Client) Enabled() bool {
	return c != nil && c.query != nil
}

// IntakeEnabled reports whether the runtime-worker→api-gateway intake
// channel is configured.
func (c *Client) IntakeEnabled() bool {
	return c != nil && c.intake != nil
}

// --- query direction: api-gateway -> runtime-worker ---

func (c *Client) GetNamespaceLocks(ctx context.Context) (*systemmetric.ListNamespaceLockResp, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("runtime grpc client is not configured")
	}
	resp, err := c.query.GetNamespaceLocks(ctx, &runtimev1.PingRequest{})
	if err != nil {
		return nil, mapRPCError(err)
	}
	return decodeStruct[systemmetric.ListNamespaceLockResp](resp.GetData())
}

func (c *Client) GetQueuedTasks(ctx context.Context) (*task.QueuedTasksResp, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("runtime grpc client is not configured")
	}
	resp, err := c.query.GetQueuedTasks(ctx, &runtimev1.PingRequest{})
	if err != nil {
		return nil, mapRPCError(err)
	}
	return decodeStruct[task.QueuedTasksResp](resp.GetData())
}

// --- intake direction: runtime-worker -> api-gateway ---

func (c *Client) CreateExecution(ctx context.Context, req *execution.RuntimeCreateExecutionReq) (int, error) {
	if !c.IntakeEnabled() {
		return 0, fmt.Errorf("runtime intake grpc client is not configured")
	}
	body, err := toStruct(req)
	if err != nil {
		return 0, fmt.Errorf("encode create execution request: %w", err)
	}
	resp, err := c.intake.CreateExecution(ctx, &runtimev1.StructResponse{Data: body})
	if err != nil {
		return 0, mapRPCError(err)
	}
	data := resp.GetData().AsMap()
	executionID, ok := data["execution_id"].(float64)
	if !ok {
		return 0, fmt.Errorf("runtime intake payload missing execution_id")
	}
	return int(executionID), nil
}

func (c *Client) CreateInjection(ctx context.Context, req *injection.RuntimeCreateInjectionReq) (*dto.InjectionItem, error) {
	if !c.IntakeEnabled() {
		return nil, fmt.Errorf("runtime intake grpc client is not configured")
	}
	body, err := toStruct(req)
	if err != nil {
		return nil, fmt.Errorf("encode create injection request: %w", err)
	}
	resp, err := c.intake.CreateInjection(ctx, &runtimev1.StructResponse{Data: body})
	if err != nil {
		return nil, mapRPCError(err)
	}
	return decodeStruct[dto.InjectionItem](resp.GetData())
}

func (c *Client) UpdateExecutionState(ctx context.Context, req *execution.RuntimeUpdateExecutionStateReq) error {
	if !c.IntakeEnabled() {
		return fmt.Errorf("runtime intake grpc client is not configured")
	}
	body, err := toStruct(req)
	if err != nil {
		return fmt.Errorf("encode update execution state request: %w", err)
	}
	if _, err := c.intake.UpdateExecutionState(ctx, &runtimev1.StructResponse{Data: body}); err != nil {
		return mapRPCError(err)
	}
	return nil
}

func (c *Client) UpdateInjectionState(ctx context.Context, req *injection.RuntimeUpdateInjectionStateReq) error {
	if !c.IntakeEnabled() {
		return fmt.Errorf("runtime intake grpc client is not configured")
	}
	body, err := toStruct(req)
	if err != nil {
		return fmt.Errorf("encode update injection state request: %w", err)
	}
	if _, err := c.intake.UpdateInjectionState(ctx, &runtimev1.StructResponse{Data: body}); err != nil {
		return mapRPCError(err)
	}
	return nil
}

func (c *Client) UpdateInjectionTimestamps(ctx context.Context, req *injection.RuntimeUpdateInjectionTimestampReq) (*dto.InjectionItem, error) {
	if !c.IntakeEnabled() {
		return nil, fmt.Errorf("runtime intake grpc client is not configured")
	}
	body, err := toStruct(req)
	if err != nil {
		return nil, fmt.Errorf("encode update injection timestamps request: %w", err)
	}
	resp, err := c.intake.UpdateInjectionTimestamps(ctx, &runtimev1.StructResponse{Data: body})
	if err != nil {
		return nil, mapRPCError(err)
	}
	return decodeStruct[dto.InjectionItem](resp.GetData())
}

func (c *Client) GetExecution(ctx context.Context, executionID int) (*execution.ExecutionDetailResp, error) {
	if !c.IntakeEnabled() {
		return nil, fmt.Errorf("runtime intake grpc client is not configured")
	}
	body, err := toStruct(map[string]any{"execution_id": executionID})
	if err != nil {
		return nil, fmt.Errorf("encode get execution request: %w", err)
	}
	resp, err := c.intake.GetExecution(ctx, &runtimev1.StructResponse{Data: body})
	if err != nil {
		return nil, mapRPCError(err)
	}
	return decodeStruct[execution.ExecutionDetailResp](resp.GetData())
}

// --- encoding helpers ---

func toStruct(value any) (*structpb.Struct, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return structpb.NewStruct(payload)
}

func decodeStruct[T any](payload *structpb.Struct) (*T, error) {
	if payload == nil {
		return nil, fmt.Errorf("runtime payload is nil")
	}
	data, err := json.Marshal(payload.AsMap())
	if err != nil {
		return nil, err
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func mapRPCError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.Unauthenticated:
		return fmt.Errorf("%w: %s", consts.ErrAuthenticationFailed, st.Message())
	case codes.PermissionDenied:
		return fmt.Errorf("%w: %s", consts.ErrPermissionDenied, st.Message())
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", consts.ErrBadRequest, st.Message())
	case codes.NotFound:
		return fmt.Errorf("%w: %s", consts.ErrNotFound, st.Message())
	case codes.AlreadyExists:
		return fmt.Errorf("%w: %s", consts.ErrAlreadyExists, st.Message())
	default:
		return fmt.Errorf("runtime rpc failed: %w", err)
	}
}
