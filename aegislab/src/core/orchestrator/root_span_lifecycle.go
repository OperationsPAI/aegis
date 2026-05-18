package consumer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"

	"aegis/platform/consts"
	"aegis/platform/model"
	redisinfra "aegis/platform/redis"
	"aegis/platform/tracing"
)

// MaxTraceTimeout caps how long a root span can stay open before the safety
// net fires. Aligned with the stuck-trace reconciler threshold so a trace
// reported "stuck" by reconciler and a trace timing out at the root span
// agree on the deadline.
const MaxTraceTimeout = 6 * time.Hour

// RootDoneChannelPattern is the Redis pub/sub channel published by
// tryUpdateTraceStateCore when a trace transitions to a terminal state.
const RootDoneChannelPattern = "trace:%s:done"

func rootDoneChannel(traceID string) string {
	return fmt.Sprintf(RootDoneChannelPattern, traceID)
}

var rootInflightGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "aegis_trace_root_inflight",
	Help: "Number of in-flight long-lived OTel root spans held by the lifecycle goroutine",
})

// RootSpanLifecycleManager owns the per-trace goroutines that keep the
// long-lived root span open until either:
//   - the trace transitions to a terminal state (Redis pub/sub fires),
//   - MaxTraceTimeout elapses (safety net), or
//   - the process is shutting down (OnShutdown hook).
//
// Whichever fires first wins the CAS on Trace.OTelRootEmitted; the loser
// silently discards its span without calling End() so the SDK isn't fed
// duplicate finished spans for the same trace.
type RootSpanLifecycleManager struct {
	db           *gorm.DB
	redisGateway *redisinfra.Gateway

	mu       sync.Mutex
	inflight map[string]*rootSpanHandle
}

type rootSpanHandle struct {
	traceID   string
	span      oteltrace.Span
	cancel    context.CancelFunc
	ended     atomic.Bool
	startTime time.Time
}

var (
	globalRootLifecycle   *RootSpanLifecycleManager
	globalRootLifecycleMu sync.RWMutex
)

// SetGlobalRootSpanLifecycleManager wires the active manager so producer-side
// code (SubmitTaskWithDB → SpawnRootSpan) and consumer-side code
// (tryUpdateTraceStateCore → SignalRootSpanDone) can reach it without
// threading another dependency through fx. Set once at boot; subsequent
// callers see the latest value.
func SetGlobalRootSpanLifecycleManager(m *RootSpanLifecycleManager) {
	globalRootLifecycleMu.Lock()
	defer globalRootLifecycleMu.Unlock()
	globalRootLifecycle = m
}

// GlobalRootSpanLifecycleManager returns the active manager or nil if not
// yet wired (tests / pre-boot calls degrade silently).
func GlobalRootSpanLifecycleManager() *RootSpanLifecycleManager {
	globalRootLifecycleMu.RLock()
	defer globalRootLifecycleMu.RUnlock()
	return globalRootLifecycle
}

// NewRootSpanLifecycleManager constructs a manager. The redisGateway may be
// nil for tests that don't need pub/sub; the safety-net timer is sufficient
// to exercise CAS semantics.
func NewRootSpanLifecycleManager(db *gorm.DB, gateway *redisinfra.Gateway) *RootSpanLifecycleManager {
	return &RootSpanLifecycleManager{
		db:           db,
		redisGateway: gateway,
		inflight:     make(map[string]*rootSpanHandle),
	}
}

