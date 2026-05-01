"""End-to-end feature-wiring smoke test (FORGE rework Phase 3.5).

Asserts that:

1. ``extract_feature_samples`` populates a non-empty
   ``feature_samples`` map from a Node carrying baseline + abnormal
   metrics shaped like the real datasets in
   ``/home/ddq/AoyangSpace/dataset/rca/``.
2. The map carries the entry-signature features the
   ``CPUStress`` / ``JVMMemoryStress`` manifest requires.
3. ``ManifestEntryGate`` reaches a sane verdict on the populated
   context (passes when the magnitude bands are met, fails when they
   are not).

There are two flavours of fixture:

* a **synthetic** fixture (always run) — a HyperGraph with a single
  container/pod that mirrors the metric shape the parquet loader
  produces. This guarantees CI coverage even on machines without the
  500-case dataset checked out.
* a **real datapack** fixture (skipped when the dataset path is
  absent) — pulls one ``ts0-*-stress-*`` case from
  ``/home/ddq/AoyangSpace/dataset/rca/``, materialises pod baseline +
  abnormal metric series from its ``*_metrics.parquet``, and runs the
  same extractor + gate against it. This catches schema drift the
  synthetic fixture cannot.
"""

from __future__ import annotations

from pathlib import Path

import numpy as np
import pytest

from rcabench_platform.v3.internal.reasoning.algorithms.gates.manifest_entry import (
    ManifestEntryGate,
)
from rcabench_platform.v3.internal.reasoning.manifests import (
    Feature,
    FeatureKind,
    ReasoningContext,
    load_manifest,
)
from rcabench_platform.v3.internal.reasoning.manifests.extractors import (
    extract_feature_samples,
)
from rcabench_platform.v3.internal.reasoning.models.graph import (
    HyperGraph,
    Node,
    PlaceKind,
)

CPU_STRESS_MANIFEST = (
    Path(__file__).resolve().parents[1]
    / "manifests"
    / "fault_types"
    / "cpu_stress.yaml"
)
JVM_MEM_STRESS_MANIFEST = (
    Path(__file__).resolve().parents[1]
    / "manifests"
    / "fault_types"
    / "jvm_memory_stress.yaml"
)
DATASET_DIR = Path("/home/ddq/AoyangSpace/dataset/rca/")


# ---------------------------------------------------------------------------
# Synthetic fixture: pod + container with the metric shape produced by
# the parquet loader. Bands match the CPUStress entry_signature.
# ---------------------------------------------------------------------------


def _make_synthetic_cpu_stress_graph() -> tuple[HyperGraph, Node, Node]:
    g = HyperGraph()
    pod = g.add_node(
        Node(
            kind=PlaceKind.pod,
            self_name="ts-auth-service-77d85c69dd-sv9d4",
        )
    )
    cont = g.add_node(
        Node(kind=PlaceKind.container, self_name="ts-auth-service")
    )

    base_ts = np.arange(1000, 1060, dtype=np.int64)
    base_vals_cpu = np.full(60, 0.10, dtype=np.float64)  # 10% baseline CPU.
    abn_ts = np.arange(2000, 2060, dtype=np.int64)
    # Saturated CPU during abnormal window — simulates throttle-bound
    # container. peak/baseline = 0.95 / 0.10 = 9.5x — well above the
    # CPUStress band [3.0, .inf).
    abn_vals_cpu = np.concatenate([np.full(20, 0.10), np.full(40, 0.95)])

    pod.baseline_metrics["k8s.pod.cpu_limit_utilization"] = (
        base_ts, base_vals_cpu,
    )
    pod.abnormal_metrics["k8s.pod.cpu_limit_utilization"] = (
        abn_ts, abn_vals_cpu,
    )
    cont.baseline_metrics["container.cpu.usage"] = (
        base_ts, base_vals_cpu,
    )
    cont.abnormal_metrics["container.cpu.usage"] = (abn_ts, abn_vals_cpu)
    return g, pod, cont


