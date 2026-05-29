package consumer

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"aegis/platform/config"
	"aegis/platform/consts"
)

// fakeTrafficProbe returns a scripted span count per call. A negative entry
// signals a probe error for that call.
type fakeTrafficProbe struct {
	counts []int
	calls  atomic.Int32
}

func (f *fakeTrafficProbe) RecentSpanCount(_ context.Context, _ string, _ time.Duration) (uint64, error) {
	i := int(f.calls.Add(1)) - 1
	v := f.counts[len(f.counts)-1]
	if i < len(f.counts) {
		v = f.counts[i]
	}
	if v < 0 {
		return 0, fmt.Errorf("clickhouse unreachable")
	}
	return uint64(v), nil
}

// setGateConfig shrinks the poll to 1s so the gate loop runs fast under the
// 60s test budget; iteration count is bounded by the scripted probe.
func setGateConfig(t *testing.T, minSpans, timeoutSecs int) {
	t.Helper()
	viper.Set(consts.PedestalTrafficGateMinSpansKey, minSpans)
	viper.Set(consts.PedestalTrafficGatePollKey, 1)
	viper.Set(consts.PedestalTrafficGateTimeoutKey, timeoutSecs)
	t.Cleanup(func() {
		viper.Set(consts.PedestalTrafficGateMinSpansKey, nil)
		viper.Set(consts.PedestalTrafficGatePollKey, nil)
		viper.Set(consts.PedestalTrafficGateTimeoutKey, nil)
	})
}

func TestWaitForTrafficPresence_TrafficSeenReturnsTrue(t *testing.T) {
	setGateConfig(t, 50, 5)
	probe := &fakeTrafficProbe{counts: []int{0, 10, 75}}

	got := waitForTrafficPresence(context.Background(), probe, "sn0", logrus.NewEntry(logrus.New()))
	if !got {
		t.Fatalf("expected gate to confirm traffic once span_count >= min_spans, got false")
	}
}

func TestWaitForTrafficPresence_TimeoutFallsBack(t *testing.T) {
	setGateConfig(t, 50, 1)
	// Always below threshold → gate must exhaust the bounded timeout and fall
	// back (return false) rather than block forever.
	probe := &fakeTrafficProbe{counts: []int{0, 1, 2}}

	got := waitForTrafficPresence(context.Background(), probe, "sn0", logrus.NewEntry(logrus.New()))
	if got {
		t.Fatalf("expected gate to fall back to timer (false) when threshold never met, got true")
	}
}

func TestWaitForTrafficPresence_ProbeErrorFallsBack(t *testing.T) {
	setGateConfig(t, 50, 1)
	probe := &fakeTrafficProbe{counts: []int{-1}}

	got := waitForTrafficPresence(context.Background(), probe, "sn0", logrus.NewEntry(logrus.New()))
	if got {
		t.Fatalf("expected gate to fall back (false) on probe error, got true")
	}
}

func TestWaitForTrafficPresence_NilProbeFallsBack(t *testing.T) {
	got := waitForTrafficPresence(context.Background(), nil, "sn0", logrus.NewEntry(logrus.New()))
	if got {
		t.Fatalf("expected gate to fall back (false) when no probe is wired, got true")
	}
}

func TestRestartPostReadySoakDuration_PerSystemWarmupWins(t *testing.T) {
	viper.Set("orchestrator.pedestal.warmup_seconds", 60)
	t.Cleanup(func() { viper.Set("orchestrator.pedestal.warmup_seconds", nil) })

	withOverride := config.ChaosSystemConfig{WarmupSeconds: 420}
	if got := restartPostReadySoakDuration(withOverride); got != 420*time.Second {
		t.Fatalf("per-system warmup_seconds should win: got %s, want 420s", got)
	}

	noOverride := config.ChaosSystemConfig{}
	if got := restartPostReadySoakDuration(noOverride); got != 60*time.Second {
		t.Fatalf("no per-system override should fall back to global warmup_seconds: got %s, want 60s", got)
	}
}

func TestRestartPostReadySoakDuration_FallsBackToConstDefault(t *testing.T) {
	viper.Set("orchestrator.pedestal.warmup_seconds", nil)

	if got := restartPostReadySoakDuration(config.ChaosSystemConfig{}); got != consts.DefaultFixedPedestalWarmupSeconds*time.Second {
		t.Fatalf("with neither override nor global set, want const default %ds, got %s",
			consts.DefaultFixedPedestalWarmupSeconds, got)
	}
}
