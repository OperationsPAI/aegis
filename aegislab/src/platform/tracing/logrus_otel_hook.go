package tracing

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
)

// OtelLogrusHook mirrors every logrus entry into an OTel log.Record so the
// configured LoggerProvider (see NewLogProvider) can ship aegislab's own
// runtime logs through the same OTLP pipeline that already carries traces
// and metrics. The hook does not replace stdout output — logrus' default
// formatter still runs first; this purely tees a copy into OTel.
type OtelLogrusHook struct {
	logger log.Logger
}

// NewOtelLogrusHook resolves the global LoggerProvider at construction
// time. Callers must register it AFTER NewLogProvider has run so the
// returned hook bridges into the configured exporter rather than the
// pre-init no-op.
func NewOtelLogrusHook() *OtelLogrusHook {
	return newHookWithLogger(global.GetLoggerProvider().Logger("aegis/logrus-bridge"))
}

// newHookWithLogger is the test-friendly seam used by logrus_otel_hook_test
// to inject a logtest recorder Logger. Not exported because production code
// has no reason to build a hook against anything other than the global
// LoggerProvider.
func newHookWithLogger(logger log.Logger) *OtelLogrusHook {
	return &OtelLogrusHook{logger: logger}
}

func (h *OtelLogrusHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *OtelLogrusHook) Fire(e *logrus.Entry) error {
	if h == nil || h.logger == nil {
		return nil
	}

	var rec log.Record
	rec.SetTimestamp(e.Time)
	rec.SetSeverity(toOTelSeverity(e.Level))
	rec.SetSeverityText(e.Level.String())
	rec.SetBody(log.StringValue(e.Message))

	// Forward every logrus field as a string-valued attribute. Stringifying
	// keeps the attribute schema uniform across heterogeneous field types
	// (errors, structs, time.Time) without inventing a logrus-aware codec
	// — the receiving ClickHouse column is map<string,string> anyway.
	if len(e.Data) > 0 {
		attrs := make([]log.KeyValue, 0, len(e.Data))
		for k, v := range e.Data {
			attrs = append(attrs, log.String(k, fmt.Sprintf("%v", v)))
		}
		rec.AddAttributes(attrs...)
	}

	// Pass the entry's context so the SDK can attach TraceID/SpanID from
	// any active OTel span. When the caller didn't use .WithContext(ctx)
	// the bridge falls through to Background, which the SDK treats as
	// "no span" and emits the record uncorrelated — same fallback as
	// logrus' own default.
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	h.logger.Emit(ctx, rec)
	return nil
}

// toOTelSeverity maps logrus severity onto the OpenTelemetry severity
// number range. The choices match the OTel logs data model recommendation
// (one severity per logrus level; trace = SeverityTrace1, …).
func toOTelSeverity(lvl logrus.Level) log.Severity {
	switch lvl {
	case logrus.PanicLevel:
		return log.SeverityFatal2
	case logrus.FatalLevel:
		return log.SeverityFatal1
	case logrus.ErrorLevel:
		return log.SeverityError1
	case logrus.WarnLevel:
		return log.SeverityWarn1
	case logrus.InfoLevel:
		return log.SeverityInfo1
	case logrus.DebugLevel:
		return log.SeverityDebug1
	case logrus.TraceLevel:
		return log.SeverityTrace1
	default:
		return log.SeverityUndefined
	}
}