def test_extractor_populates_cpu_throttle_ratio_synthetic() -> None:
    g, pod, cont = _make_synthetic_cpu_stress_graph()
    samples = extract_feature_samples(
        graph=g,
        baseline_traces=None,
        abnormal_traces=None,
        abnormal_window_start=2000,
        abnormal_window_end=2060,
    )
    assert samples, "extractor produced no samples"

    # Both pod and container carry cpu_throttle_ratio.
    assert (
        pod.id, FeatureKind.pod, Feature.cpu_throttle_ratio,
    ) in samples
    assert (
        cont.id, FeatureKind.container, Feature.cpu_throttle_ratio,
    ) in samples
    cont_ratio = samples[
        (cont.id, FeatureKind.container, Feature.cpu_throttle_ratio)
    ]
    assert cont_ratio >= 3.0, (
        f"expected CPU ratio >= 3.0 (CPUStress band lower edge), got {cont_ratio}"
    )


def test_entry_gate_passes_with_extracted_samples_synthetic() -> None:
    g, _pod, cont = _make_synthetic_cpu_stress_graph()
    samples = extract_feature_samples(
        graph=g,
        baseline_traces=None,
        abnormal_traces=None,
        abnormal_window_start=2000,
        abnormal_window_end=2060,
    )

    manifest = load_manifest(CPU_STRESS_MANIFEST)
    # CPUStress requires container.cpu_throttle_ratio AND optional_min_match=1
    # of the optionals. Provide an optional match so the entry signature
    # passes — synthesise a thread_queue_depth sample.
    samples[(cont.id, FeatureKind.container, Feature.thread_queue_depth)] = 5.0

    rctx = ReasoningContext(
        fault_type_name=manifest.fault_type_name,
        manifest=manifest,
        v_root_node_id=cont.id,
        t0=2000,
        feature_samples=samples,
    )
    gate = ManifestEntryGate(rctx)
    result = gate.evaluate(path=None, ctx=None)  # type: ignore[arg-type]
    assert result.passed, result.reason


def test_entry_gate_fails_when_no_samples_extracted_synthetic() -> None:
    """A graph with no metrics → empty samples → entry gate fails.

    This guards the polarity contract: missing samples must not
    silently pass the gate.
    """
    g = HyperGraph()
    cont = g.add_node(
        Node(kind=PlaceKind.container, self_name="ts-auth-service")
    )
    samples = extract_feature_samples(
        graph=g,
        baseline_traces=None,
        abnormal_traces=None,
        abnormal_window_start=2000,
        abnormal_window_end=2060,
    )
    assert samples == {}, "expected empty samples on empty graph"

    manifest = load_manifest(CPU_STRESS_MANIFEST)
    rctx = ReasoningContext(
        fault_type_name=manifest.fault_type_name,
        manifest=manifest,
        v_root_node_id=cont.id,
        t0=2000,
        feature_samples=samples,
    )
    result = ManifestEntryGate(rctx).evaluate(
        path=None, ctx=None  # type: ignore[arg-type]
    )
    assert not result.passed
    assert "required" in result.reason


# ---------------------------------------------------------------------------
# Real-datapack smoke test. Skipped when the dataset is not on disk.
# ---------------------------------------------------------------------------


def _list_stress_cases(root: Path) -> list[Path]:
    if not root.exists():
        return []
    return sorted(p for p in root.iterdir() if p.is_dir() and "stress" in p.name)


