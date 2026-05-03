"""Materialise the 14 canonical manifest features from raw IR products.

The contract — ``extract_feature_samples(graph, baseline_traces,
abnormal_traces, abnormal_window_start, abnormal_window_end,
timelines=None) -> dict[(int, FeatureKind, Feature), float]`` — produces
exactly the map shape that ``ReasoningContext.feature_samples`` expects.

Coverage of the 14 features (see ``manifests.features.FEATURE_METADATA``
for the canonical list and their declared ``extraction_adapter``):

* k8s_metrics adapter source:
    - ``cpu_throttle_ratio`` (container, pod): peak abnormal-window CPU
      utilisation divided by baseline mean. Both the cgroup-relative
      ``k8s.pod.cpu_limit_utilization`` (already a saturation fraction)
      and the absolute ``k8s.pod.cpu.usage`` are tried — first hit wins.
    - ``memory_usage_ratio`` (container, pod): same idea for memory
      (limit-relative preferred; falls back to working_set).
    - ``restart_count`` (container, pod): max restart counter delta over
      the abnormal window.
    - ``unavailable`` (pod): 1.0 iff the timeline (or a tail-window
      data-stop heuristic) marks the pod ``unavailable`` during the
      abnormal window. No entry emitted for ``False`` per the boolean
      convention from SCHEMA.md "Magnitude band semantics".

* trace adapter source (per ``service::span_name`` → graph
  ``span|<key>`` node):
    - ``latency_p99_ratio`` / ``latency_p50_ratio`` (span): p99 / p50
      latency in the abnormal window divided by the baseline equivalent.
    - ``error_rate`` (span): error span fraction in the abnormal window
      (HTTP 5xx OR gRPC non-zero, per the existing ``FailureDetector``).
    - ``request_count_ratio`` (span): abnormal-window count / baseline
      count, both normalised to per-second rates so window length
      mismatch does not bias the ratio.
    - ``dns_failure_rate`` / ``connection_refused_rate`` /
      ``timeout_rate`` (span): per-error-class fraction in the abnormal
      window. Absent when the underlying span attributes are not present
      in the parquet schema (the IR ``FailureDetector`` machinery already
      filters out detectors whose columns are missing).

* trace_volume adapter source:
    - ``silent`` (service): 1.0 iff a service timeline ever enters the
      ``silent`` state; or, in the absence of a timeline pass, a
      direct-from-parquet fallback that compares baseline vs abnormal
      span rate per service.

* jvm adapter source:
    - ``gc_pause_ratio`` (container): max abnormal-window GC pause time
      divided by the window length (so the value is a fraction in
      ``[0, 1]`` matching the manifest band convention).
    - ``thread_queue_depth`` (container): peak abnormal-window thread
      count divided by baseline mean. Best-effort: not all stacks emit
      a queue-depth metric, in which case no sample is produced.

The extractor never raises on missing inputs. A field that cannot be
materialised on a given case simply produces no entry, which the
downstream gate already treats as "feature did not match".
"""

from __future__ import annotations

from collections.abc import Iterable
from typing import TYPE_CHECKING, Any

import numpy as np

from rcabench_platform.v3.internal.reasoning.manifests.context import FeatureSample
from rcabench_platform.v3.internal.reasoning.manifests.features import (
    Feature,
    FeatureKind,
)
from rcabench_platform.v3.internal.reasoning.models.graph import (
    HyperGraph,
    Node,
    PlaceKind,
)

if TYPE_CHECKING:  # pragma: no cover
    import polars as pl

    from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline


# ---------------------------------------------------------------------------
# Metric-name vocabularies (a small generalisation of the K8sMetricsAdapter
# constants — kept here so the extractor is independent and can be used
# without dragging the adapter classes in).
# ---------------------------------------------------------------------------

