package runtimev1

import (
	"context"

	"google.golang.org/grpc"
)

// RuntimeIntakeService gRPC service names. Declared here (rather than in the
// proto file + `protoc` output) so that extending the runtime seam doesn't
// require rebuilding generated code — the 5 intake methods all carry a
// Struct payload, same wire shape as GetNamespaceLocks.
//
// The direction is runtime-worker (client) → api-gateway (server).
const (
	RuntimeIntakeService_FullName = "runtime.v1.RuntimeIntakeService"

	RuntimeIntakeService_CreateExecution_FullMethodName          = "/runtime.v1.RuntimeIntakeService/CreateExecution"
	RuntimeIntakeService_GetExecution_FullMethodName             = "/runtime.v1.RuntimeIntakeService/GetExecution"
	RuntimeIntakeService_UpdateExecutionState_FullMethodName     = "/runtime.v1.RuntimeIntakeService/UpdateExecutionState"
	RuntimeIntakeService_CreateInjection_FullMethodName          = "/runtime.v1.RuntimeIntakeService/CreateInjection"
	RuntimeIntakeService_UpdateInjectionState_FullMethodName     = "/runtime.v1.RuntimeIntakeService/UpdateInjectionState"
	RuntimeIntakeService_UpdateInjectionTimestamps_FullMethodName = "/runtime.v1.RuntimeIntakeService/UpdateInjectionTimestamps"
)

// RuntimeIntakeServiceClient is the client API for RuntimeIntakeService.
type RuntimeIntakeServiceClient interface {
	CreateExecution(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error)
	GetExecution(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error)
	UpdateExecutionState(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error)
	CreateInjection(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error)
	UpdateInjectionState(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error)
	UpdateInjectionTimestamps(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error)
}

type runtimeIntakeServiceClient struct {
	cc grpc.ClientConnInterface
}

// NewRuntimeIntakeServiceClient constructs a client bound to the provided connection.
func NewRuntimeIntakeServiceClient(cc grpc.ClientConnInterface) RuntimeIntakeServiceClient {
	return &runtimeIntakeServiceClient{cc: cc}
}

func (c *runtimeIntakeServiceClient) invoke(ctx context.Context, method string, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error) {
	out := new(StructResponse)
	if err := c.cc.Invoke(ctx, method, in, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *runtimeIntakeServiceClient) CreateExecution(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error) {
	return c.invoke(ctx, RuntimeIntakeService_CreateExecution_FullMethodName, in, opts...)
}

func (c *runtimeIntakeServiceClient) GetExecution(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error) {
	return c.invoke(ctx, RuntimeIntakeService_GetExecution_FullMethodName, in, opts...)
}

func (c *runtimeIntakeServiceClient) UpdateExecutionState(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error) {
	return c.invoke(ctx, RuntimeIntakeService_UpdateExecutionState_FullMethodName, in, opts...)
}

func (c *runtimeIntakeServiceClient) CreateInjection(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error) {
	return c.invoke(ctx, RuntimeIntakeService_CreateInjection_FullMethodName, in, opts...)
}

func (c *runtimeIntakeServiceClient) UpdateInjectionState(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error) {
	return c.invoke(ctx, RuntimeIntakeService_UpdateInjectionState_FullMethodName, in, opts...)
}

func (c *runtimeIntakeServiceClient) UpdateInjectionTimestamps(ctx context.Context, in *StructResponse, opts ...grpc.CallOption) (*StructResponse, error) {
	return c.invoke(ctx, RuntimeIntakeService_UpdateInjectionTimestamps_FullMethodName, in, opts...)
}

// RuntimeIntakeServiceServer is the server API for RuntimeIntakeService.
type RuntimeIntakeServiceServer interface {
	CreateExecution(context.Context, *StructResponse) (*StructResponse, error)
	GetExecution(context.Context, *StructResponse) (*StructResponse, error)
	UpdateExecutionState(context.Context, *StructResponse) (*StructResponse, error)
	CreateInjection(context.Context, *StructResponse) (*StructResponse, error)
	UpdateInjectionState(context.Context, *StructResponse) (*StructResponse, error)
	UpdateInjectionTimestamps(context.Context, *StructResponse) (*StructResponse, error)
}

// RegisterRuntimeIntakeServiceServer registers the intake service implementation.
func RegisterRuntimeIntakeServiceServer(s grpc.ServiceRegistrar, srv RuntimeIntakeServiceServer) {
	s.RegisterService(&RuntimeIntakeService_ServiceDesc, srv)
}

func makeIntakeHandler(method func(RuntimeIntakeServiceServer, context.Context, *StructResponse) (*StructResponse, error), fullMethod string) func(interface{}, context.Context, func(interface{}) error, grpc.UnaryServerInterceptor) (interface{}, error) {
	return func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
		in := new(StructResponse)
		if err := dec(in); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return method(srv.(RuntimeIntakeServiceServer), ctx, in)
		}
		info := &grpc.UnaryServerInfo{
			Server:     srv,
			FullMethod: fullMethod,
		}
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			return method(srv.(RuntimeIntakeServiceServer), ctx, req.(*StructResponse))
		}
		return interceptor(ctx, in, info, handler)
	}
}

