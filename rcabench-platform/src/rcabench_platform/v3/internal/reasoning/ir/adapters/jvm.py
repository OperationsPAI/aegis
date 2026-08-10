"""JvmAugmenterAdapter — JVM-specific specialization labels for pod/container nodes.

This is the first *specialization-adapter* example mandated by Phase 4 of
issue #163. The adapter inspects ``Node.abnormal_metrics`` for JVM
metrics (OpenTelemetry semantic-convention names under the ``jvm.*``
prefix) and emits Transitions that:

* keep using the canonical IR state vocabulary (``DEGRADED`` /
  ``UNAVAILABLE``); the augmenter never invents new states.
* attach JVM-specific specialization labels via
  ``Evidence.specialization_labels``.

The labels emitted here are the contract surface for augmentation rules
that gate via :pyattr:`PropagationRule.required_labels`:

* ``frequent_gc`` — sustained pauses on ``jvm.gc.duration``
  (or the legacy ``jvm.gc.collection.elapsed`` name): pod stays
  technically alive but spends large fractions of wall-clock in GC and
  user-visible spans go SLOW. Emits ``pod.DEGRADED + frequent_gc``.
* ``high_heap_pressure`` — heap utilisation
  (``jvm.memory.used / jvm.memory.limit`` or
  ``jvm.memory.committed`` proxy) sustained ≥ 0.9. Emits
  ``pod.DEGRADED + high_heap_pressure``.
* ``oom_killed`` — explicit OOM kill signal
  (``k8s.container.oom_killed`` or ``jvm.memory.oom``). Emits
  ``container.UNAVAILABLE + oom_killed``.

Boundary with :class:`K8sMetricsAdapter`:

* k8s_metrics already turns ``k8s.container.restarts`` deltas into
  ``container.UNAVAILABLE + crash_loop``. An OOM kill almost always
  shows up there too. The JVM augmenter's distinct contribution is
  *labelling the kill as OOM-driven specifically* via the ``oom_killed``
  label (the augmentation rule gates on that label rather than on a new
  state). Emitting alongside ``crash_loop`` is intentional: severity-
  aware merge in ``synth_timelines`` keeps the stronger state, and the
  union of specialization_labels survives the merge.
* k8s_metrics' ``high_memory`` covers k8s pod-level memory pressure
  (``k8s.pod.memory.working_set``); the JVM augmenter's
  ``high_heap_pressure`` covers JVM-internal heap pressure even when
  the container is well below its k8s memory limit (e.g. a pinned-down
  heap with frequent full GCs).

Real-data note: the metric names below follow the OTel ``jvm.*`` semantic
conventions. They are the names emitted by the OpenTelemetry Java agent
out of the box. Real-datapack validation is deferred until JVM-stack
fixtures are provided; this adapter is a no-op when the relevant metrics
aren't populated, so wiring it unconditionally is safe.
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

# OTel JVM metrics (semantic-convention names; older names kept for compatibility
# with collectors that haven't migrated). When neither name is present on a
# node the adapter no-ops — it is safe to wire on non-Java stacks.
GC_DURATION_METRICS = frozenset(
    {
        "jvm.gc.duration",  # OTel current
        "jvm.gc.collection.elapsed",  # OTel deprecated, legacy collectors
        "process.runtime.jvm.gc.duration",  # OTel pre-1.0 prefix
    }
)
HEAP_USED_METRICS = frozenset(
    {
        "jvm.memory.used",
        "process.runtime.jvm.memory.usage",
    }
)
HEAP_LIMIT_METRICS = frozenset(
    {
        "jvm.memory.limit",
        "jvm.memory.max",
        "process.runtime.jvm.memory.limit",
    }
)
HEAP_COMMITTED_METRICS = frozenset(
    {
        "jvm.memory.committed",
        "process.runtime.jvm.memory.committed",
    }
)
OOM_METRICS = frozenset(
    {
        "k8s.container.oom_killed",
        "jvm.memory.oom",
        "process.runtime.jvm.memory.oom",
    }
)

# Heap utilisation threshold — sustained at-or-above this ratio counts as
# ``high_heap_pressure``. Picked at 0.90 so the JVM in steady-state full-heap
# operation (commonly 0.7–0.85 of -Xmx) is *not* tagged.
HEAP_PRESSURE_RATIO = 0.90


@dataclass(frozen=True, slots=True)
class _JvmClassification:
    state: str
    label: str
    trigger: str
    observed: float
    threshold: float


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


def _node_has_any_metric(node: Node, metrics: Iterable[str]) -> bool:
    return any(m in node.abnormal_metrics for m in metrics)


def _node_has_any_jvm_signal(node: Node) -> bool:
    """Cheap precondition: only do work for nodes that actually have JVM data."""
    return (
        _node_has_any_metric(node, GC_DURATION_METRICS)
        or _node_has_any_metric(node, HEAP_USED_METRICS)
        or _node_has_any_metric(node, HEAP_COMMITTED_METRICS)
        or _node_has_any_metric(node, OOM_METRICS)
    )


class JvmAugmenterAdapter:
    """Specialization adapter that emits JVM-flavoured specialization labels.

    Pattern-aligned with :class:`K8sMetricsAdapter`: per-metric Z-score
    detection against the baseline window for adaptive thresholds, plus a
    static heap-utilisation threshold (heap pressure is a level signal,
    not a deviation).
    """

    name = "jvm_augmenter"

    def __init__(
        self,
        graph: HyperGraph,
        *,
        window_sec: int = DEFAULT_WINDOW_SEC,
    ) -> None:
        self._graph = graph
        self._window_sec = window_sec
        self._prev_oom_max: dict[str, float] = {}

    def emit(self, ctx: AdapterContext) -> Iterable[Transition]:
        return list(self._emit_all())

    def _emit_all(self) -> Iterable[Transition]:
        for kind in (PlaceKind.pod, PlaceKind.container):
            for node in self._graph.get_nodes_by_kind(kind):
                yield from self._emit_node(node)

    def _emit_node(self, node: Node) -> Iterable[Transition]:
        if not node.abnormal_metrics:
            return
        if not _node_has_any_jvm_signal(node):
            return

        baseline_stats: dict[str, BaselineStatistics] = {}
        for metric, (_, vals) in node.baseline_metrics.items():
            if vals is None or len(vals) == 0:
                continue
            baseline_stats[metric] = compute_baseline_statistics(vals)
        detector = BaselineAwareDetector(baseline_stats)

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
                if last_state != "healthy":
                    # Recovery is owned by k8s_metrics; the JVM augmenter only
                    # adds *positive* signals so it never overwrites someone
                    # else's degraded -> something with healthy.
                    pass
                continue

            to_state = classification.state
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
    ) -> _JvmClassification | None:
        kind = node.kind

        # OOM kill — strongest signal. Emit on a delta>0 over a previously
        # observed sample for this metric+node, mirroring how k8s_metrics
        # treats container restarts.
        if kind == PlaceKind.container:
            for metric in OOM_METRICS:
                if metric not in node.abnormal_metrics:
                    continue
                ts, vals = node.abnormal_metrics[metric]
                cur = _window_max(ts, vals, t0, t1)
                if cur is None:
                    continue
                key = f"{node.uniq_name}::{metric}"
                prev = self._prev_oom_max.get(key, 0.0)
                self._prev_oom_max[key] = max(prev, cur)
                if cur > prev and cur > 0:
                    return _JvmClassification(
                        state="unavailable",
                        label="oom_killed",
                        trigger=metric,
                        observed=float(cur),
                        threshold=float(prev),
                    )

        # frequent_gc — only emitted on pod nodes (where rules attach).
        # Z-score anomaly on GC duration relative to baseline. JVMs always
        # GC; the signal is "much more than usual".
        if kind == PlaceKind.pod:
            for metric in GC_DURATION_METRICS:
                if metric not in node.abnormal_metrics:
                    continue
                ts, vals = node.abnormal_metrics[metric]
                v = _window_max(ts, vals, t0, t1)
                if v is None:
                    continue
                if detector.is_critical_anomaly(metric, v):
                    base = detector.baseline_stats.get(metric)
                    threshold = (base.mean + 3.0 * base.std) if base else 0.0
                    return _JvmClassification(
                        state="degraded",
                        label="frequent_gc",
                        trigger=metric,
                        observed=float(v),
                        threshold=float(threshold),
                    )

            # high_heap_pressure — used / limit (preferred) or
            # used / committed (fallback). Static ratio threshold.
            heap_used_metric = next((m for m in HEAP_USED_METRICS if m in node.abnormal_metrics), None)
            if heap_used_metric is not None:
                ts_u, vals_u = node.abnormal_metrics[heap_used_metric]
                used = _window_max(ts_u, vals_u, t0, t1)
                if used is not None:
                    cap_metric = next(
                        (m for m in HEAP_LIMIT_METRICS if m in node.abnormal_metrics),
                        None,
                    ) or next(
                        (m for m in HEAP_COMMITTED_METRICS if m in node.abnormal_metrics),
                        None,
                    )
                    if cap_metric is not None:
                        ts_c, vals_c = node.abnormal_metrics[cap_metric]
                        cap = _window_max(ts_c, vals_c, t0, t1)
                        if cap is not None and cap > 0:
                            ratio = used / cap
                            if ratio >= HEAP_PRESSURE_RATIO:
                                return _JvmClassification(
                                    state="degraded",
                                    label="high_heap_pressure",
                                    trigger=heap_used_metric,
                                    observed=float(ratio),
                                    threshold=HEAP_PRESSURE_RATIO,
                                )
        return None


__all__ = ["JvmAugmenterAdapter"]