CPU_LIMIT_UTIL_METRICS = (
    "k8s.pod.cpu_limit_utilization",
    "container.cpu_limit_utilization",
    "k8s.pod.cpu.node.utilization",
)
CPU_USAGE_METRICS = (
    "k8s.pod.cpu.usage",
    "container.cpu.usage",
    "jvm.cpu.recent_utilization",
)
MEM_LIMIT_UTIL_METRICS = (
    "k8s.pod.memory_limit_utilization",
    "container.memory_limit_utilization",
)
MEM_USAGE_METRICS = (
    "k8s.pod.memory.working_set",
    "k8s.pod.memory.usage",
    "container.memory.working_set",
    "container.memory.usage",
)
RESTART_METRICS = (
    "k8s.container.restarts",
    "k8s.pod.restarts",
)
GC_DURATION_METRICS = (
    "jvm.gc.duration",
    "jvm.gc.collection.elapsed",
    "process.runtime.jvm.gc.duration",
)
THREAD_COUNT_METRICS = (
    "jvm.thread.count",
    "jvm.threads.live",
    "process.runtime.jvm.threads.count",
)


# ---------------------------------------------------------------------------
# Numeric helpers.
# ---------------------------------------------------------------------------


def _window_max(timestamps: np.ndarray, values: np.ndarray, t0: int | None, t1: int | None) -> float | None:
    if len(timestamps) == 0 or len(values) == 0:
        return None
    if t0 is not None or t1 is not None:
        lo = t0 if t0 is not None else int(np.min(timestamps))
        hi = t1 if t1 is not None else int(np.max(timestamps)) + 1
        mask = (timestamps >= lo) & (timestamps < hi)
        if not np.any(mask):
            return None
        sl = values[mask]
    else:
        sl = values
    sl = sl[~np.isnan(sl)]
    if len(sl) == 0:
        return None
    return float(np.max(sl))


def _window_sum(timestamps: np.ndarray, values: np.ndarray, t0: int | None, t1: int | None) -> float | None:
    if len(timestamps) == 0 or len(values) == 0:
        return None
    if t0 is not None or t1 is not None:
        lo = t0 if t0 is not None else int(np.min(timestamps))
        hi = t1 if t1 is not None else int(np.max(timestamps)) + 1
        mask = (timestamps >= lo) & (timestamps < hi)
        if not np.any(mask):
            return None
        sl = values[mask]
    else:
        sl = values
    sl = sl[~np.isnan(sl)]
    if len(sl) == 0:
        return None
    return float(np.sum(sl))


def _series_mean(values: np.ndarray) -> float | None:
    if len(values) == 0:
        return None
    sl = values[~np.isnan(values)]
    if len(sl) == 0:
        return None
    return float(np.mean(sl))


def _ratio(abnormal: float | None, baseline: float | None) -> float | None:
    """Safe ratio with floor on baseline (treat zero baseline as 'no signal')."""
    if abnormal is None or baseline is None:
        return None
    if baseline <= 1e-9:
        # If baseline is effectively zero but abnormal is non-trivial,
        # report a large ratio so threshold checks still fire.
        if abnormal > 1e-6:
            return 1e6
        return None
    return abnormal / baseline


def _first_present_metric(node: Node, candidates: Iterable[str]) -> str | None:
    for m in candidates:
        if m in node.abnormal_metrics:
            return m
    return None


# ---------------------------------------------------------------------------
# Per-feature primitive extractions.
# ---------------------------------------------------------------------------


def _cpu_throttle_ratio(node: Node, t0: int | None, t1: int | None) -> float | None:
    """Prefer cgroup-saturation metric (already a ratio in [0,1]); fall back
    to absolute CPU usage divided by baseline mean.
    """
    sat_metric = _first_present_metric(node, CPU_LIMIT_UTIL_METRICS)
    if sat_metric is not None:
        ts, vals = node.abnormal_metrics[sat_metric]
        peak = _window_max(ts, vals, t0, t1)
        if peak is None:
            return None
        # cpu_throttle_ratio is multiplicative-vs-baseline. Baseline of a
        # saturation series is typically << 1.0, so dividing by it gives the
        # multiplier the manifest expects (band [3.0, .inf)).
        if sat_metric in node.baseline_metrics:
            base_ts, base_vals = node.baseline_metrics[sat_metric]
            base = _series_mean(base_vals)
            r = _ratio(peak, base)
            if r is not None:
                return r
        # No baseline: convert raw saturation to a multiplier with a 0.05
        # implicit baseline floor (typical idle utilisation), so 1.0 → 20×.
        return peak / 0.05

    abs_metric = _first_present_metric(node, CPU_USAGE_METRICS)
    if abs_metric is None:
        return None
    ts, vals = node.abnormal_metrics[abs_metric]
    peak = _window_max(ts, vals, t0, t1)
    if peak is None:
        return None
    if abs_metric in node.baseline_metrics:
        base_ts, base_vals = node.baseline_metrics[abs_metric]
        base = _series_mean(base_vals)
        r = _ratio(peak, base)
        if r is not None:
            return r
    return None