var _RuntimeIntakeService_CreateExecution_Handler = makeIntakeHandler(
	func(s RuntimeIntakeServiceServer, ctx context.Context, in *StructResponse) (*StructResponse, error) {
		return s.CreateExecution(ctx, in)
	},
	RuntimeIntakeService_CreateExecution_FullMethodName,
)

var _RuntimeIntakeService_GetExecution_Handler = makeIntakeHandler(
	func(s RuntimeIntakeServiceServer, ctx context.Context, in *StructResponse) (*StructResponse, error) {
		return s.GetExecution(ctx, in)
	},
	RuntimeIntakeService_GetExecution_FullMethodName,
)

var _RuntimeIntakeService_UpdateExecutionState_Handler = makeIntakeHandler(
	func(s RuntimeIntakeServiceServer, ctx context.Context, in *StructResponse) (*StructResponse, error) {
		return s.UpdateExecutionState(ctx, in)
	},
	RuntimeIntakeService_UpdateExecutionState_FullMethodName,
)

var _RuntimeIntakeService_CreateInjection_Handler = makeIntakeHandler(
	func(s RuntimeIntakeServiceServer, ctx context.Context, in *StructResponse) (*StructResponse, error) {
		return s.CreateInjection(ctx, in)
	},
	RuntimeIntakeService_CreateInjection_FullMethodName,
)

var _RuntimeIntakeService_UpdateInjectionState_Handler = makeIntakeHandler(
	func(s RuntimeIntakeServiceServer, ctx context.Context, in *StructResponse) (*StructResponse, error) {
		return s.UpdateInjectionState(ctx, in)
	},
	RuntimeIntakeService_UpdateInjectionState_FullMethodName,
)

var _RuntimeIntakeService_UpdateInjectionTimestamps_Handler = makeIntakeHandler(
	func(s RuntimeIntakeServiceServer, ctx context.Context, in *StructResponse) (*StructResponse, error) {
		return s.UpdateInjectionTimestamps(ctx, in)
	},
	RuntimeIntakeService_UpdateInjectionTimestamps_FullMethodName,
)

// RuntimeIntakeService_ServiceDesc is the grpc.ServiceDesc for RuntimeIntakeService service.
var RuntimeIntakeService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "runtime.v1.RuntimeIntakeService",
	HandlerType: (*RuntimeIntakeServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "CreateExecution", Handler: _RuntimeIntakeService_CreateExecution_Handler},
		{MethodName: "GetExecution", Handler: _RuntimeIntakeService_GetExecution_Handler},
		{MethodName: "UpdateExecutionState", Handler: _RuntimeIntakeService_UpdateExecutionState_Handler},
		{MethodName: "CreateInjection", Handler: _RuntimeIntakeService_CreateInjection_Handler},
		{MethodName: "UpdateInjectionState", Handler: _RuntimeIntakeService_UpdateInjectionState_Handler},
		{MethodName: "UpdateInjectionTimestamps", Handler: _RuntimeIntakeService_UpdateInjectionTimestamps_Handler},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/runtime/v1/runtime.proto",
}
