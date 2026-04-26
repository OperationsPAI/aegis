package consumer

import (
	"context"
	"errors"
	"testing"

	"aegis/dto"

	"github.com/sirupsen/logrus"
)

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
