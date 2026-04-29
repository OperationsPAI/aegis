package consumer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"aegis/dto"

	"github.com/sirupsen/logrus"
)

// fakeFreshnessProbe is a deterministic FreshnessProbe used by the
// issue #210 pre-flight tests. It walks a canned response sequence; once
// exhausted, the last response is sticky so "always too stale" / "always
// errors" stories work without bookkeeping.
type fakeFreshnessProbe struct {
	responses []freshnessProbeResponse
	calls     atomic.Int32
}

type freshnessProbeResponse struct {
	ts  time.Time
	ok  bool
	err error
}

func (f *fakeFreshnessProbe) MaxTraceTimestamp(_ context.Context, _ string) (time.Time, bool, error) {
	idx := int(f.calls.Add(1)) - 1
	if idx >= len(f.responses) {
		idx = len(f.responses) - 1
	}
	r := f.responses[idx]
	return r.ts, r.ok, r.err
}

func (f *fakeFreshnessProbe) callCount() int { return int(f.calls.Load()) }

// TestAcquireBuildDatapackToken_AcquiresOnFirstTry covers the happy path:
// when the bucket has capacity, AcquireToken returns (true, nil) and we
// never fall through to WaitForToken.
func TestAcquireBuildDatapackToken_AcquiresOnFirstTry(t *testing.T) {
	issuer := newFakeIssuer("build_datapack", 8)
	task := &dto.UnifiedTask{TaskID: "tid-1", TraceID: "trc-1"}
	logEntry := logrus.NewEntry(logrus.StandardLogger())

	acquired, err := acquireBuildDatapackToken(context.Background(), issuer, task, logEntry)
	if err != nil {
		t.Fatalf("acquireBuildDatapackToken: %v", err)
	}
	if !acquired {
		t.Fatalf("expected acquired=true on empty bucket, got false")
	}
	if issuer.acquireCalls != 1 {
		t.Fatalf("AcquireToken calls = %d, want 1", issuer.acquireCalls)
	}
	if issuer.waitCalls != 0 {
		t.Fatalf("WaitForToken calls = %d, want 0 (only used when bucket is full)", issuer.waitCalls)
	}
	if issuer.inUse != 1 {
		t.Fatalf("inUse = %d, want 1 after successful acquire", issuer.inUse)
	}
}

// TestAcquireBuildDatapackToken_FallsThroughToWait covers the surge path:
// AcquireToken returns (false, nil) when the bucket is exhausted, which
// must trigger a WaitForToken poll. If wait succeeds, the caller proceeds.
func TestAcquireBuildDatapackToken_FallsThroughToWait(t *testing.T) {
	issuer := newFakeIssuer("build_datapack", 8)
	issuer.exhausted = true // first AcquireToken call returns false
	task := &dto.UnifiedTask{TaskID: "tid-2", TraceID: "trc-2"}
	logEntry := logrus.NewEntry(logrus.StandardLogger())

	// Lift the exhausted flag inside WaitForToken's check by leaving
	// exhaustedWait false; fakeIssuer only blocks WaitForToken via inUse>=cap.
	acquired, err := acquireBuildDatapackToken(context.Background(), issuer, task, logEntry)
	if err != nil {
		t.Fatalf("acquireBuildDatapackToken: %v", err)
	}
	if !acquired {
		t.Fatalf("expected acquired=true after wait succeeds, got false")
	}
	if issuer.acquireCalls != 1 {
		t.Fatalf("AcquireToken calls = %d, want 1", issuer.acquireCalls)
	}
	if issuer.waitCalls != 1 {
		t.Fatalf("WaitForToken calls = %d, want 1 (bucket was full)", issuer.waitCalls)
	}
}

