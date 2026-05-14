package consumer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2"
	chrow "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/dto"
	db "aegis/platform/db"
)

// FreshnessProbe is the minimum surface waitForCHFreshness needs to read
// the latest trace ingestion timestamp out of ClickHouse. The interface is
// the seam used by build_datapack_test.go; the production implementation
// is clickHouseFreshnessProbe below.
//
// MaxTraceTimestamp returns the most recent Timestamp visible in
// otel.otel_traces, optionally narrowed to a single service.namespace.
// The bool result is false when no row matches (the table is empty for
// this namespace, NOT an error). When the row exists, the time.Time is
// the ingested span timestamp.
type FreshnessProbe interface {
	MaxTraceTimestamp(ctx context.Context, namespace string) (time.Time, bool, error)
}

// clickHouseFreshnessProbe is the production implementation; it reuses the
// already-imported clickhouse-go/v2 driver (same dependency the aegisctl
// `clickhouse.otel-tables` check uses, see cmd/aegisctl/cluster/live_env.go)
// and reads its host/port/user/password from the same [database.clickhouse]
// section that NewDatabaseConfig("clickhouse") consumes for the BuildDatapack
// Job env vars. Connections are short-lived: open → query → close, so we do
// not introduce a new long-lived pool.
type clickHouseFreshnessProbe struct {
	cfg *db.DatabaseConfig
}

// NewClickHouseFreshnessProbe builds the default production probe from the
// loaded [database.clickhouse] config block.
func NewClickHouseFreshnessProbe() FreshnessProbe {
	return &clickHouseFreshnessProbe{cfg: db.NewDatabaseConfig("clickhouse")}
}

func (p *clickHouseFreshnessProbe) MaxTraceTimestamp(ctx context.Context, namespace string) (time.Time, bool, error) {
	ctx, span := otel.Tracer("aegis/observability").Start(ctx, "observability/clickhouse/MaxTraceTimestamp")
	defer span.End()
	span.SetAttributes(
		attribute.String("db.system", "clickhouse"),
		attribute.String("db.namespace", namespace),
	)

	if p.cfg == nil || p.cfg.Host == "" {
		return time.Time{}, false, fmt.Errorf("clickhouse host not configured")
	}
	conn, err := chdriver.Open(freshnessProbeOptions(p.cfg))
	if err != nil {
		return time.Time{}, false, fmt.Errorf("clickhouse open: %w", err)
	}
	defer func() { _ = conn.Close() }()

	var (
		row chrow.Row
		ts  time.Time
		stmt string
	)
	// Why the Timestamp >= now() - INTERVAL 1 HOUR + max_partitions_to_read=2:
	// otel.otel_traces is `PARTITION BY toDate(Timestamp) ORDER BY (ServiceName,
	// SpanName, toDateTime(Timestamp))`. With Timestamp third in the sort key,
	// `MAX(Timestamp)` cannot use a primary-key shortcut and degrades to reading
	// per-part metadata across every active part. On byte-cluster (#289) this
	// pushed p50 to ~3s and p95 past 50s with 400+ parts; concurrent K>=3
	// freshness probes routinely tripped client deadlines and surfaced as
	// `Code 394 QUERY_WAS_CANCELLED`. The 1-hour predicate (a) prunes to at
	// most today's plus the immediately-prior partition (max_partitions_to_read=2
	// covers the midnight rollover) and (b) restricts per-part scanning to
	// recent granules. Probe semantics are unchanged: the freshness deadline
	// is always within seconds of now(), so any ingest lag <1h is still
	// detected, and a namespace with zero recent ingest still returns a zero
	// timestamp (treated as "not fresh") exactly as before.
	if namespace != "" {
		stmt = "SELECT max(Timestamp) FROM otel.otel_traces " +
			"WHERE ResourceAttributes['service.namespace'] = ? " +
			"AND Timestamp >= now() - INTERVAL 1 HOUR " +
			"SETTINGS max_partitions_to_read = 2"
		row = conn.QueryRow(ctx, stmt, namespace)
	} else {
		stmt = "SELECT max(Timestamp) FROM otel.otel_traces " +
			"WHERE Timestamp >= now() - INTERVAL 1 HOUR " +
			"SETTINGS max_partitions_to_read = 2"
		row = conn.QueryRow(ctx, stmt)
	}
	span.SetAttributes(attribute.String("db.statement", truncateStmt(stmt, 500)))
	if err := row.Scan(&ts); err != nil {
		return time.Time{}, false, fmt.Errorf("clickhouse query max(Timestamp): %w", err)
	}
	if ts.IsZero() {
		// No rows for this namespace yet; the OTel collector hasn't
		// flushed any spans. Treat as "not fresh" rather than an error.
		return time.Time{}, false, nil
	}
	return ts, true, nil
}

