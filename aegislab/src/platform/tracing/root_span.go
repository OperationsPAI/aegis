package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// NewRootSpanContext creates a sampled remote SpanContext for an orchestration
// trace. Workers and the reconciler reconstruct the trace's root SpanContext
// from the persisted (traceID, rootSpanID, flags) triple so every per-pickup
// stage span shares the same TraceID.
//
// The returned SpanContext is marked Remote so that tracer.Start with the
// resulting context produces a proper child (not a sibling) of the root.
func NewRootSpanContext(traceID, rootSpanID string, flags uint8) (trace.SpanContext, error) {
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		return trace.SpanContext{}, fmt.Errorf("invalid trace id %q: %w", traceID, err)
	}
	sid, err := trace.SpanIDFromHex(rootSpanID)
	if err != nil {
		return trace.SpanContext{}, fmt.Errorf("invalid span id %q: %w", rootSpanID, err)
	}

	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.TraceFlags(flags),
		Remote:     true,
	}), nil
}

// MintRootSpanContext starts a fresh root span, captures its SpanContext,
// and ends it immediately. The caller persists the captured IDs and
// reconstructs the SpanContext via NewRootSpanContext on every pickup.
//
// The actual long-lived root span is emitted at terminal-state time (see
// the lifecycle goroutine / reconciler fallback in core/orchestrator).
func MintRootSpanContext(ctx context.Context, name, aegisTraceID, traceType string) trace.SpanContext {
	_, span := otel.Tracer("rcabench/trace").Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("aegis.trace_id", aegisTraceID),
			attribute.String("aegis.trace_type", traceType),
		),
	)
	sc := span.SpanContext()
	span.End()
	return sc
}
