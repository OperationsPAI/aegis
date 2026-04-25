"""K8sMetricsAdapter: synthetic numpy series on a HyperGraph node."""

from __future__ import annotations

from pathlib import Path

import numpy as np

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.k8s_metrics import K8sMetricsAdapter
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph, Node, PlaceKind

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="metrics-fixture")


def _add_pod(graph: HyperGraph, name: str) -> Node:
    n = Node(kind=PlaceKind.pod, self_name=name)
    return graph.add_node(n)


def _ts_range(start: int, n: int) -> np.ndarray:
    return np.arange(start, start + n, dtype=np.int64)


def test_high_cpu_emits_pod_degraded() -> None:
    g = HyperGraph()
    pod = _add_pod(g, "ts-order-7b4-xxx")
    base_ts = _ts_range(1000, 60)
    base_vals = np.full(60, 0.10, dtype=np.float64)
    abn_ts = _ts_range(2000, 30)
    abn_vals = np.concatenate([np.full(10, 0.10), np.full(20, 0.95)])
    pod.baseline_metrics["k8s.pod.cpu.usage"] = (base_ts, base_vals)
    pod.abnormal_metrics["k8s.pod.cpu.usage"] = (abn_ts, abn_vals)

    adapter = K8sMetricsAdapter(g, window_sec=5)
    events = list(adapter.emit(CTX))
    assert events
    deg = [e for e in events if e.to_state == "degraded"]
    assert deg
    assert deg[0].evidence.get("specialization_labels") == frozenset({"high_cpu"})


def test_high_memory_emits_pod_degraded() -> None:
    g = HyperGraph()
    pod = _add_pod(g, "ts-order-mem")
    base_ts = _ts_range(1000, 60)
    base_vals = np.full(60, 200_000_000.0)
    abn_ts = _ts_range(2000, 30)
    abn_vals = np.concatenate([np.full(10, 200_000_000.0), np.full(20, 1_500_000_000.0)])
    pod.baseline_metrics["k8s.pod.memory.working_set"] = (base_ts, base_vals)
    pod.abnormal_metrics["k8s.pod.memory.working_set"] = (abn_ts, abn_vals)

    events = list(K8sMetricsAdapter(g, window_sec=5).emit(CTX))
    deg = [e for e in events if e.to_state == "degraded"]
    assert deg
    assert deg[0].evidence.get("specialization_labels") == frozenset({"high_memory"})


def test_container_restart_emits_unavailable() -> None:
    g = HyperGraph()
    cont = g.add_node(Node(kind=PlaceKind.container, self_name="coherence"))
    base_ts = _ts_range(1000, 60)
    base_vals = np.zeros(60, dtype=np.float64)
    abn_ts = _ts_range(2000, 30)
    abn_vals = np.array([0] * 10 + [1] * 5 + [3] * 15, dtype=np.float64)
    cont.baseline_metrics["k8s.container.restarts"] = (base_ts, base_vals)
    cont.abnormal_metrics["k8s.container.restarts"] = (abn_ts, abn_vals)

    events = list(K8sMetricsAdapter(g, window_sec=5).emit(CTX))
    una = [e for e in events if e.to_state == "unavailable"]
    assert una
    assert una[0].evidence.get("specialization_labels") == frozenset({"crash_loop"})


def test_pod_data_stop_emits_unavailable() -> None:
    g = HyperGraph()
    pod = _add_pod(g, "ts-killed-pod")
    base_ts = _ts_range(1000, 60)
    base_vals = np.full(60, 0.10, dtype=np.float64)
    abn_ts = _ts_range(2000, 10)  # only first 10 sec, rest of window has nothing
    abn_vals = np.full(10, 0.10, dtype=np.float64)
    pod.baseline_metrics["k8s.pod.cpu.usage"] = (base_ts, base_vals)
    pod.abnormal_metrics["k8s.pod.cpu.usage"] = (abn_ts, abn_vals)
    # Force the window range to extend past data by adding a second metric with broader ts
    pod.abnormal_metrics["_marker"] = (np.array([2000, 2050], dtype=np.int64), np.array([0.0, 0.0]))

    events = list(K8sMetricsAdapter(g, window_sec=5).emit(CTX))
    killed = [
        e
        for e in events
        if e.to_state == "unavailable" and "pod_killed" in e.evidence.get("specialization_labels", frozenset())
    ]
    assert killed, events


def test_recovery_returns_to_healthy() -> None:
    g = HyperGraph()
    pod = _add_pod(g, "ts-recover")
    base_ts = _ts_range(1000, 60)
    base_vals = np.full(60, 0.10, dtype=np.float64)
    abn_ts = _ts_range(2000, 30)
    abn_vals = np.concatenate([np.full(10, 0.95), np.full(20, 0.10)])
    pod.baseline_metrics["k8s.pod.cpu.usage"] = (base_ts, base_vals)
    pod.abnormal_metrics["k8s.pod.cpu.usage"] = (abn_ts, abn_vals)

    events = list(K8sMetricsAdapter(g, window_sec=5).emit(CTX))
    states = [e.to_state for e in events]
    assert "degraded" in states
    assert "healthy" in states