// Spawn opens the long-lived root span for a trace and starts the
// per-trace goroutine that waits for the terminal signal or the safety-net
// timeout. Idempotent: a second call for the same traceID is a no-op.
//
// The startTime should be the trace's StartTime so the emitted span covers
// the entire trace lifetime even if Spawn is called from a worker that
// picked the trace up after a small delay.
func (m *RootSpanLifecycleManager) Spawn(traceID, traceType, otelTraceID, otelRootSpanID string, flags uint8, startTime time.Time) {
	if m == nil {
		return
	}
	if otelTraceID == "" || otelRootSpanID == "" {
		return
	}

	m.mu.Lock()
	if _, exists := m.inflight[traceID]; exists {
		m.mu.Unlock()
		return
	}

	sc, err := tracing.NewRootSpanContext(otelTraceID, otelRootSpanID, flags)
	if err != nil {
		m.mu.Unlock()
		logrus.WithError(err).WithField("trace_id", traceID).Warn("Spawn: invalid persisted SpanContext")
		return
	}

	parentCtx := oteltrace.ContextWithRemoteSpanContext(context.Background(), sc)
	_, span := otel.Tracer("rcabench/trace").Start(parentCtx,
		fmt.Sprintf("trace.root/%s", traceType),
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithTimestamp(startTime),
		oteltrace.WithAttributes(
			attribute.String("aegis.trace_id", traceID),
			attribute.String("aegis.trace_type", traceType),
		),
	)

	ctx, cancel := context.WithCancel(context.Background())
	handle := &rootSpanHandle{
		traceID:   traceID,
		span:      span,
		cancel:    cancel,
		startTime: startTime,
	}
	m.inflight[traceID] = handle
	rootInflightGauge.Inc()
	m.mu.Unlock()

	go m.watch(ctx, handle, traceType)
}

// watch blocks until either the pub/sub terminal signal arrives, the
// MaxTraceTimeout safety net fires, or the context is cancelled by Shutdown.
// Calls endIfWinner exactly once on whichever path wins.
func (m *RootSpanLifecycleManager) watch(ctx context.Context, h *rootSpanHandle, traceType string) {
	defer func() {
		m.mu.Lock()
		delete(m.inflight, h.traceID)
		m.mu.Unlock()
		rootInflightGauge.Dec()
	}()

	timeoutCh := time.After(MaxTraceTimeout)

	if m.redisGateway != nil {
		pubsub, err := m.redisGateway.Subscribe(ctx, rootDoneChannel(h.traceID))
		if err == nil {
			defer func() { _ = pubsub.Close() }()
			msgCh := pubsub.Channel()
			select {
			case <-msgCh:
				m.endIfWinner(h, codes.Ok, "trace terminal-state signal", time.Now())
				return
			case <-timeoutCh:
				m.endIfWinner(h, codes.Error, "trace timeout — root closed by safety net", time.Now())
				return
			case <-ctx.Done():
				m.endIfWinner(h, codes.Unset, "aborted by shutdown", time.Now())
				return
			}
		}
		logrus.WithError(err).WithField("trace_id", h.traceID).
			Warn("root lifecycle: pubsub subscribe failed, relying on timeout/shutdown")
	}

	select {
	case <-timeoutCh:
		m.endIfWinner(h, codes.Error, "trace timeout — root closed by safety net", time.Now())
	case <-ctx.Done():
		m.endIfWinner(h, codes.Unset, "aborted by shutdown", time.Now())
	}
	_ = traceType
}

// endIfWinner CASes Trace.OTelRootEmitted from false to true. If we win,
// End() the span; if not, drop it silently — the reconciler already emitted
// a synthesized root span for this trace.
func (m *RootSpanLifecycleManager) endIfWinner(h *rootSpanHandle, status codes.Code, description string, when time.Time) {
	if !h.ended.CompareAndSwap(false, true) {
		return
	}

	won := m.casEmitted(h.traceID)
	if !won {
		// Reconciler beat us to it. Drop the span without End() — the SDK
		// will GC the unrecorded handle on next batch flush.
		return
	}

	if status != codes.Unset {
		h.span.SetStatus(status, description)
	}
	h.span.End(oteltrace.WithTimestamp(when))
}

