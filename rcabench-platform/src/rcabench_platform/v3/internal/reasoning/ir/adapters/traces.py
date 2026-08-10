"""TraceStateAdapter — span/service state from baseline-vs-abnormal trace comparison.

Translation of ``ParquetDataLoader.identify_alarm_nodes_v2`` from a one-shot
alarm-set scorer into a stateful, sliding-window adapter. The adapter:

- Aggregates baseline traces in one bucket per ``service_name::span_name`` key.
- Walks abnormal traces in 3-second windows, comparing each window's
  per-key stats against the baseline using the same adaptive-threshold
  primitive (``get_adaptive_threshold``) that powers the legacy detector.
- Emits ``Transition`` events with ``EvidenceLevel.observed``:

  - ``span.SLOW`` — p99 or avg latency exceeds adaptive threshold.
  - ``span.ERRORING`` — error rate elevated above baseline + minimum floor.
  - ``span.MISSING`` — present in baseline, absent in window.
  - ``span.HEALTHY`` — return-to-baseline after a non-healthy window.

- Service-level state is the rollup of each service's root spans for the
  same window: any SLOW/ERRORING/MISSING root span maps the service to
  ``service.SLOW`` / ``service.ERRORING`` / ``service.UNAVAILABLE``.

Trigger metric on each transition is one of ``"p99_latency"``,
``"avg_latency"``, ``"error_rate"``, ``"missing"``, ``"recovery"``.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass

import polars as pl

from rcabench_platform.v3.internal.reasoning.algorithms.baseline_detector import (
    get_adaptive_threshold,
)
from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.evidence import Evidence, EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.protocols import (
    FailureDetector,
    default_failure_detector,
    filter_by_columns,
)
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind

DEFAULT_WINDOW_SEC = 3
DEFAULT_ERROR_RATE_FLOOR = 0.1
DEFAULT_MIN_CALLS = 1


@dataclass(frozen=True, slots=True)
class _BaselineStat:
    avg: float
    std: float
    p99: float
    err_rate: float
    count: int


@dataclass(frozen=True, slots=True)
class _WindowStat:
    avg: float
    p99: float
    err_rate: float
    count: int


def _aggregate_baseline(
    traces: pl.DataFrame,
    failure_detector: FailureDetector,
) -> dict[str, _BaselineStat]:
    if len(traces) == 0:
        return {}
    df = traces.with_columns(
        [
            (pl.col("duration") / 1e9).alias("duration_sec"),
            failure_detector.is_failure_expr().cast(pl.Int32).alias("is_error"),
            (pl.col("service_name") + "::" + pl.col("span_name")).alias("full_span_name"),
        ]
    )
    agg = df.group_by("full_span_name").agg(
        [
            pl.len().alias("call_count"),
            pl.col("is_error").sum().alias("error_count"),
            pl.col("duration_sec").mean().alias("avg_duration"),
            pl.col("duration_sec").std().alias("std_duration"),
            pl.col("duration_sec").quantile(0.99).alias("p99_duration"),
        ]
    )
    out: dict[str, _BaselineStat] = {}
    for row in agg.iter_rows(named=True):
        cnt = row["call_count"] or 0
        if cnt <= 0:
            continue
        out[row["full_span_name"]] = _BaselineStat(
            avg=float(row["avg_duration"] or 0.0),
            std=float(row["std_duration"] or 0.0),
            p99=float(row["p99_duration"] or 0.0),
            err_rate=float((row["error_count"] or 0) / cnt),
            count=int(cnt),
        )
    return out


def _aggregate_window(
    traces: pl.DataFrame,
    failure_detector: FailureDetector,
) -> dict[str, _WindowStat]:
    if len(traces) == 0:
        return {}
    df = traces.with_columns(
        [
            (pl.col("duration") / 1e9).alias("duration_sec"),
            failure_detector.is_failure_expr().cast(pl.Int32).alias("is_error"),
            (pl.col("service_name") + "::" + pl.col("span_name")).alias("full_span_name"),
        ]
    )
    agg = df.group_by("full_span_name").agg(
        [
            pl.len().alias("call_count"),
            pl.col("is_error").sum().alias("error_count"),
            pl.col("duration_sec").mean().alias("avg_duration"),
            pl.col("duration_sec").quantile(0.99).alias("p99_duration"),
        ]
    )
    out: dict[str, _WindowStat] = {}
    for row in agg.iter_rows(named=True):
        cnt = row["call_count"] or 0
        if cnt <= 0:
            continue
        out[row["full_span_name"]] = _WindowStat(
            avg=float(row["avg_duration"] or 0.0),
            p99=float(row["p99_duration"] or 0.0),
            err_rate=float((row["error_count"] or 0) / cnt),
            count=int(cnt),
        )
    return out


def _ts_seconds(traces: pl.DataFrame) -> pl.DataFrame:
    if "time" not in traces.columns:
        return traces.with_columns(pl.lit(0).alias("_ts"))
    sample = traces.select(pl.col("time").drop_nulls().first()).item()
    if sample is None:
        return traces.with_columns(pl.lit(0).alias("_ts"))
    if isinstance(sample, int):
        if sample > 10**14:
            return traces.with_columns((pl.col("time") // 1_000_000_000).alias("_ts"))
        if sample > 10**11:
            return traces.with_columns((pl.col("time") // 1_000).alias("_ts"))
        return traces.with_columns(pl.col("time").alias("_ts"))
    return traces.with_columns(pl.col("time").dt.timestamp("ms").floordiv(1000).alias("_ts"))


def _classify(
    base: _BaselineStat,
    win: _WindowStat,
    error_rate_floor: float,
) -> tuple[str, str, float, float] | None:
    """Return (to_state, trigger_metric, observed, threshold) or None if healthy."""
    if win.err_rate > base.err_rate and win.err_rate >= error_rate_floor:
        return "erroring", "error_rate", win.err_rate, max(base.err_rate, error_rate_floor)
    if base.avg > 0:
        cv = base.std / base.avg if base.avg > 1e-6 else 0.0
        avg_mult = get_adaptive_threshold(base.avg, cv)
        if win.avg > base.avg * avg_mult:
            return "slow", "avg_latency", win.avg, base.avg * avg_mult
    if base.p99 > 0:
        p99_std = base.std * 1.5
        cv99 = p99_std / base.p99 if base.p99 > 1e-6 else 0.0
        p99_mult = get_adaptive_threshold(base.p99, cv99)
        if win.p99 > base.p99 * p99_mult:
            return "slow", "p99_latency", win.p99, base.p99 * p99_mult
    return None


def _service_rollup(span_state: str) -> str:
    if span_state == "slow":
        return "slow"
    if span_state == "erroring":
        return "erroring"
    if span_state == "missing":
        return "unavailable"
    return "healthy"


class TraceStateAdapter:
    """Sliding-window trace anomaly emitter."""

    name = "traces"

    def __init__(
        self,
        baseline_traces: pl.DataFrame,
        abnormal_traces: pl.DataFrame,
        *,
        window_sec: int = DEFAULT_WINDOW_SEC,
        error_rate_floor: float = DEFAULT_ERROR_RATE_FLOOR,
        min_calls: int = DEFAULT_MIN_CALLS,
        failure_detector: FailureDetector | None = None,
    ) -> None:
        self._baseline = baseline_traces
        self._abnormal = abnormal_traces
        self._window_sec = window_sec
        self._error_rate_floor = error_rate_floor
        self._min_calls = min_calls
        # Per-system FailureDetector contract (§9.2). Default = HTTP OR gRPC.
        # Trim detectors whose required columns aren't present in the input
        # frames — polars raises ColumnNotFoundError otherwise. Use the
        # union of baseline + abnormal columns so a detector applicable to
        # either side is preserved.
        detector = failure_detector if failure_detector is not None else default_failure_detector()
        available = set(baseline_traces.columns) | set(abnormal_traces.columns)
        self._failure_detector: FailureDetector = filter_by_columns(detector, available)

    def emit(self, ctx: AdapterContext) -> Iterable[Transition]:
        return list(self._emit_all())

    def _emit_all(self) -> Iterable[Transition]:
        baseline_stats = _aggregate_baseline(self._baseline, self._failure_detector)
        if not baseline_stats and len(self._abnormal) == 0:
            return

        if len(self._abnormal) == 0:
            return

        df = _ts_seconds(self._abnormal)
        ts_min = df.select(pl.col("_ts").min()).item()
        ts_max = df.select(pl.col("_ts").max()).item()
        if ts_min is None or ts_max is None:
            return
        ts_min = int(ts_min)
        ts_max = int(ts_max)

        window = self._window_sec
        last_state: dict[str, str] = {}
        # Service rollup: per service per window track worst observed state
        service_last_state: dict[str, str] = {}

        # Pre-compute root span set in baseline (for service rollup)
        if "parent_span_id" in self._baseline.columns:
            root_keys = set(
                self._baseline.filter(pl.col("parent_span_id") == "")
                .with_columns((pl.col("service_name") + "::" + pl.col("span_name")).alias("k"))
                .select("k")
                .to_series()
                .to_list()
            )
        else:
            root_keys = set()

        baseline_keys = set(baseline_stats.keys())

        for w_start in range(ts_min, ts_max + 1, window):
            w_end = w_start + window
            chunk = df.filter((pl.col("_ts") >= w_start) & (pl.col("_ts") < w_end))
            win_stats = _aggregate_window(chunk, self._failure_detector)
            seen_keys = set(win_stats.keys())

            service_worst: dict[str, str] = {}

            for key, ws in win_stats.items():
                if ws.count < self._min_calls:
                    continue
                base = baseline_stats.get(key)
                if base is None or base.count < self._min_calls:
                    continue
                classification = _classify(base, ws, self._error_rate_floor)
                node_key = f"span|{key}"
                if classification is None:
                    if last_state.get(node_key, "healthy") != "healthy":
                        yield Transition(
                            node_key=node_key,
                            kind=PlaceKind.span,
                            at=w_start,
                            from_state=last_state[node_key],
                            to_state="healthy",
                            trigger="recovery",
                            level=EvidenceLevel.observed,
                            evidence={"trigger_metric": "recovery"},
                        )
                        last_state[node_key] = "healthy"
                    continue
                to_state, trig, observed, threshold = classification
                prev = last_state.get(node_key, "healthy")
                if prev != to_state:
                    yield Transition(
                        node_key=node_key,
                        kind=PlaceKind.span,
                        at=w_start,
                        from_state=prev,
                        to_state=to_state,
                        trigger=trig,
                        level=EvidenceLevel.observed,
                        evidence={
                            "trigger_metric": trig,
                            "observed": observed,
                            "threshold": threshold,
                        },
                    )
                    last_state[node_key] = to_state
                if key in root_keys:
                    svc = key.split("::", 1)[0]
                    cand = _service_rollup(to_state)
                    service_worst[svc] = _pick_worst(service_worst.get(svc, "healthy"), cand)

            # Missing detection: baseline has it, this window doesn't
            for key in baseline_keys - seen_keys:
                base = baseline_stats[key]
                if base.count < self._min_calls:
                    continue
                node_key = f"span|{key}"
                if last_state.get(node_key, "healthy") != "missing":
                    yield Transition(
                        node_key=node_key,
                        kind=PlaceKind.span,
                        at=w_start,
                        from_state=last_state.get(node_key, "healthy"),
                        to_state="missing",
                        trigger="missing",
                        level=EvidenceLevel.observed,
                        evidence={
                            "trigger_metric": "missing",
                            "observed": 0.0,
                            "threshold": float(base.count),
                        },
                    )
                    last_state[node_key] = "missing"
                if key in root_keys:
                    svc = key.split("::", 1)[0]
                    service_worst[svc] = _pick_worst(service_worst.get(svc, "healthy"), "unavailable")

            for svc, st in service_worst.items():
                node_key = f"service|{svc}"
                prev = service_last_state.get(node_key, "healthy")
                if prev != st:
                    yield Transition(
                        node_key=node_key,
                        kind=PlaceKind.service,
                        at=w_start,
                        from_state=prev,
                        to_state=st,
                        trigger="rollup",
                        level=EvidenceLevel.observed,
                        evidence={"trigger_metric": "rollup"},
                    )
                    service_last_state[node_key] = st

            # Service recovery: services not in service_worst this window go HEALTHY
            for node_key, prev in list(service_last_state.items()):
                svc_name = node_key.split("|", 1)[1]
                if svc_name not in service_worst and prev != "healthy":
                    yield Transition(
                        node_key=node_key,
                        kind=PlaceKind.service,
                        at=w_start,
                        from_state=prev,
                        to_state="healthy",
                        trigger="recovery",
                        level=EvidenceLevel.observed,
                        evidence={"trigger_metric": "recovery"},
                    )
                    service_last_state[node_key] = "healthy"


_SEVERITY_ROLLUP = {"healthy": 0, "slow": 1, "erroring": 2, "unavailable": 3}


def _pick_worst(a: str, b: str) -> str:
    return a if _SEVERITY_ROLLUP.get(a, 0) >= _SEVERITY_ROLLUP.get(b, 0) else b


__all__ = ["TraceStateAdapter"]


# Suppress unused import warning — Evidence is a structural type used in dict literals.
_ = Evidence