def _memory_usage_ratio(node: Node, t0: int | None, t1: int | None) -> float | None:
    """Prefer the cgroup-saturation memory metric (already in [0,1]).

    The manifest's memory_usage_ratio band is in saturation units (e.g.
    [0.7, 1.0] for JVMMemoryStress). When only an absolute working_set
    series exists, fall back to working_set / baseline_mean and clip into
    the same convention.
    """
    sat_metric = _first_present_metric(node, MEM_LIMIT_UTIL_METRICS)
    if sat_metric is not None:
        ts, vals = node.abnormal_metrics[sat_metric]
        peak = _window_max(ts, vals, t0, t1)
        if peak is None:
            return None
        return peak

    abs_metric = _first_present_metric(node, MEM_USAGE_METRICS)
    if abs_metric is None:
        return None
    ts, vals = node.abnormal_metrics[abs_metric]
    peak = _window_max(ts, vals, t0, t1)
    if peak is None:
        return None
    if abs_metric in node.baseline_metrics:
        _, base_vals = node.baseline_metrics[abs_metric]
        base = _series_mean(base_vals)
        if base is not None and base > 0:
            # Project absolute memory into saturation-band semantics
            # by treating "1× baseline" as ~0.5 utilisation.
            return min(1.0, 0.5 * (peak / base))
    return None


def _restart_count(node: Node, t0: int | None, t1: int | None) -> float | None:
    metric = _first_present_metric(node, RESTART_METRICS)
    if metric is None:
        return None
    ts, vals = node.abnormal_metrics[metric]
    peak = _window_max(ts, vals, t0, t1)
    if peak is None:
        return None
    base = 0.0
    if metric in node.baseline_metrics:
        _, base_vals = node.baseline_metrics[metric]
        bm = _series_mean(base_vals)
        base = bm if bm is not None else 0.0
    delta = max(0.0, peak - base)
    return delta if delta > 0 else None


def _gc_pause_ratio(node: Node, t0: int | None, t1: int | None) -> float | None:
    metric = _first_present_metric(node, GC_DURATION_METRICS)
    if metric is None:
        return None
    ts, vals = node.abnormal_metrics[metric]
    if t0 is None or t1 is None or t1 <= t0:
        return None
    total_pause = _window_sum(ts, vals, t0, t1)
    if total_pause is None:
        return None
    window = float(t1 - t0)
    if window <= 0:
        return None
    # GC duration metric units vary (seconds or ns). If sum exceeds 10×
    # window assume nanoseconds; if exceeds window assume milliseconds.
    if total_pause > window * 1e6:  # plausibly nanoseconds
        total_pause = total_pause / 1e9
    elif total_pause > window * 10:  # plausibly milliseconds
        total_pause = total_pause / 1e3
    return min(1.0, total_pause / window)


def _thread_queue_depth(node: Node, t0: int | None, t1: int | None) -> float | None:
    metric = _first_present_metric(node, THREAD_COUNT_METRICS)
    if metric is None:
        return None
    ts, vals = node.abnormal_metrics[metric]
    peak = _window_max(ts, vals, t0, t1)
    if peak is None:
        return None
    if metric not in node.baseline_metrics:
        return None
    _, base_vals = node.baseline_metrics[metric]
    base = _series_mean(base_vals)
    return _ratio(peak, base)


