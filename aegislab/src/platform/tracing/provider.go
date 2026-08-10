package tracing

import (
	"context"
	"time"

	"aegis/platform/config"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func NewProvider() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	endpoint := config.GetString("tracing.endpoint")
	logrus.Infof("tracing: initializing OTLP HTTP exporter -> %s", endpoint)

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithEndpoint(endpoint),
	)
	if err != nil {
		return nil, err
	}

	res, err := rcabenchResource()
	if err != nil {
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			// 5s batch timeout so backdated terminal-state emits (root
			// span ending at trace.EndTime, K8s dispatch_wait spans
			// ending hours after creationTimestamp) reliably flush
			// without piling up under spiky load.
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logrus.Warnf("otel error: %v", err)
	}))
	logrus.Info("tracing: TracerProvider installed as otel global")
	return provider, nil
}

func ShutdownProvider(ctx context.Context, provider *sdktrace.TracerProvider) {
	if provider == nil {
		return
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := provider.Shutdown(shutdownCtx); err != nil {
		logrus.Errorf("failed to shutdown tracer provider: %v", err)
	}
}
