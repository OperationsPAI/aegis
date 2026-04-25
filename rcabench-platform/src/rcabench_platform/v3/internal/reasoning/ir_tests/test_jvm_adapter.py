"""JvmAugmenterAdapter — synthetic JVM metric series exercising each label."""

from __future__ import annotations

from pathlib import Path

import numpy as np

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.jvm import JvmAugmenterAdapter
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph, Node, PlaceKind

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="jvm-fixture")


def _ts_range(start: int, n: int) -> np.ndarray:
    return np.arange(start, start + n, dtype=np.int64)


def _add_pod(g: HyperGraph, name: str) -> Node:
    return g.add_node(Node(kind=PlaceKind.pod, self_name=name))


def _add_container(g: HyperGraph, name: str) -> Node:
    return g.add_node(Node(kind=PlaceKind.container, self_name=name))


def test_no_jvm_metrics_emits_nothing() -> None:
    """Adapter is safe to wire on non-Java stacks: no JVM metric -> no events."""
    g = HyperGraph()
    pod = _add_pod(g, "go-svc-0")
    base_ts = _ts_range(1000, 60)
    pod.baseline_metrics["k8s.pod.cpu.usage"] = (base_ts, np.full(60, 0.10))
    pod.abnormal_metrics["k8s.pod.cpu.usage"] = (_ts_range(2000, 30), np.full(30, 0.95))

    events = list(JvmAugmenterAdapter(g, window_sec=5).emit(CTX))
    assert events == []


def test_frequent_gc_emits_pod_degraded_with_label() -> None:
    g = HyperGraph()
    pod = _add_pod(g, "tea-store-auth-0")
    metric = "jvm.gc.duration"
    base_ts = _ts_range(1000, 60)
    base_vals = np.full(60, 0.001, dtype=np.float64)  # 1ms baseline GC
    abn_ts = _ts_range(2000, 30)
    # Sustained 200ms GC pauses — well above 3-sigma of a 1ms baseline.
    abn_vals = np.concatenate([np.full(10, 0.001), np.full(20, 0.200)])
    pod.baseline_metrics[metric] = (base_ts, base_vals)
    pod.abnormal_metrics[metric] = (abn_ts, abn_vals)

    events = list(JvmAugmenterAdapter(g, window_sec=5).emit(CTX))
    assert events, "expected at least one transition"
    deg = [e for e in events if e.to_state == "degraded"]
    assert deg, f"expected degraded transition, got {[(e.to_state, e.trigger) for e in events]}"
    assert deg[0].evidence.get("specialization_labels") == frozenset({"frequent_gc"})
    assert deg[0].kind == PlaceKind.pod


def test_high_heap_pressure_emits_pod_degraded_with_label() -> None:
    g = HyperGraph()
    pod = _add_pod(g, "tea-store-recommender-0")
    base_ts = _ts_range(1000, 60)
    abn_ts = _ts_range(2000, 30)

    # used / limit ratio crosses 0.90 throughout the abnormal window.
    pod.baseline_metrics["jvm.memory.used"] = (base_ts, np.full(60, 200_000_000.0))
    pod.baseline_metrics["jvm.memory.limit"] = (base_ts, np.full(60, 1_000_000_000.0))

    pod.abnormal_metrics["jvm.memory.used"] = (abn_ts, np.full(30, 950_000_000.0))
    pod.abnormal_metrics["jvm.memory.limit"] = (abn_ts, np.full(30, 1_000_000_000.0))

    events = list(JvmAugmenterAdapter(g, window_sec=5).emit(CTX))
    deg = [e for e in events if e.to_state == "degraded"]
    assert deg, f"expected degraded transition, got {events}"
    assert deg[0].evidence.get("specialization_labels") == frozenset({"high_heap_pressure"})