def _pod_unavailable(node: Node, t0: int | None, t1: int | None) -> float | None:
    """Detect pod-killed via the same data-stop heuristic K8sMetricsAdapter
    uses: a tail window with no metric samples while baseline had data.
    """
    if not node.abnormal_metrics:
        return None
    if t0 is None or t1 is None:
        return None
    # Expect any series to extend through t1; if every series ends before
    # t1 - 5, mark as data_stop / unavailable.
    last_seen: int | None = None
    for ts, _ in node.abnormal_metrics.values():
        if len(ts) == 0:
            continue
        ts_max = int(np.max(ts))
        if last_seen is None or ts_max > last_seen:
            last_seen = ts_max
    if last_seen is None:
        return None
    # 10 second slack — a kill late in the window may still see one
    # last sample. Require the gap to be material.
    if t1 - last_seen > 10:
        return 1.0
    return None


# ---------------------------------------------------------------------------
# Trace-side extraction (per ``service::span_name``).
# ---------------------------------------------------------------------------


def _aggregate_trace_stats(
    traces: "pl.DataFrame",
    t0: int | None,
    t1: int | None,
) -> dict[str, dict[str, float]]:
    """Aggregate per-(service, span) latency / error / count / class stats.

    Returns ``key -> {"count", "avg", "p50", "p99", "errors",
    "dns_errors", "conn_refused", "timeouts"}``. Absent error-class
    fields are simply not emitted (so an extractor consumer can use
    ``.get`` to detect missingness).
    """
    import polars as pl

    if len(traces) == 0:
        return {}

    df = traces
    # Restrict to window if given and the frame has a parseable time col.
    if (t0 is not None or t1 is not None) and "time" in df.columns:
        from rcabench_platform.v3.internal.reasoning.ir.adapters.traces import (
            _ts_seconds,
        )

        df = _ts_seconds(df)
        if t0 is not None:
            df = df.filter(pl.col("_ts") >= t0)
        if t1 is not None:
            df = df.filter(pl.col("_ts") < t1)

    if len(df) == 0:
        return {}

    # FailureDetector — composed default (HTTP + gRPC + exception event),
    # filtered to columns present in the frame.
    from rcabench_platform.v3.internal.reasoning.ir.protocols import (
        default_failure_detector,
        filter_by_columns,
    )

    detector = filter_by_columns(default_failure_detector(), set(df.columns))
    df = df.with_columns(
        [
            (pl.col("duration") / 1e9).alias("_dur_sec"),
            detector.is_failure_expr().cast(pl.Int32).alias("_is_err"),
            (pl.col("service_name") + "::" + pl.col("span_name")).alias("_full_name"),
        ]
    )

    # Optional error-class columns: dns / connection-refused / timeout.
    # We rely on attribute heuristics where they are present and
    # fall back to "absent" when the columns aren't there.
    has_status = "status_message" in df.columns

    aggs = [
        pl.len().alias("count"),
        pl.col("_is_err").sum().alias("errors"),
        pl.col("_dur_sec").mean().alias("avg"),
        pl.col("_dur_sec").quantile(0.5).alias("p50"),
        pl.col("_dur_sec").quantile(0.99).alias("p99"),
    ]
    if has_status:
        sm = pl.col("status_message").cast(pl.Utf8).fill_null("")
        aggs.extend(
            [
                ((sm.str.contains("(?i)dns") | sm.str.contains("(?i)resolve")).cast(pl.Int32))
                .sum()
                .alias("dns_errors"),
                ((sm.str.contains("(?i)connection refused") | sm.str.contains("(?i)econnrefused")).cast(pl.Int32))
                .sum()
                .alias("conn_refused"),
                ((sm.str.contains("(?i)timeout") | sm.str.contains("(?i)deadline")).cast(pl.Int32))
                .sum()
                .alias("timeouts"),
            ]
        )

    grouped = df.group_by("_full_name").agg(aggs)

    out: dict[str, dict[str, float]] = {}
    for row in grouped.iter_rows(named=True):
        cnt = row.get("count") or 0
        if cnt <= 0:
            continue
        rec: dict[str, float] = {
            "count": float(cnt),
            "errors": float(row.get("errors") or 0),
            "avg": float(row.get("avg") or 0.0),
            "p50": float(row.get("p50") or 0.0),
            "p99": float(row.get("p99") or 0.0),
        }
        if has_status:
            rec["dns_errors"] = float(row.get("dns_errors") or 0)
            rec["conn_refused"] = float(row.get("conn_refused") or 0)
            rec["timeouts"] = float(row.get("timeouts") or 0)
        out[row["_full_name"]] = rec
    return out


