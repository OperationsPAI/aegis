package dto

import (
	"context"
	"encoding/json"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/tracing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestUnifiedTask_RootTraceCarrierMarshalRoundTrip(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})

	const (
		traceHex = "0102030405060708090a0b0c0d0e0f10"
		spanHex  = "1112131415161718"
	)
	sc, err := tracing.NewRootSpanContext(traceHex, spanHex, 1)
	require.NoError(t, err)

	rootCtx := oteltrace.ContextWithRemoteSpanContext(context.Background(), sc)

	task := &UnifiedTask{
		TaskID:     "tid",
		TraceID:    "trace-uuid",
		Type:       consts.TaskTypeRestartPedestal,
		EnqueuedAt: 1700000000_000000000,
	}
	task.SetRootTraceCtx(rootCtx)

	require.NotNil(t, task.RootTraceCarrier)
	require.NotEmpty(t, task.RootTraceCarrier)

	blob, err := json.Marshal(task)
	require.NoError(t, err)

	var decoded UnifiedTask
	require.NoError(t, json.Unmarshal(blob, &decoded))

	assert.Equal(t, task.EnqueuedAt, decoded.EnqueuedAt)
	assert.Equal(t, task.RootTraceCarrier, decoded.RootTraceCarrier)

	roundCtx := decoded.GetRootTraceCtx(context.Background())
	roundSC := oteltrace.SpanContextFromContext(roundCtx)
	assert.True(t, roundSC.IsValid())
	assert.Equal(t, traceHex, roundSC.TraceID().String())
	assert.Equal(t, spanHex, roundSC.SpanID().String())
}

func TestUnifiedTask_GetAnnotationsIncludesRootCarrier(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})

	sc, err := tracing.NewRootSpanContext(
		"0102030405060708090a0b0c0d0e0f10",
		"1112131415161718",
		1,
	)
	require.NoError(t, err)

	task := &UnifiedTask{TaskID: "tid", TraceID: "trace-uuid"}
	task.SetRootTraceCtx(oteltrace.ContextWithRemoteSpanContext(context.Background(), sc))

	ann, err := task.GetAnnotations(context.Background())
	require.NoError(t, err)

	_, ok := ann[consts.RootTraceCarrier]
	assert.True(t, ok, "annotation %q must be present when RootTraceCarrier is set", consts.RootTraceCarrier)
}

func TestUnifiedTask_GetAnnotationsOmitsRootCarrierWhenAbsent(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})

	task := &UnifiedTask{TaskID: "tid", TraceID: "trace-uuid"}
	ann, err := task.GetAnnotations(context.Background())
	require.NoError(t, err)

	_, ok := ann[consts.RootTraceCarrier]
	assert.False(t, ok)
}
