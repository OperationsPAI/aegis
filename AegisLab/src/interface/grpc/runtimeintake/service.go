package grpcruntimeintake

import (
	"context"
	"encoding/json"
	"fmt"

	"aegis/platform/dto"
	execution "aegis/module/execution"
	injection "aegis/module/injection"
	runtimev1 "aegis/platform/proto/runtime/v1"

	"google.golang.org/protobuf/types/known/structpb"
)

// executionOwner is the local api-gateway-side execution module surface
// needed to serve intake RPCs originating from runtime-worker.
type executionOwner interface {
	CreateExecutionRecord(context.Context, *execution.RuntimeCreateExecutionReq) (int, error)
	UpdateExecutionState(context.Context, *execution.RuntimeUpdateExecutionStateReq) error
	GetExecution(context.Context, int) (*execution.ExecutionDetailResp, error)
}

// injectionOwner is the local api-gateway-side injection module surface.
type injectionOwner interface {
	CreateInjectionRecord(context.Context, *injection.RuntimeCreateInjectionReq) (*dto.InjectionItem, error)
	UpdateInjectionState(context.Context, *injection.RuntimeUpdateInjectionStateReq) error
	UpdateInjectionTimestamps(context.Context, *injection.RuntimeUpdateInjectionTimestampReq) (*dto.InjectionItem, error)
}

type intakeServer struct {
	execution executionOwner
	injection injectionOwner
}

func newIntakeServer(executionSvc *execution.Service, injectionSvc *injection.Service) *intakeServer {
	return &intakeServer{
		execution: executionSvc,
		injection: injectionSvc,
	}
}

func (s *intakeServer) CreateExecution(ctx context.Context, req *runtimev1.StructResponse) (*runtimev1.StructResponse, error) {
	body, err := decodeBody[execution.RuntimeCreateExecutionReq](req.GetData())
	if err != nil {
		return nil, err
	}
	executionID, err := s.execution.CreateExecutionRecord(ctx, body)
	if err != nil {
		return nil, err
	}
	return encodeBody(map[string]any{"execution_id": executionID})
}

func (s *intakeServer) GetExecution(ctx context.Context, req *runtimev1.StructResponse) (*runtimev1.StructResponse, error) {
	body, err := decodeBody[map[string]any](req.GetData())
	if err != nil {
		return nil, err
	}
	idValue, ok := (*body)["execution_id"].(float64)
	if !ok {
		return nil, fmt.Errorf("intake GetExecution: missing execution_id")
	}
	resp, err := s.execution.GetExecution(ctx, int(idValue))
	if err != nil {
		return nil, err
	}
	return encodeBody(resp)
}

func (s *intakeServer) UpdateExecutionState(ctx context.Context, req *runtimev1.StructResponse) (*runtimev1.StructResponse, error) {
	body, err := decodeBody[execution.RuntimeUpdateExecutionStateReq](req.GetData())
	if err != nil {
		return nil, err
	}
	if err := s.execution.UpdateExecutionState(ctx, body); err != nil {
		return nil, err
	}
	return encodeBody(map[string]any{"ok": true})
}

func (s *intakeServer) CreateInjection(ctx context.Context, req *runtimev1.StructResponse) (*runtimev1.StructResponse, error) {
	body, err := decodeBody[injection.RuntimeCreateInjectionReq](req.GetData())
	if err != nil {
		return nil, err
	}
	resp, err := s.injection.CreateInjectionRecord(ctx, body)
	if err != nil {
		return nil, err
	}
	return encodeBody(resp)
}

func (s *intakeServer) UpdateInjectionState(ctx context.Context, req *runtimev1.StructResponse) (*runtimev1.StructResponse, error) {
	body, err := decodeBody[injection.RuntimeUpdateInjectionStateReq](req.GetData())
	if err != nil {
		return nil, err
	}
	if err := s.injection.UpdateInjectionState(ctx, body); err != nil {
		return nil, err
	}
	return encodeBody(map[string]any{"ok": true})
}

func (s *intakeServer) UpdateInjectionTimestamps(ctx context.Context, req *runtimev1.StructResponse) (*runtimev1.StructResponse, error) {
	body, err := decodeBody[injection.RuntimeUpdateInjectionTimestampReq](req.GetData())
	if err != nil {
		return nil, err
	}
	resp, err := s.injection.UpdateInjectionTimestamps(ctx, body)
	if err != nil {
		return nil, err
	}
	return encodeBody(resp)
}

func decodeBody[T any](payload *structpb.Struct) (*T, error) {
	if payload == nil {
		return nil, fmt.Errorf("intake payload is nil")
	}
	data, err := json.Marshal(payload.AsMap())
	if err != nil {
		return nil, err
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func encodeBody(value any) (*runtimev1.StructResponse, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	body, err := structpb.NewStruct(payload)
	if err != nil {
		return nil, err
	}
	return &runtimev1.StructResponse{Data: body}, nil
}
