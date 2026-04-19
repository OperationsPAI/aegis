package grpcruntimeintake

import (
	"context"
	"fmt"
	"net"

	"aegis/config"
	"aegis/httpx"
	runtimev1 "aegis/proto/runtime/v1"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

const defaultIntakeGRPCAddr = ":9096"

type Lifecycle struct {
	server   *grpc.Server
	addr     string
	listener net.Listener

	// StartFunc / StopFunc are test hooks.
	StartFunc func(context.Context) error
	StopFunc  func()
}

func newLifecycle(intake *intakeServer) (*Lifecycle, error) {
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(httpx.UnaryServerRequestIDInterceptor()))
	runtimev1.RegisterRuntimeIntakeServiceServer(grpcServer, intake)

	healthServer := health.NewServer()
	healthServer.SetServingStatus(runtimev1.RuntimeIntakeService_FullName, grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	if config.GetBool("api_gateway.intake.grpc.reflection") {
		reflection.Register(grpcServer)
	}

	addr := config.GetString("api_gateway.intake.grpc.addr")
	if addr == "" {
		addr = config.GetString("runtime_intake.grpc.addr")
	}
	if addr == "" {
		addr = defaultIntakeGRPCAddr
	}

	return &Lifecycle{
		server: grpcServer,
		addr:   addr,
	}, nil
}

func (r *Lifecycle) start(ctx context.Context) error {
	if r.StartFunc != nil {
		return r.StartFunc(ctx)
	}

	listener, err := net.Listen("tcp", r.addr)
	if err != nil {
		return fmt.Errorf("listen runtime intake grpc on %s: %w", r.addr, err)
	}
	r.listener = listener

	go func() {
		logrus.Infof("Starting runtime intake gRPC server on %s", r.addr)
		if err := r.server.Serve(listener); err != nil {
			logrus.Errorf("runtime intake gRPC server error: %v", err)
		}
	}()
	return nil
}

func (r *Lifecycle) stop() {
	if r.StopFunc != nil {
		r.StopFunc()
		return
	}
	if r.server != nil {
		r.server.GracefulStop()
	}
}

func registerLifecycle(lc fx.Lifecycle, runner *Lifecycle) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return runner.start(ctx)
		},
		OnStop: func(ctx context.Context) error {
			runner.stop()
			return nil
		},
	})
}