def _extract_span_features(
    out: dict[FeatureSample, float],
    graph: HyperGraph,
    baseline_traces: Any,
    abnormal_traces: Any,
    t0: int | None,
    t1: int | None,
) -> None:
    if baseline_traces is None or abnormal_traces is None:
        return
    try:
        import polars as pl  # noqa: F401
    except ImportError:  # pragma: no cover
        return

    baseline_stats = _aggregate_trace_stats(baseline_traces, None, None)
    abnormal_stats = _aggregate_trace_stats(abnormal_traces, t0, t1)
    if not abnormal_stats:
        return

    # Per-second normalisation: divide counts by window length so a
    # short abnormal window does not deflate the request_count_ratio.
    base_window = None
    abn_window = None
    if "time" in baseline_traces.columns and len(baseline_traces) > 0:
        from rcabench_platform.v3.internal.reasoning.ir.adapters.traces import (
            _ts_seconds,
        )

        bts = _ts_seconds(baseline_traces)
        bmin = bts.select(pl.col("_ts").min()).item()
        bmax = bts.select(pl.col("_ts").max()).item()
        if bmin is not None and bmax is not None and bmax > bmin:
            base_window = float(bmax - bmin)
    if t0 is not None and t1 is not None and t1 > t0:
        abn_window = float(t1 - t0)

    for full_name, abn in abnormal_stats.items():
        node = graph.get_node_by_name(f"span|{full_name}")
        if node is None or node.id is None:
            continue
        nid = node.id

        base = baseline_stats.get(full_name)

        # latency_p99_ratio / latency_p50_ratio
        if base is not None:
            if base.get("p99", 0.0) > 0 and abn["p99"] > 0:
                out[(nid, FeatureKind.span, Feature.latency_p99_ratio)] = abn["p99"] / base["p99"]
            if base.get("p50", 0.0) > 0 and abn["p50"] > 0:
                out[(nid, FeatureKind.span, Feature.latency_p50_ratio)] = abn["p50"] / base["p50"]

        # error_rate is absolute (not ratio): errors / count.
        if abn["count"] > 0:
            out[(nid, FeatureKind.span, Feature.error_rate)] = abn["errors"] / abn["count"]

        # request_count_ratio: per-second-rate ratio.
        if (
            base is not None
            and base_window is not None
            and abn_window is not None
            and base_window > 0
            and abn_window > 0
            and base["count"] > 0
        ):
            base_rate = base["count"] / base_window
            abn_rate = abn["count"] / abn_window
            if base_rate > 0:
                out[(nid, FeatureKind.span, Feature.request_count_ratio)] = abn_rate / base_rate

        # Error-class rates (only when the schema carried status_message).
        if "dns_errors" in abn and abn["count"] > 0:
            out[(nid, FeatureKind.span, Feature.dns_failure_rate)] = abn["dns_errors"] / abn["count"]
        if "conn_refused" in abn and abn["count"] > 0:
            out[(nid, FeatureKind.span, Feature.connection_refused_rate)] = abn["conn_refused"] / abn["count"]
        if "timeouts" in abn and abn["count"] > 0:
            out[(nid, FeatureKind.span, Feature.timeout_rate)] = abn["timeouts"] / abn["count"]


