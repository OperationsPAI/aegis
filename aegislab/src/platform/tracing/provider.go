package tracing

import (
	"context"
	"time"

	"aegis/platform/config"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
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

	// Pass the empty SchemaURL so Merge() doesn't reject our overrides
	// when `resource.Default()` ships a newer schema URL than the version
	// of semconv we import.
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			"",
			semconv.ServiceName(config.GetString("name")),
			semconv.ServiceVersion(config.GetString("version")),
		),
	)
	if err != nil {
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
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