// freshnessProbeOptions builds the clickhouse-go/v2 driver options used by
// the freshness probe.
//
// Why HTTP: the dynamic etcd config (`database.clickhouse.port`) holds the
// HTTP listener (8123) — the same port the BuildDatapack Job env vars and
// clickstack tooling target. clickhouse-go/v2 defaults to the native binary
// protocol (port 9000); speaking native bytes at 8123 yields
// `unexpected packet [72] from server` (the 'H' of "HTTP/1.1"), which broke
// every BuildDatapack run after the freshness pre-flight landed (#222) and
// is the regression tracked in #226. Pinning Protocol=HTTP here keeps the
// probe consistent with the configured port without introducing a new etcd
// key. Extracted as a pure helper so unit tests can assert the choice
// without opening a network connection.
func freshnessProbeOptions(cfg *db.DatabaseConfig) *chdriver.Options {
	return &chdriver.Options{
		Addr: []string{net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))},
		Auth: chdriver.Auth{
			Database: orDefaultStr(cfg.Database, "otel"),
			Username: cfg.User,
			Password: cfg.Password,
		},
		Protocol:    chdriver.HTTP,
		DialTimeout: 3 * time.Second,
	}
}

func truncateStmt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func orDefaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// freshnessProbeRetroGrace is the age threshold beyond which the freshness
// probe is skipped for retro-recovery traces. It matches the 1-hour
// Timestamp predicate in MaxTraceTimestamp: once abnormalEnd is older than
// this, the probe's SELECT window filters out all relevant data and would
// return a zero timestamp forever, causing pointless rescheduling (issue
// #295). Any trace whose abnormalEnd is further in the past than this grace
// period has already had its OTel exporter flush complete.
const freshnessProbeRetroGrace = 1 * time.Hour

