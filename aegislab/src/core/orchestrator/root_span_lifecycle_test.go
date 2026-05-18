package consumer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"aegis/platform/consts"
	"aegis/platform/model"
)

func newInMemoryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Trace{}, &model.Task{}))
	return db
}

func installInMemoryTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})
	return exp
}

func seedTrace(t *testing.T, db *gorm.DB, id, traceHex, spanHex string, endTime *time.Time) {
	t.Helper()
	tr := &model.Trace{
		ID:              id,
		Type:            consts.TraceTypeFaultInjection,
		StartTime:       time.Now().Add(-10 * time.Minute),
		EndTime:         endTime,
		LeafNum:         1,
		State:           consts.TraceCompleted,
		Status:          consts.CommonEnabled,
		OTelTraceID:     traceHex,
		OTelRootSpanID:  spanHex,
		OTelFlags:       1,
		OTelRootEmitted: false,
	}
	require.NoError(t, db.Create(tr).Error)
}

const (
	tHex = "0102030405060708090a0b0c0d0e0f10"
	sHex = "1112131415161718"
)

// Scenario A: CAS-winner emit — Spawn + Shutdown path closes the span and
// flips OTelRootEmitted to true.
func TestRootLifecycle_CASWinnerEmitsSpan(t *testing.T) {
	exp := installInMemoryTracer(t)
	db := newInMemoryDB(t)
	seedTrace(t, db, "trace-A", tHex, sHex, nil)

	m := NewRootSpanLifecycleManager(db, nil)
	m.Spawn("trace-A", "FaultInjection", tHex, sHex, 1, time.Now().Add(-5*time.Minute))

	// Allow watcher goroutine to subscribe.
	time.Sleep(50 * time.Millisecond)
	m.Shutdown(context.Background())

	// Wait for the watcher goroutine to drain.
	require.Eventually(t, func() bool {
		var row model.Trace
		require.NoError(t, db.Where("id = ?", "trace-A").First(&row).Error)
		return row.OTelRootEmitted
	}, 2*time.Second, 10*time.Millisecond)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "trace.root/FaultInjection", spans[0].Name)
	assert.Equal(t, tHex, spans[0].SpanContext.TraceID().String())
}

// Scenario B: CAS-loser discards — if the DB already shows
// OTelRootEmitted=true (reconciler beat the goroutine), the span MUST NOT
// be exported.
func TestRootLifecycle_CASLoserDiscardsSpan(t *testing.T) {
	exp := installInMemoryTracer(t)
	db := newInMemoryDB(t)
	seedTrace(t, db, "trace-B", tHex, sHex, nil)
	require.NoError(t, db.Model(&model.Trace{}).
		Where("id = ?", "trace-B").
		Update("o_tel_root_emitted", true).Error)

	m := NewRootSpanLifecycleManager(db, nil)
	m.Spawn("trace-B", "FaultInjection", tHex, sHex, 1, time.Now().Add(-5*time.Minute))
	time.Sleep(50 * time.Millisecond)
	m.Shutdown(context.Background())

	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, exp.GetSpans(), "loser of CAS must not emit a span")
}

// Scenario C: Safety-net timeout — manually trip endIfWinner with the
// timeout code path. The full 6h timer can't be exercised in a unit test;
// instead we assert endIfWinner with codes.Error sets the right status and
// emits exactly one span.
func TestRootLifecycle_SafetyNetTimeoutEmitsErrorSpan(t *testing.T) {
	exp := installInMemoryTracer(t)
	db := newInMemoryDB(t)
	seedTrace(t, db, "trace-C", tHex, sHex, nil)

	m := NewRootSpanLifecycleManager(db, nil)
	m.Spawn("trace-C", "FaultInjection", tHex, sHex, 1, time.Now().Add(-5*time.Minute))
	time.Sleep(50 * time.Millisecond)

	// Reach into the registry to trip the timeout path directly.
	m.mu.Lock()
	h := m.inflight["trace-C"]
	m.mu.Unlock()
	require.NotNil(t, h)

	m.endIfWinner(h, /* codes.Error */ 1, "trace timeout — root closed by safety net", time.Now())

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "trace.root/FaultInjection", spans[0].Name)
	// Status code 1 == Error.
	assert.Equal(t, "trace timeout — root closed by safety net", spans[0].Status.Description)

	var row model.Trace
	require.NoError(t, db.Where("id = ?", "trace-C").First(&row).Error)
	assert.True(t, row.OTelRootEmitted)
}

// Scenario D: Reconciler synthesizes a backdated root span for traces whose
// end_time landed but whose goroutine never closed the span (worker crash).
func TestRootLifecycle_ReconcilerSynthesizesOrphanedRoot(t *testing.T) {
	exp := installInMemoryTracer(t)
	db := newInMemoryDB(t)
	endTime := time.Now()
	seedTrace(t, db, "trace-D", tHex, sHex, &endTime)

	m := NewRootSpanLifecycleManager(db, nil)
	emitted, err := m.ReconcileOrphanedRoots(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, emitted)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "trace.root/FaultInjection", spans[0].Name)

	// Synthesized flag must be present so operators can tell the difference
	// from a clean emit.
	found := false
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "aegis.root_synthesized" {
			found = true
			break
		}
	}
	assert.True(t, found, "synthesized root span must carry aegis.root_synthesized=true")

	var row model.Trace
	require.NoError(t, db.Where("id = ?", "trace-D").First(&row).Error)
	assert.True(t, row.OTelRootEmitted)
}

// Scenario E: Shutdown ends in-flight root spans with Status=Unset so the
// SDK still ships them on process exit (SIGTERM coverage).
func TestRootLifecycle_ShutdownEndsAllInflightSpans(t *testing.T) {
	exp := installInMemoryTracer(t)
	db := newInMemoryDB(t)
	seedTrace(t, db, "trace-E1", tHex, sHex, nil)
	seedTrace(t, db, "trace-E2", "20202020202020202020202020202020", "3030303030303030", nil)

	m := NewRootSpanLifecycleManager(db, nil)
	m.Spawn("trace-E1", "FaultInjection", tHex, sHex, 1, time.Now().Add(-5*time.Minute))
	m.Spawn("trace-E2", "FaultInjection",
		"20202020202020202020202020202020", "3030303030303030", 1,
		time.Now().Add(-5*time.Minute))
	time.Sleep(50 * time.Millisecond)

	m.Shutdown(context.Background())

	require.Eventually(t, func() bool {
		var rows []model.Trace
		require.NoError(t, db.Where("id IN ?", []string{"trace-E1", "trace-E2"}).Find(&rows).Error)
		if len(rows) != 2 {
			return false
		}
		return rows[0].OTelRootEmitted && rows[1].OTelRootEmitted
	}, 2*time.Second, 10*time.Millisecond)

	assert.Len(t, exp.GetSpans(), 2, "shutdown must end every in-flight root span")
}