// TestAcquireBuildDatapackToken_ReschedulesOnTimeout is the critical test
// for the inject-loop overload bug: when both AcquireToken and WaitForToken
// return (false, nil), the helper must report acquired=false so the caller
// can reschedule the task instead of barreling into createDatapackJob and
// hammering ClickHouse beyond its concurrent-query ceiling.
func TestAcquireBuildDatapackToken_ReschedulesOnTimeout(t *testing.T) {
	issuer := newFakeIssuer("build_datapack", 8)
	issuer.exhausted = true
	issuer.exhaustedWait = true
	task := &dto.UnifiedTask{TaskID: "tid-3", TraceID: "trc-3"}
	logEntry := logrus.NewEntry(logrus.StandardLogger())

	acquired, err := acquireBuildDatapackToken(context.Background(), issuer, task, logEntry)
	if err != nil {
		t.Fatalf("acquireBuildDatapackToken: %v", err)
	}
	if acquired {
		t.Fatalf("expected acquired=false when wait times out, got true")
	}
	if issuer.acquireCalls != 1 {
		t.Fatalf("AcquireToken calls = %d, want 1", issuer.acquireCalls)
	}
	if issuer.waitCalls != 1 {
		t.Fatalf("WaitForToken calls = %d, want 1", issuer.waitCalls)
	}
	if issuer.inUse != 0 {
		t.Fatalf("inUse = %d, want 0 (no token actually acquired)", issuer.inUse)
	}
}

// TestAcquireBuildDatapackToken_PropagatesAcquireError ensures a Redis
// connection flake on AcquireToken bubbles up to the caller (which then
// handleExecutionError-marks the task), instead of silently falling
// through to WaitForToken on a degraded backend.
func TestAcquireBuildDatapackToken_PropagatesAcquireError(t *testing.T) {
	issuer := newFakeIssuer("build_datapack", 8)
	issuer.acquireErr = errors.New("redis: connection refused")
	task := &dto.UnifiedTask{TaskID: "tid-4", TraceID: "trc-4"}
	logEntry := logrus.NewEntry(logrus.StandardLogger())

	acquired, err := acquireBuildDatapackToken(context.Background(), issuer, task, logEntry)
	if err == nil {
		t.Fatalf("expected acquire error to propagate, got nil")
	}
	if acquired {
		t.Fatalf("expected acquired=false on acquire error, got true")
	}
	if issuer.waitCalls != 0 {
		t.Fatalf("WaitForToken should not be called when AcquireToken errors: %d calls", issuer.waitCalls)
	}
}

// withFastFreshnessBackoff shrinks the freshness probe backoff for the
// duration of one test so the multi-probe path stays well under the
// repo-wide 60s test timeout.
func withFastFreshnessBackoff(t *testing.T) {
	t.Helper()
	prevInit, prevMax := freshnessInitialBackoff, freshnessMaxBackoff
	freshnessInitialBackoff = 5 * time.Millisecond
	freshnessMaxBackoff = 20 * time.Millisecond
	t.Cleanup(func() {
		freshnessInitialBackoff = prevInit
		freshnessMaxBackoff = prevMax
	})
}

// TestWaitForCHFreshness_ReturnsNilOnceFresh covers the happy path: the
// first three probes report a max(Timestamp) that's older than the
// (abnormal_end - watermark) deadline; the 4th probe finally crosses the
// threshold. waitForCHFreshness must return nil after the 4th call and
// not keep probing.
func TestWaitForCHFreshness_ReturnsNilOnceFresh(t *testing.T) {
	withFastFreshnessBackoff(t)
	// Use a recent abnormalEnd so the retro-recovery fast-path is not triggered.
	abnormalEnd := time.Now()
	watermark := 30 * time.Second
	stale := abnormalEnd.Add(-2 * time.Minute) // < deadline (= abnormalEnd-30s)
	fresh := abnormalEnd.Add(-10 * time.Second) // >= deadline
	probe := &fakeFreshnessProbe{
		responses: []freshnessProbeResponse{
			{ts: stale, ok: true},
			{ts: stale, ok: true},
			{ts: stale, ok: true},
			{ts: fresh, ok: true},
		},
	}
	logEntry := logrus.NewEntry(logrus.StandardLogger())
	err := waitForCHFreshness(context.Background(), probe, "ts0", abnormalEnd, watermark, 5*time.Second, logEntry)
	if err != nil {
		t.Fatalf("waitForCHFreshness: %v", err)
	}
	if got := probe.callCount(); got != 4 {
		t.Fatalf("probe call count = %d, want 4", got)
	}
}