// waitForCHFreshness blocks until ClickHouse has spans freshly ingested up
// to (abnormalEnd − watermark), bounded by maxWait. Closes the race
// documented in issue #210: BuildDatapack used to fire `prepare_inputs.py`
// straight from the executor entry point and could race the OTel
// exporter's retry queue under cluster load, producing an empty
// abnormal_traces.parquet → ValueError → datapack.build.failed.
//
// Probe cadence: exponential backoff starting at 5s, doubling, capped at
// 30s between probes. Every probe is logged at INFO level so operators
// watching the logs can see the wait progress.
//
// Return contract:
//   - nil when the namespace is fresh enough (max(Timestamp) >= deadline).
//   - nil when abnormalEnd is older than freshnessProbeRetroGrace (retro
//     recovery fast-path: the OTel exporter has long since caught up).
//   - context error if ctx is cancelled.
//   - errFreshnessTimeout when maxWait is exhausted; the caller is expected
//     to bump the task into the retry queue (rescheduleBuildDatapackTask)
//     rather than mark it datapack.build.failed.
//   - the underlying probe error on persistent CH failure (don't silently
//     retry forever on a misconfigured DSN).
func waitForCHFreshness(
	ctx context.Context,
	probe FreshnessProbe,
	namespace string,
	abnormalEnd time.Time,
	watermark time.Duration,
	maxWait time.Duration,
	logEntry *logrus.Entry,
) error {
	if probe == nil {
		// No probe wired (test paths / pre-PR-#210 deployments). Do not
		// block the executor — preserve old behavior.
		return nil
	}
	if logEntry == nil {
		logEntry = logrus.NewEntry(logrus.StandardLogger())
	}
	// Retro-recovery fast path (issue #295): if abnormalEnd is older than
	// freshnessProbeRetroGrace, the probe's Timestamp >= now()-1h predicate
	// would filter out all relevant CH data and return a zero timestamp,
	// causing the probe loop to run until timeout and reschedule forever.
	// The OTel exporter has long since flushed for such old traces, so skip
	// the probe entirely.
	if time.Since(abnormalEnd) > freshnessProbeRetroGrace {
		logEntry.WithFields(logrus.Fields{
			"namespace":    namespace,
			"abnormal_end": abnormalEnd.UTC().Format(time.RFC3339),
		}).Info("abnormalEnd is past probe lookback window; skipping freshness probe")
		return nil
	}
	deadlineTs := abnormalEnd.Add(-watermark)
	probeBudget := time.Now().Add(maxWait)

	backoff := freshnessInitialBackoff
	maxBackoff := freshnessMaxBackoff
	attempt := 0
	for {
		attempt++
		ts, ok, err := probe.MaxTraceTimestamp(ctx, namespace)
		if err != nil {
			// Propagate the error rather than burn the budget on a
			// misconfigured CH DSN; the caller turns it into a retryable
			// task error.
			return fmt.Errorf("ch freshness probe: %w", err)
		}
		logEntry.WithFields(logrus.Fields{
			"attempt":      attempt,
			"namespace":    namespace,
			"abnormal_end": abnormalEnd.UTC().Format(time.RFC3339),
			"deadline":     deadlineTs.UTC().Format(time.RFC3339),
			"ch_max_ts":    formatTSOrEmpty(ts, ok),
			"watermark":    watermark.String(),
		}).Info("ch freshness probe")
		if ok && !ts.Before(deadlineTs) {
			return nil
		}
		if time.Now().After(probeBudget) {
			return fmt.Errorf("%w: namespace=%q abnormal_end=%s waited=%s",
				errFreshnessTimeout, namespace, abnormalEnd.UTC().Format(time.RFC3339), maxWait)
		}
		// Sleep with backoff; respect ctx cancellation.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// errFreshnessTimeout signals that the bounded retry budget for the CH
// freshness check was exhausted. The BuildDatapack executor maps this to
// a reschedule (retryable) instead of handleExecutionError ("failed").
var errFreshnessTimeout = errors.New("clickhouse not fresh enough within max wait")

// freshnessInitialBackoff and freshnessMaxBackoff are package-level vars
// (not consts) so unit tests can shrink them to keep the test suite well
// under the 60s timeout while still exercising the multi-probe path. In
// production they default to 5s/30s as documented in waitForCHFreshness.
var (
	freshnessInitialBackoff = 5 * time.Second
	freshnessMaxBackoff     = 30 * time.Second
)

func formatTSOrEmpty(ts time.Time, ok bool) string {
	if !ok || ts.IsZero() {
		return "<none>"
	}
	return ts.UTC().Format(time.RFC3339)
}

// freshnessParamsFromConfig reads the watermark and max-wait bounds from
// the rate_limiting.* dynamic config keys, falling back to the
// consts.BuildDatapackFreshness* defaults. Reads are direct config.GetInt
// calls, so a runtime `aegisctl` push to etcd applies on the next probe
// without a backend rebuild (same pattern as max_concurrent_build_datapack).
// errorsIsFreshnessTimeout reports whether err is the bounded-wait
// timeout sentinel (vs. a probe error or context cancellation).
func errorsIsFreshnessTimeout(err error) bool {
	return errors.Is(err, errFreshnessTimeout)
}

// extractNamespaceFromBenchmarkEnv pulls the per-task target namespace
// out of the benchmark env var list. The fault-injection callback path
// (k8s_handler.go) prepends a NAMESPACE override at the top of EnvVars
// before submitting BuildDatapack, so it reliably points at the namespace
// the abnormal traffic was injected into. Returns "" if no NAMESPACE env
// var is present, in which case waitForCHFreshness falls back to a
// table-wide max(Timestamp) probe (still race-closing, just less precise).
func extractNamespaceFromBenchmarkEnv(envVars []dto.ParameterItem) string {
	for _, ev := range envVars {
		if ev.Key != "NAMESPACE" {
			continue
		}
		if s, ok := ev.Value.(string); ok {
			return s
		}
	}
	return ""
}

func freshnessParamsFromConfig() (watermark, maxWait time.Duration) {
	w := config.GetInt(consts.MaxTokensKeyBuildDatapackFreshnessWatermark)
	if w <= 0 {
		w = consts.DefaultBuildDatapackFreshnessWatermarkSeconds
	}
	mw := config.GetInt(consts.MaxTokensKeyBuildDatapackFreshnessMaxWait)
	if mw <= 0 {
		mw = consts.DefaultBuildDatapackFreshnessMaxWaitSeconds
	}
	return time.Duration(w) * time.Second, time.Duration(mw) * time.Second
}
