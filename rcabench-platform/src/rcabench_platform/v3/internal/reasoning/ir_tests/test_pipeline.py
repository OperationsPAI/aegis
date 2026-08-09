"""End-to-end IR pipeline: injection + traces + k8s_metrics adapters."""

from __future__ import annotations

from pathlib import Path

import numpy as np
import polars as pl

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.pipeline import run_reasoning_ir
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph, Node, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import ResolvedInjection

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="pipeline-fixture")


def _trace_row(ts: int, svc: str, span: str, dur_ns: int, status: int | None) -> dict:
    return {
        "time": ts,
        "trace_id": f"trace-{ts}-{span}",
        "span_id": f"sp-{ts}-{span}",
        "parent_span_id": "",
        "span_name": span,
        "service_name": svc,
        "duration": dur_ns,
        "attr.http.response.status_code": status,
    }


def test_pipeline_combines_three_adapters() -> None:
    g = HyperGraph()
    pod = g.add_node(Node(kind=PlaceKind.pod, self_name="users-0"))
    base_ts = np.arange(1000, 1060, dtype=np.int64)
    pod.baseline_metrics["k8s.pod.cpu.usage"] = (base_ts, np.full(60, 0.10))
    abn_ts = np.arange(2000, 2030, dtype=np.int64)
    abn_vals = np.concatenate([np.full(10, 0.10), np.full(20, 0.95)])
    pod.abnormal_metrics["k8s.pod.cpu.usage"] = (abn_ts, abn_vals)

    base_rows = [_trace_row(1000 + i, "front-end", "GET /login", 100_000_000, 200) for i in range(40)]
    abn_rows = [_trace_row(2003 + i % 3, "front-end", "GET /login", 5_000_000_000, 200) for i in range(40)]
    base_df = pl.DataFrame(base_rows)
    abn_df = pl.DataFrame(abn_rows)

    resolved = ResolvedInjection(
        injection_nodes=["pod|users-0"],
        start_kind="pod",
        category="pod_lifecycle",
        fault_category="pod",
        fault_type_name="PodFailure",
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

    assert "pod|users-0" in timelines
    pod_tl = timelines["pod|users-0"]
    assert pod_tl.kind == PlaceKind.pod
    pod_states = {w.state for w in pod_tl.windows}
    assert "unavailable" in pod_states  # from injection seed

    span_key = "span|front-end::GET /login"
    assert span_key in timelines
    span_tl = timelines[span_key]
    span_states = {w.state for w in span_tl.windows}
    assert "slow" in span_states


def test_pipeline_empty_inputs_produces_only_seed() -> None:
    g = HyperGraph()
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
    resolved = ResolvedInjection(
        injection_nodes=["span|front-end::GET /api"],
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
    assert "span|front-end::GET /api" in timelines
    states = {w.state for w in timelines["span|front-end::GET /api"].windows}
    assert "slow" in states
