package tracing

import (
	"context"

	"github.com/sirupsen/logrus"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/fx"
)

var Module = fx.Module("tracing",
	fx.Provide(NewTraceProvider),
	fx.Provide(NewLogProvider),
	// Force instantiation of both providers + the logrus → OTel bridge.
	// Without an explicit invoke nothing depends on these singletons and
	// fx skips construction entirely, leaving the OTel globals as the SDK
	// no-op (silently drops every span / log).
	fx.Invoke(func(*trace.TracerProvider, *sdklog.LoggerProvider) {
		// Install the logrus hook only after NewLogProvider has run so
		// NewOtelLogrusHook resolves the configured global, not the
		// pre-init no-op.
		logrus.AddHook(NewOtelLogrusHook())
	}),
)

func NewTraceProvider(lc fx.Lifecycle) *trace.TracerProvider {
	provider, err := NewProvider()
	if err != nil {
		panic(err)
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			ShutdownProvider(ctx, provider)
			return nil
		},
	})

	return provider
}