// TestWaitForCHFreshness_TimesOut covers the timeout path: every probe
// keeps reporting a stale max(Timestamp). After maxWait the helper must
// return errFreshnessTimeout (a sentinel the executor maps to a task
// reschedule, NOT to datapack.build.failed).
func TestWaitForCHFreshness_TimesOut(t *testing.T) {
	withFastFreshnessBackoff(t)
	// Use a recent abnormalEnd so the retro-recovery fast-path is not triggered.
	abnormalEnd := time.Now()
	stale := abnormalEnd.Add(-5 * time.Minute)
	probe := &fakeFreshnessProbe{
		responses: []freshnessProbeResponse{{ts: stale, ok: true}},
	}
	logEntry := logrus.NewEntry(logrus.StandardLogger())
	err := waitForCHFreshness(context.Background(), probe, "ts0", abnormalEnd, 30*time.Second, 50*time.Millisecond, logEntry)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !errors.Is(err, errFreshnessTimeout) {
		t.Fatalf("expected errFreshnessTimeout, got %v", err)
	}
	if !errorsIsFreshnessTimeout(err) {
		t.Fatalf("errorsIsFreshnessTimeout should match the timeout sentinel")
	}
	if probe.callCount() < 1 {
		t.Fatalf("probe should have been called at least once")
	}
}

// TestWaitForCHFreshness_PropagatesProbeError covers the "don't silently
// retry forever on a misconfigured DSN" requirement: a CH error from the
// probe must surface to the caller (which then handleExecutionError-marks
// the task) instead of being swallowed by the retry loop.
func TestWaitForCHFreshness_PropagatesProbeError(t *testing.T) {
	withFastFreshnessBackoff(t)
	// Use a recent abnormalEnd so the retro-recovery fast-path is not triggered.
	abnormalEnd := time.Now()
	probeErr := errors.New("clickhouse: connection refused")
	probe := &fakeFreshnessProbe{
		responses: []freshnessProbeResponse{{err: probeErr}},
	}
	logEntry := logrus.NewEntry(logrus.StandardLogger())
	err := waitForCHFreshness(context.Background(), probe, "ts0", abnormalEnd, 30*time.Second, 5*time.Second, logEntry)
	if err == nil {
		t.Fatalf("expected probe error to propagate, got nil")
	}
	if errorsIsFreshnessTimeout(err) {
		t.Fatalf("probe error must NOT be reported as the timeout sentinel: %v", err)
	}
	if !errors.Is(err, probeErr) {
		t.Fatalf("probe error chain lost; want wrap of %v, got %v", probeErr, err)
	}
	if got := probe.callCount(); got != 1 {
		t.Fatalf("probe call count = %d, want 1 (errors must not retry)", got)
	}
}

// TestWaitForCHFreshness_NoOpWhenProbeNil documents the back-compat
// guarantee: if RuntimeDeps.FreshnessProbe is nil (unit-test path or a
// pre-issue-#210 deployment that hasn't wired the probe yet),
// waitForCHFreshness must return immediately without blocking the
// executor. This is the safety hatch — no probe, no race-fix, but also
// no regression vs. the pre-PR behavior.
func TestWaitForCHFreshness_NoOpWhenProbeNil(t *testing.T) {
	abnormalEnd := time.Now()
	logEntry := logrus.NewEntry(logrus.StandardLogger())
	if err := waitForCHFreshness(context.Background(), nil, "ts0", abnormalEnd, 30*time.Second, 5*time.Second, logEntry); err != nil {
		t.Fatalf("nil probe should be a no-op, got %v", err)
	}
}