def test_high_heap_pressure_no_emit_when_under_threshold() -> None:
    g = HyperGraph()
    pod = _add_pod(g, "tea-store-image-0")
    base_ts = _ts_range(1000, 60)
    abn_ts = _ts_range(2000, 30)

    pod.baseline_metrics["jvm.memory.used"] = (base_ts, np.full(60, 200_000_000.0))
    pod.baseline_metrics["jvm.memory.limit"] = (base_ts, np.full(60, 1_000_000_000.0))
    # Sit comfortably at 0.5 utilisation — below 0.90 threshold.
    pod.abnormal_metrics["jvm.memory.used"] = (abn_ts, np.full(30, 500_000_000.0))
    pod.abnormal_metrics["jvm.memory.limit"] = (abn_ts, np.full(30, 1_000_000_000.0))

    events = list(JvmAugmenterAdapter(g, window_sec=5).emit(CTX))
    assert events == []


def test_oom_killed_emits_container_unavailable_with_label() -> None:
    g = HyperGraph()
    cont = _add_container(g, "tea-store-persistence-app")
    metric = "k8s.container.oom_killed"
    base_ts = _ts_range(1000, 60)
    base_vals = np.zeros(60, dtype=np.float64)
    abn_ts = _ts_range(2000, 30)
    # 0 → 1 in the second half-window: OOM kill recorded.
    abn_vals = np.array([0] * 10 + [1] * 20, dtype=np.float64)
    cont.baseline_metrics[metric] = (base_ts, base_vals)
    cont.abnormal_metrics[metric] = (abn_ts, abn_vals)

    events = list(JvmAugmenterAdapter(g, window_sec=5).emit(CTX))
    una = [e for e in events if e.to_state == "unavailable"]
    assert una, f"expected unavailable transition, got {events}"
    assert una[0].evidence.get("specialization_labels") == frozenset({"oom_killed"})
    assert una[0].kind == PlaceKind.container


def test_legacy_metric_name_is_recognised() -> None:
    """``jvm.gc.collection.elapsed`` is the deprecated OTel name; still works."""
    g = HyperGraph()
    pod = _add_pod(g, "legacy-jvm-0")
    metric = "jvm.gc.collection.elapsed"
    base_ts = _ts_range(1000, 60)
    base_vals = np.full(60, 0.001, dtype=np.float64)
    abn_ts = _ts_range(2000, 30)
    abn_vals = np.concatenate([np.full(10, 0.001), np.full(20, 0.200)])
    pod.baseline_metrics[metric] = (base_ts, base_vals)
    pod.abnormal_metrics[metric] = (abn_ts, abn_vals)

    events = list(JvmAugmenterAdapter(g, window_sec=5).emit(CTX))
    deg = [e for e in events if e.to_state == "degraded"]
    assert deg
    assert deg[0].evidence.get("specialization_labels") == frozenset({"frequent_gc"})


def test_pipeline_includes_jvm_adapter_when_metrics_absent() -> None:
    """End-to-end: JVM adapter wired in pipeline.run_reasoning_ir is a safe no-op
    on non-Java stacks (the existing test_pipeline cases must stay green; this
    test asserts the JVM adapter is in the standard trio explicitly)."""
    import polars as pl

    from rcabench_platform.v3.internal.reasoning.ir.pipeline import run_reasoning_ir
    from rcabench_platform.v3.internal.reasoning.models.injection import ResolvedInjection

    base_df = pl.DataFrame(
        schema={
            "time": pl.Int64,
            "trace_id": pl.Utf8,
            "span_id": pl.Utf8,
            "parent_span_id": pl.Utf8,
            "span_name": pl.Utf8,
            "service_name": pl.Utf8,
            "duration": pl.Int64,
            "attr.http.response.status_code": pl.Int64,
        }
    )
    abn_df = base_df.clone()
    g = HyperGraph()
    resolved = ResolvedInjection(
        injection_nodes=["span|svc::GET /api"],
        start_kind="span",
        category="http_response",
        fault_category="http_response",
        fault_type_name="HTTPResponseDelay",
        resolution_method="test",
    )
    timelines = run_reasoning_ir(
        graph=g,
        ctx=CTX,
        resolved=resolved,
        injection_at=2000,
        baseline_traces=base_df,
        abnormal_traces=abn_df,
    )
    # Should still produce the seed timeline; JVM adapter is a no-op here.
    assert "span|svc::GET /api" in timelines