// casEmitted attempts the OTelRootEmitted false→true transition. Returns
// true iff this caller won the CAS. A nil DB returns true so tests using
// the manager without a DB still exercise span emission paths.
func (m *RootSpanLifecycleManager) casEmitted(traceID string) bool {
	if m.db == nil {
		return true
	}
	res := m.db.Model(&model.Trace{}).
		Where("id = ? AND o_tel_root_emitted = ?", traceID, false).
		Update("o_tel_root_emitted", true)
	if res.Error != nil {
		logrus.WithError(res.Error).WithField("trace_id", traceID).
			Warn("root lifecycle: CAS update failed")
		return false
	}
	return res.RowsAffected > 0
}

// SignalDone publishes the terminal-state signal so the per-trace goroutine
// closes the root span at the time the trace actually finished. Best-effort
// — the safety-net timeout / reconciler fallback covers Redis flake.
func (m *RootSpanLifecycleManager) SignalDone(ctx context.Context, traceID string) {
	if m == nil || m.redisGateway == nil {
		return
	}
	if err := m.redisGateway.Publish(ctx, rootDoneChannel(traceID), "done"); err != nil {
		logrus.WithError(err).WithField("trace_id", traceID).
			Warn("root lifecycle: failed to publish terminal-state signal")
	}
}

// Shutdown ends every in-flight root span with Status=Unset and reason
// "aborted by shutdown". Called from the OnStop fx hook before the
// TracerProvider flushes so the SDK gets a chance to export the partials.
func (m *RootSpanLifecycleManager) Shutdown(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	handles := make([]*rootSpanHandle, 0, len(m.inflight))
	for _, h := range m.inflight {
		handles = append(handles, h)
	}
	m.mu.Unlock()
	for _, h := range handles {
		h.cancel()
		m.endIfWinner(h, codes.Unset, "aborted by shutdown", time.Now())
	}
	_ = ctx
}

// ReconcileOrphanedRoots scans for terminal-state traces that never had
// their root span emitted (worker process crash, Redis pub/sub message
// loss bypassing the safety net somehow). For each, emit a backdated
// synthesized root span and CAS OTelRootEmitted true.
//
// Returns the number of synthesized roots emitted on this sweep.
func (m *RootSpanLifecycleManager) ReconcileOrphanedRoots(ctx context.Context, maxBatch int) (int, error) {
	if m == nil || m.db == nil {
		return 0, nil
	}
	if maxBatch <= 0 {
		maxBatch = 50
	}

	var rows []model.Trace
	err := m.db.WithContext(ctx).
		Where("o_tel_root_emitted = ? AND end_time IS NOT NULL AND o_tel_trace_id <> ?", false, "").
		Limit(maxBatch).
		Find(&rows).Error
	if err != nil {
		return 0, fmt.Errorf("query orphaned roots: %w", err)
	}

	emitted := 0
	for i := range rows {
		row := &rows[i]
		if row.EndTime == nil {
			continue
		}

		sc, err := tracing.NewRootSpanContext(row.OTelTraceID, row.OTelRootSpanID, row.OTelFlags)
		if err != nil {
			logrus.WithError(err).WithField("trace_id", row.ID).
				Warn("reconcile: invalid persisted SpanContext")
			continue
		}

		// Race guard: only proceed if we win the CAS. A live goroutine
		// might publish between our SELECT and UPDATE.
		if !m.casEmitted(row.ID) {
			continue
		}

		parentCtx := oteltrace.ContextWithRemoteSpanContext(ctx, sc)
		_, span := otel.Tracer("rcabench/trace").Start(parentCtx,
			fmt.Sprintf("trace.root/%s", consts.GetTraceTypeName(row.Type)),
			oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
			oteltrace.WithTimestamp(row.StartTime),
			oteltrace.WithAttributes(
				attribute.String("aegis.trace_id", row.ID),
				attribute.String("aegis.trace_type", consts.GetTraceTypeName(row.Type)),
				attribute.Bool("aegis.root_synthesized", true),
				attribute.String("aegis.root.persisted_span_id", row.OTelRootSpanID),
			),
		)
		span.End(oteltrace.WithTimestamp(*row.EndTime))
		emitted++
	}
	return emitted, nil
}