// TestWaitForCHFreshness_SkipsProbeForRetroRecovery covers issue #295:
// reconciler-recovered traces with abnormalEnd > 1h ago must not loop
// forever. The probe's Timestamp >= now()-1h predicate filters out all
// data for such traces, returning a zero timestamp on every call. The
// retro-recovery fast path must bypass the probe entirely and return nil
// so BuildDatapack proceeds immediately.
func TestWaitForCHFreshness_SkipsProbeForRetroRecovery(t *testing.T) {
	// abnormalEnd 2 hours in the past — well beyond freshnessProbeRetroGrace.
	abnormalEnd := time.Now().Add(-2 * time.Hour)
	probe := &fakeFreshnessProbe{
		// Responses would always be zero (probe's 1h window filters them
		// out), but the probe must NOT be called at all.
		responses: []freshnessProbeResponse{{ok: false}},
	}
	logEntry := logrus.NewEntry(logrus.StandardLogger())
	err := waitForCHFreshness(context.Background(), probe, "ts0", abnormalEnd, 30*time.Second, 5*time.Second, logEntry)
	if err != nil {
		t.Fatalf("retro-recovery trace should skip probe and return nil, got %v", err)
	}
	if got := probe.callCount(); got != 0 {
		t.Fatalf("probe call count = %d, want 0 (probe must be skipped for old abnormalEnd)", got)
	}
}

// TestWaitForCHFreshness_ProbesWhenAbnormalEndWithinGrace ensures live
// traces whose abnormalEnd is within the grace window still go through the
// probe loop, preserving the original freshness-waiting behaviour.
func TestWaitForCHFreshness_ProbesWhenAbnormalEndWithinGrace(t *testing.T) {
	withFastFreshnessBackoff(t)
	// abnormalEnd 30 minutes in the past — within freshnessProbeRetroGrace.
	abnormalEnd := time.Now().Add(-30 * time.Minute)
	fresh := abnormalEnd.Add(-10 * time.Second) // >= deadline (abnormalEnd - watermark)
	probe := &fakeFreshnessProbe{
		responses: []freshnessProbeResponse{{ts: fresh, ok: true}},
	}
	logEntry := logrus.NewEntry(logrus.StandardLogger())
	err := waitForCHFreshness(context.Background(), probe, "ts0", abnormalEnd, 30*time.Second, 5*time.Second, logEntry)
	if err != nil {
		t.Fatalf("waitForCHFreshness: %v", err)
	}
	if got := probe.callCount(); got < 1 {
		t.Fatalf("probe call count = %d, want >= 1 (probe must run within grace window)", got)
	}
}

// TestExtractNamespaceFromBenchmarkEnv ensures the executor pulls the
// per-task namespace out of the benchmark env-var list (the
// fault-injection callback prepends NAMESPACE before submitting the
// BuildDatapack task), and falls back to "" when missing — in which case
// waitForCHFreshness still works against a table-wide max(Timestamp).
func TestExtractNamespaceFromBenchmarkEnv(t *testing.T) {
	cases := []struct {
		name string
		in   []dto.ParameterItem
		want string
	}{
		{name: "happy", in: []dto.ParameterItem{{Key: "NAMESPACE", Value: "ts0"}}, want: "ts0"},
		{name: "missing", in: []dto.ParameterItem{{Key: "OTHER", Value: "x"}}, want: ""},
		{name: "non-string-value", in: []dto.ParameterItem{{Key: "NAMESPACE", Value: 42}}, want: ""},
		{name: "empty", in: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractNamespaceFromBenchmarkEnv(tc.in); got != tc.want {
				t.Fatalf("extractNamespaceFromBenchmarkEnv = %q, want %q", got, tc.want)
			}
		})
	}
}