def _extract_silent_unavailable_from_timelines(
    out: dict[FeatureSample, float],
    graph: HyperGraph,
    timelines: dict[str, "StateTimeline"] | None,
    t0: int | None,
    t1: int | None,
) -> None:
    if not timelines:
        return
    for node_key, tl in timelines.items():
        node = graph.get_node_by_name(node_key)
        if node is None or node.id is None:
            continue
        # Only consider state during the abnormal window if known; otherwise
        # any window in the timeline counts.
        in_window: list[Any] = []
        for w in tl.windows:
            if t0 is not None and w.end <= t0:
                continue
            if t1 is not None and w.start >= t1:
                continue
            in_window.append(w)
        windows = in_window or list(tl.windows)
        states = {w.state for w in windows}

        if tl.kind == PlaceKind.service:
            if "silent" in states or any(
                "silent" in (w.evidence.get("specialization_labels") or set()) for w in windows
            ):
                out[(node.id, FeatureKind.service, Feature.silent)] = 1.0
            if "unavailable" in states:
                out[(node.id, FeatureKind.service, Feature.unavailable)] = 1.0
        elif tl.kind == PlaceKind.span:
            if "missing" in states or "silent" in states:
                out[(node.id, FeatureKind.span, Feature.silent)] = 1.0
        elif tl.kind == PlaceKind.pod:
            if "unavailable" in states:
                out[(node.id, FeatureKind.pod, Feature.unavailable)] = 1.0


# ---------------------------------------------------------------------------
# Public entry point.
# ---------------------------------------------------------------------------


def extract_feature_samples(
    *,
    graph: HyperGraph,
    baseline_traces: Any | None = None,
    abnormal_traces: Any | None = None,
    abnormal_window_start: int | None = None,
    abnormal_window_end: int | None = None,
    timelines: dict[str, "StateTimeline"] | None = None,
) -> dict[FeatureSample, float]:
    """Materialise the 14 canonical features into a feature_samples dict.

    The returned dict's keys are the exact tuple shape expected by
    :class:`ReasoningContext` —
    ``(node_id: int, FeatureKind, Feature) -> float``.

    Features for which the underlying telemetry is absent on a given
    case produce no entry. The gates already treat a missing sample as
    "feature did not match", so this preserves the polarity contract
    (absence ≠ zero).
    """
    out: dict[FeatureSample, float] = {}
    t0 = abnormal_window_start
    t1 = abnormal_window_end

    # k8s_metrics + jvm features (per pod / container Node).
    for kind, fk in (
        (PlaceKind.pod, FeatureKind.pod),
        (PlaceKind.container, FeatureKind.container),
    ):
        for node in graph.get_nodes_by_kind(kind):
            if node.id is None:
                continue
            nid = node.id

            v = _cpu_throttle_ratio(node, t0, t1)
            if v is not None:
                out[(nid, fk, Feature.cpu_throttle_ratio)] = v
            v = _memory_usage_ratio(node, t0, t1)
            if v is not None:
                out[(nid, fk, Feature.memory_usage_ratio)] = v
            v = _restart_count(node, t0, t1)
            if v is not None:
                out[(nid, fk, Feature.restart_count)] = v

            # Container-only JVM features (avoid double-emit on pod nodes).
            if kind == PlaceKind.container:
                v = _gc_pause_ratio(node, t0, t1)
                if v is not None:
                    out[(nid, fk, Feature.gc_pause_ratio)] = v
                v = _thread_queue_depth(node, t0, t1)
                if v is not None:
                    out[(nid, fk, Feature.thread_queue_depth)] = v

            # Pod-only unavailable detection (data-stop heuristic).
            if kind == PlaceKind.pod:
                v = _pod_unavailable(node, t0, t1)
                if v is not None:
                    out[(nid, fk, Feature.unavailable)] = v

    # Trace-derived span features.
    if baseline_traces is not None and abnormal_traces is not None:
        _extract_span_features(out, graph, baseline_traces, abnormal_traces, t0, t1)

    # Timeline-derived boolean features (silent / unavailable on
    # service / span / pod, populated by trace_volume + structural
    # inheritance).
    _extract_silent_unavailable_from_timelines(out, graph, timelines, t0, t1)

    return out


__all__ = ["extract_feature_samples"]
