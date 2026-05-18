package tracing

import (
	"context"
	"time"

	"aegis/platform/config"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/fx"
)

// NewLogProvider wires an OTLP HTTP log exporter to the same collector
// the trace provider targets. The returned LoggerProvider is also published
// as the OTel global so packages that pull a logger via
// global.GetLoggerProvider().Logger(...) (notably the logrus → OTel hook
// below) hit the configured pipeline instead of the SDK no-op.
//
// When tracing.endpoint is unset (tests / offline boot) we still return a
// non-nil no-op LoggerProvider so fx wiring stays uniform; nothing exports
// in that mode.
func NewLogProvider(lc fx.Lifecycle) (*sdklog.LoggerProvider, error) {
	endpoint := config.GetString("tracing.endpoint")
	if endpoint == "" {
		lp := sdklog.NewLoggerProvider()
		global.SetLoggerProvider(lp)
		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				return lp.Shutdown(ctx)
			},
		})
		return lp, nil
	}

	logrus.Infof("tracing: initializing OTLP HTTP log exporter -> %s", endpoint)

	exporter, err := otlploghttp.New(context.Background(),
		otlploghttp.WithEndpoint(endpoint),
		otlploghttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res, err := rcabenchResource()
	if err != nil {
		return nil, err
	}

	provider := sdklog.NewLoggerProvider(
		// 5s export interval mirrors the trace batch timeout so logs and
		// spans land in ClickHouse on the same cadence — useful when a
		// late-flushed span and its correlated log entry would otherwise
		// show up minutes apart in the UI.
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter,
			sdklog.WithExportInterval(5*time.Second),
		)),
		sdklog.WithResource(res),
	)
	global.SetLoggerProvider(provider)

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return provider.Shutdown(shutdownCtx)
		},
	})

	logrus.Info("tracing: LoggerProvider installed as otel global")
	return provider, nil
}
