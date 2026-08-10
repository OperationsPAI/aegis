"""K8sMetricsAdapter — pod/container state from baseline-aware metric anomalies.

Per pod/container Node, this adapter:

- Computes per-metric baseline statistics (``compute_baseline_statistics``)
  once over the entire baseline window.
- Walks the abnormal window in 5-second slices and asks
  ``BaselineAwareDetector.is_critical_anomaly`` per metric.
- Maps the metric → canonical state + specialization label:

    k8s.pod.cpu.usage / k8s.pod.cpu_limit_utilization /
    jvm.cpu.recent_utilization                            → pod.DEGRADED + high_cpu
    k8s.pod.memory.working_set / k8s.pod.memory_limit_utilization
                                                          → pod.DEGRADED + high_memory
    k8s.pod.network.errors                                → pod.DEGRADED + network_errors
    k8s.pod.filesystem.usage / capacity ratio > 0.95      → pod.DEGRADED + disk_pressure
    k8s.container.restarts (Δ > 0 in window)              → container.UNAVAILABLE + crash_loop
    pod metrics absent in tail of abnormal window         → pod.UNAVAILABLE + pod_killed

Severity-aware merge happens in ``synth_timelines`` already; the adapter
just emits Transitions whenever a window's classification differs from the
previously emitted state on a node, plus a HEALTHY recovery transition when
no signal triggers.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass

import numpy as np

from rcabench_platform.v3.internal.reasoning.algorithms.baseline_detector import (
    BaselineAwareDetector,
    BaselineStatistics,
    compute_baseline_statistics,
)
from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph, Node, PlaceKind

DEFAULT_WINDOW_SEC = 5

CPU_METRICS = {
    "k8s.pod.cpu.usage",
    "k8s.pod.cpu_limit_utilization",
    "k8s.pod.cpu.node.utilization",
    "container.cpu.usage",
    "jvm.cpu.recent_utilization",
}
MEMORY_METRICS = {
    "k8s.pod.memory.working_set",
    "k8s.pod.memory_limit_utilization",
    "k8s.pod.memory.usage",
    "container.memory.working_set",
}
NETWORK_ERROR_METRICS = {"k8s.pod.network.errors"}
RESTART_METRICS = {"k8s.container.restarts"}
FS_USAGE_METRICS = {"k8s.pod.filesystem.usage", "container.filesystem.usage"}
FS_CAP_METRICS = {"k8s.pod.filesystem.available", "container.filesystem.available"}

DISK_PRESSURE_RATIO = 0.95


@dataclass(frozen=True, slots=True)
class _MetricClassification:
    state: str
    label: str
    trigger: str
    observed: float
    threshold: float


def _label_for(metric: str) -> tuple[str, str] | None:
    """Return (canonical_state, specialization_label) for a metric, or None."""
    if metric in CPU_METRICS:
        return "degraded", "high_cpu"
    if metric in MEMORY_METRICS:
        return "degraded", "high_memory"
    if metric in NETWORK_ERROR_METRICS:
        return "degraded", "network_errors"
    if metric in FS_USAGE_METRICS:
        return "degraded", "disk_pressure"
    if metric in RESTART_METRICS:
        return "unavailable", "crash_loop"
    return None


def _window_max(timestamps: np.ndarray, values: np.ndarray, t0: int, t1: int) -> float | None:
    if len(timestamps) == 0:
        return None
    mask = (timestamps >= t0) & (timestamps < t1)
    if not np.any(mask):
        return None
    sl = values[mask]
    sl = sl[~np.isnan(sl)]
    if len(sl) == 0:
        return None
    return float(np.max(sl))


class K8sMetricsAdapter:
    name = "k8s_metrics"

    def __init__(
        self,
        graph: HyperGraph,
        *,
        window_sec: int = DEFAULT_WINDOW_SEC,
    ) -> None:
        self._graph = graph
        self._window_sec = window_sec
        self._prev_restart_max: dict[str, float] = {}

    def emit(self, ctx: AdapterContext) -> Iterable[Transition]:
        return list(self._emit_all())

    def _emit_all(self) -> Iterable[Transition]:
        for kind in (PlaceKind.pod, PlaceKind.container):
            for node in self._graph.get_nodes_by_kind(kind):
                yield from self._emit_node(node)

    def _emit_node(self, node: Node) -> Iterable[Transition]:
        if not node.abnormal_metrics:
            return

        baseline_means: dict[str, BaselineStatistics] = {}
        for metric, (_, vals) in node.baseline_metrics.items():
            if vals is None or len(vals) == 0:
                continue
            baseline_means[metric] = compute_baseline_statistics(vals)
        detector = BaselineAwareDetector(baseline_means)

        ts_min: int | None = None
        ts_max: int | None = None
        for ts, _ in node.abnormal_metrics.values():
            if len(ts) == 0:
                continue
            lo = int(np.min(ts))
            hi = int(np.max(ts))
            ts_min = lo if ts_min is None or lo < ts_min else ts_min
            ts_max = hi if ts_max is None or hi > ts_max else ts_max
        if ts_min is None or ts_max is None:
            return

        window = self._window_sec
        node_key = node.uniq_name
        kind = node.kind
        last_state = "healthy"

        for w_start in range(ts_min, ts_max + 1, window):
            w_end = w_start + window
            classification = self._classify_window(node, detector, w_start, w_end)
            if classification is None:
                # Pod-killed detection: no metrics samples in tail window
                if kind == PlaceKind.pod and self._is_data_stop(node, w_start, w_end):
                    if last_state != "unavailable":
                        yield Transition(
                            node_key=node_key,
                            kind=kind,
                            at=w_start,
                            from_state=last_state,
                            to_state="unavailable",
                            trigger="data_stop",
                            level=EvidenceLevel.observed,
                            evidence={
                                "trigger_metric": "data_stop",
                                "observed": 0.0,
                                "threshold": 1.0,
                                "specialization_labels": frozenset({"pod_killed"}),
                            },
                        )
                        last_state = "unavailable"
                    continue
                if last_state != "healthy":
                    yield Transition(
                        node_key=node_key,
                        kind=kind,
                        at=w_start,
                        from_state=last_state,
                        to_state="healthy",
                        trigger="recovery",
                        level=EvidenceLevel.observed,
                        evidence={"trigger_metric": "recovery"},
                    )
                    last_state = "healthy"
                continue

            to_state = classification.state
            # container only has degraded / erroring / unavailable / healthy / unknown; map
            if kind == PlaceKind.container and to_state == "degraded":
                pass  # ContainerStateIR.DEGRADED exists
            if last_state != to_state:
                yield Transition(
                    node_key=node_key,
                    kind=kind,
                    at=w_start,
                    from_state=last_state,
                    to_state=to_state,
                    trigger=classification.trigger,
                    level=EvidenceLevel.observed,
                    evidence={
                        "trigger_metric": classification.trigger,
                        "observed": classification.observed,
                        "threshold": classification.threshold,
                        "specialization_labels": frozenset({classification.label}),
                    },
                )
                last_state = to_state

    def _classify_window(
        self,
        node: Node,
        detector: BaselineAwareDetector,
        t0: int,
        t1: int,
    ) -> _MetricClassification | None:
        # restart delta first — strongest signal. Compare current window max vs
        # the highest sample seen in any prior window for this node+metric.
        for metric in RESTART_METRICS:
            if metric not in node.abnormal_metrics:
                continue
            ts, vals = node.abnormal_metrics[metric]
            cur = _window_max(ts, vals, t0, t1)
            if cur is None:
                continue
            key = f"{node.uniq_name}::{metric}"
            prev = self._prev_restart_max.get(key, 0.0)
            self._prev_restart_max[key] = max(prev, cur)
            if cur > prev and cur > 0:
                return _MetricClassification(
                    state="unavailable",
                    label="crash_loop",
                    trigger=metric,
                    observed=float(cur),
                    threshold=float(prev),
                )

        # disk pressure: usage / (usage + available) > 0.95
        for usage_metric in FS_USAGE_METRICS:
            if usage_metric not in node.abnormal_metrics:
                continue
            ts_u, vals_u = node.abnormal_metrics[usage_metric]
            usage = _window_max(ts_u, vals_u, t0, t1)
            if usage is None:
                continue
            cap_metric = usage_metric.replace(".usage", ".available")
            if cap_metric not in node.abnormal_metrics:
                continue
            ts_a, vals_a = node.abnormal_metrics[cap_metric]
            avail = _window_max(ts_a, vals_a, t0, t1)
            if avail is None:
                continue
            denom = usage + avail
            if denom <= 0:
                continue
            ratio = usage / denom
            if ratio >= DISK_PRESSURE_RATIO:
                return _MetricClassification(
                    state="degraded",
                    label="disk_pressure",
                    trigger=usage_metric,
                    observed=float(ratio),
                    threshold=DISK_PRESSURE_RATIO,
                )

        # adaptive thresholds for cpu / memory / network
        for metric_set, label in (
            (CPU_METRICS, "high_cpu"),
            (MEMORY_METRICS, "high_memory"),
            (NETWORK_ERROR_METRICS, "network_errors"),
        ):
            for metric in metric_set:
                if metric not in node.abnormal_metrics:
                    continue
                ts, vals = node.abnormal_metrics[metric]
                v = _window_max(ts, vals, t0, t1)
                if v is None:
                    continue
                if detector.is_critical_anomaly(metric, v):
                    base = detector.baseline_stats.get(metric)
                    threshold = (base.mean + 3.0 * base.std) if base else 0.0
                    return _MetricClassification(
                        state="degraded",
                        label=label,
                        trigger=metric,
                        observed=float(v),
                        threshold=float(threshold),
                    )

        return None

    def _is_data_stop(self, node: Node, t0: int, t1: int) -> bool:
        for ts, _ in node.abnormal_metrics.values():
            if len(ts) == 0:
                continue
            mask = (ts >= t0) & (ts < t1)
            if np.any(mask):
                return False
        return True


__all__ = ["K8sMetricsAdapter"]


# Suppress unused-import on label_for helper for symmetry/extension.
_ = _label_for