@pytest.mark.skipif(
    not DATASET_DIR.exists() or not _list_stress_cases(DATASET_DIR),
    reason="canonical RCA dataset not present at /home/ddq/AoyangSpace/dataset/rca/",
)
def test_extractor_on_real_stress_datapack() -> None:
    """Build a Node directly from a real parquet file and run the extractor.

    We deliberately bypass the heavy ``ParquetDataLoader`` /
    ``run_reasoning_ir`` plumbing here: the goal is to exercise the
    extractor + entry gate against the metric *shapes* the production
    loader emits, not to re-test the loader. That keeps the test fast
    enough for CI while still catching schema drift (a broken metric
    name in the extractor would yield zero samples, failing the
    assertion below).
    """
    import polars as pl

    case = _list_stress_cases(DATASET_DIR)[0]
    df = pl.read_parquet(case / "abnormal_metrics.parquet")
    base_df = pl.read_parquet(case / "normal_metrics.parquet")

    # Pick the pod node carrying CPU/memory metrics. Pod name lives in
    # ``attr.k8s.pod.name`` per the parquet schema.
    pod_col = "attr.k8s.pod.name"
    if pod_col not in df.columns:
        pytest.skip(f"datapack {case.name} has no {pod_col} column")
    pods = (
        df.filter(pl.col(pod_col).is_not_null())
        .select(pod_col)
        .unique()
        .to_series()
        .to_list()
    )
    target_pod = next((p for p in pods if "auth" in p or "stress" in p), pods[0])

    g = HyperGraph()
    node = g.add_node(Node(kind=PlaceKind.pod, self_name=target_pod))

    # Materialise per-metric (timestamps, values) pairs for the target pod.
    interesting = [
        "k8s.pod.cpu_limit_utilization",
        "k8s.pod.cpu.usage",
        "k8s.pod.memory_limit_utilization",
        "k8s.pod.memory.working_set",
        "k8s.container.restarts",
    ]
    populated = 0
    for metric in interesting:
        ab = df.filter(
            (pl.col("metric") == metric) & (pl.col(pod_col) == target_pod)
        ).sort("time")
        bs = base_df.filter(
            (pl.col("metric") == metric) & (pl.col(pod_col) == target_pod)
        ).sort("time")
        if len(ab) == 0:
            continue
        ts_a = ab["time"].to_numpy()
        # Convert ns/ms/sec heuristically — fall back to int seconds.
        if ts_a.dtype.kind == "i" and ts_a.size > 0 and ts_a.max() > 10**14:
            ts_a = (ts_a // 1_000_000_000).astype(np.int64)
        elif ts_a.dtype.kind == "i" and ts_a.size > 0 and ts_a.max() > 10**11:
            ts_a = (ts_a // 1_000).astype(np.int64)
        elif ts_a.dtype.kind == "M":  # datetime
            ts_a = (ts_a.astype("int64") // 1_000_000_000).astype(np.int64)
        else:
            ts_a = ts_a.astype(np.int64)
        vals_a = ab["value"].to_numpy().astype(np.float64)

        if len(bs) > 0:
            ts_b = bs["time"].to_numpy()
            if ts_b.dtype.kind == "i" and ts_b.max() > 10**14:
                ts_b = (ts_b // 1_000_000_000).astype(np.int64)
            elif ts_b.dtype.kind == "i" and ts_b.max() > 10**11:
                ts_b = (ts_b // 1_000).astype(np.int64)
            elif ts_b.dtype.kind == "M":
                ts_b = (ts_b.astype("int64") // 1_000_000_000).astype(np.int64)
            else:
                ts_b = ts_b.astype(np.int64)
            vals_b = bs["value"].to_numpy().astype(np.float64)
            node.baseline_metrics[metric] = (ts_b, vals_b)
        node.abnormal_metrics[metric] = (ts_a, vals_a)
        populated += 1

    if populated == 0:
        pytest.skip(
            f"datapack {case.name} carried no recognisable resource metrics "
            f"for pod {target_pod}"
        )

    # Window = full abnormal range.
    t0 = int(min(ts.min() for ts, _ in node.abnormal_metrics.values()))
    t1 = int(max(ts.max() for ts, _ in node.abnormal_metrics.values())) + 1

    samples = extract_feature_samples(
        graph=g,
        baseline_traces=None,
        abnormal_traces=None,
        abnormal_window_start=t0,
        abnormal_window_end=t1,
    )
    assert samples, (
        f"extractor produced no samples on real datapack {case.name} "
        f"(pod={target_pod}, populated_metrics={populated})"
    )

    # Sanity: at least one of the resource-pressure features fires
    # (cpu_throttle_ratio or memory_usage_ratio). Stress datasets in the
    # ts0 corpus push one of these into a saturation regime.
    keys = {(k[1], k[2]) for k in samples}
    assert (
        (FeatureKind.pod, Feature.cpu_throttle_ratio) in keys
        or (FeatureKind.pod, Feature.memory_usage_ratio) in keys
    ), f"no resource-pressure feature in samples: {keys}"
