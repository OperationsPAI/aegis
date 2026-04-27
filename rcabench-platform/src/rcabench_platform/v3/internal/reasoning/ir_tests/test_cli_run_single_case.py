"""cli.run_single_case happy path on a synthetic graph + mocked loader.

End-to-end real-datapack testing is owner-driven follow-up; this test
exercises the CLI's wiring of ``run_reasoning_ir`` into the propagator
without touching parquet on disk.
"""

from __future__ import annotations

from functools import partial
from pathlib import Path
from typing import Any

import numpy as np
import polars as pl

from rcabench_platform.v3.internal.reasoning import cli as reasoning_cli
from rcabench_platform.v3.internal.reasoning.ir.pipeline import run_reasoning_ir as _real_run_reasoning_ir
from rcabench_platform.v3.internal.reasoning.models.graph import (
    CallsEdgeData,
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)


def _make_graph_with_calls_chain() -> tuple[HyperGraph, dict[str, int]]:
    g = HyperGraph()
    callee_svc = g.add_node(Node(kind=PlaceKind.service, self_name="svc-callee"))
    caller_svc = g.add_node(Node(kind=PlaceKind.service, self_name="svc-caller"))
    callee_span = g.add_node(Node(kind=PlaceKind.span, self_name="svc-callee::POST /api"))
    caller_span = g.add_node(Node(kind=PlaceKind.span, self_name="svc-caller::GET /home"))

    ids = {n.uniq_name: n.id for n in (callee_svc, caller_svc, callee_span, caller_span) if n.id is not None}

    g.add_edge(
        Edge(
            src_id=ids["service|svc-callee"],
            dst_id=ids["span|svc-callee::POST /api"],
            src_name="service|svc-callee",
            dst_name="span|svc-callee::POST /api",
            kind=DepKind.includes,
            data=None,
        )
    )
    g.add_edge(
        Edge(
            src_id=ids["service|svc-caller"],
            dst_id=ids["span|svc-caller::GET /home"],
            src_name="service|svc-caller",
            dst_name="span|svc-caller::GET /home",
            kind=DepKind.includes,
            data=None,
        )
    )
    g.add_edge(
        Edge(
            src_id=ids["span|svc-caller::GET /home"],
            dst_id=ids["span|svc-callee::POST /api"],
            src_name="span|svc-caller::GET /home",
            dst_name="span|svc-callee::POST /api",
            kind=DepKind.calls,
            data=CallsEdgeData(),
        )
    )
    return g, ids


def _trace_row(ts: int, svc: str, span: str, dur_ns: int, status: int | None, parent_id: str = "") -> dict[str, Any]:
    return {
        "time": ts,
        "trace_id": f"trace-{ts}-{span}",
        "span_id": f"sp-{ts}-{span}-{parent_id}",
        "parent_span_id": parent_id,
        "span_name": span,
        "service_name": svc,
        "duration": dur_ns,
        "attr.http.response.status_code": status,
    }


class _StubLoader:
    """Minimal ParquetDataLoader stand-in.

    The real loader requires a directory of parquet files; this stub
    serves a fixed graph + fixed trace DataFrames.
    """

    def __init__(self, data_dir: Path, _polars_threads: int) -> None:
        self.data_dir = data_dir
        graph, ids = _make_graph_with_calls_chain()
        self._graph = graph
        self._ids = ids

        baseline_rows = [_trace_row(1000 + i, "svc-callee", "POST /api", 10_000_000, 200) for i in range(20)]
        baseline_rows += [_trace_row(1000 + i, "svc-caller", "GET /home", 10_000_000, 200) for i in range(20)]
        abnormal_rows = [_trace_row(2000 + i, "svc-callee", "POST /api", 5_000_000_000, 200) for i in range(20)]
        abnormal_rows += [_trace_row(2000 + i, "svc-caller", "GET /home", 5_000_000_000, 200) for i in range(20)]
        self._baseline = pl.DataFrame(baseline_rows)
        self._abnormal = pl.DataFrame(abnormal_rows)

    def build_graph_from_parquet(self) -> HyperGraph:
        return self._graph

    def identify_alarm_nodes_v2(self) -> set[str]:
        return {"svc-caller::GET /home"}

    def load_traces(self, period: str = "abnormal") -> pl.DataFrame:
        return self._abnormal if period == "abnormal" else self._baseline

    def load_logs(self, period: str = "abnormal") -> pl.DataFrame:
        # Empty logs → log-dependency adapters short-circuit. The real
        # loader raises FileNotFoundError when the parquet is absent;
        # returning an empty frame is the equivalent for this stub.
        return pl.DataFrame(schema={"service_name": pl.Utf8, "level": pl.Utf8, "message": pl.Utf8})


def test_run_single_case_happy_path(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setattr(reasoning_cli, "ParquetDataLoader", _StubLoader)

    saved: dict[str, Any] = {}

    def _capture_save(*args: Any, **kwargs: Any) -> None:
        saved["called"] = True
        saved["status"] = kwargs.get("status") or (args[2] if len(args) > 2 else None)
        saved["case_name"] = kwargs.get("case_name") or (args[1] if len(args) > 1 else None)

    monkeypatch.setattr(reasoning_cli, "_save_case_result", _capture_save)

    injection_data = {
        "fault_type": "HTTPResponseDelay",
        "display_config": '{"injection_point": {"app_name": "svc-callee", "method": "POST", "route": "/api"}}',
        "ground_truth": {"service": ["svc-callee"]},
    }

    result = reasoning_cli.run_single_case(
        data_dir=tmp_path,
        max_hops=4,
        return_graph=False,
        injection_data=injection_data,
    )

    assert result["status"] == "success"
    assert result["paths"] >= 1
    assert saved["status"] == "success"


def test_run_single_case_no_alarms_returns_no_alarms(tmp_path: Path, monkeypatch) -> None:
    class _NoAlarmLoader(_StubLoader):
        def identify_alarm_nodes_v2(self) -> set[str]:
            return set()

    monkeypatch.setattr(reasoning_cli, "ParquetDataLoader", _NoAlarmLoader)
    monkeypatch.setattr(reasoning_cli, "_save_case_result", lambda *a, **kw: None)

    injection_data = {
        "fault_type": "HTTPResponseDelay",
        "display_config": '{"injection_point": {"app_name": "svc-callee", "method": "POST", "route": "/api"}}',
        "ground_truth": {"service": ["svc-callee"]},
    }
    result = reasoning_cli.run_single_case(
        data_dir=tmp_path,
        max_hops=4,
        return_graph=False,
        injection_data=injection_data,
    )
    assert result["status"] == "no_alarms"


# ---------------------------------------------------------------------------
# Silent-path wiring: TraceVolumeAdapter end-to-end through run_single_case.
# ---------------------------------------------------------------------------


def _make_graph_with_quiet_service() -> tuple[HyperGraph, dict[str, int]]:
    """Calls chain plus a third ``svc-quiet`` service with one span node.

    The graph mirrors :func:`_make_graph_with_calls_chain` and adds a
    third service node so the IR pipeline can produce a
    ``service|svc-quiet`` timeline that the TraceVolumeAdapter is allowed
    to flip to ``silent``.
    """
    g = HyperGraph()
    callee_svc = g.add_node(Node(kind=PlaceKind.service, self_name="svc-callee"))
    caller_svc = g.add_node(Node(kind=PlaceKind.service, self_name="svc-caller"))
    quiet_svc = g.add_node(Node(kind=PlaceKind.service, self_name="svc-quiet"))
    callee_span = g.add_node(Node(kind=PlaceKind.span, self_name="svc-callee::POST /api"))
    caller_span = g.add_node(Node(kind=PlaceKind.span, self_name="svc-caller::GET /home"))
    quiet_span = g.add_node(Node(kind=PlaceKind.span, self_name="svc-quiet::GET /tick"))

    ids = {
        n.uniq_name: n.id
        for n in (callee_svc, caller_svc, quiet_svc, callee_span, caller_span, quiet_span)
        if n.id is not None
    }

    g.add_edge(
        Edge(
            src_id=ids["service|svc-callee"],
            dst_id=ids["span|svc-callee::POST /api"],
            src_name="service|svc-callee",
            dst_name="span|svc-callee::POST /api",
            kind=DepKind.includes,
            data=None,
        )
    )
    g.add_edge(
        Edge(
            src_id=ids["service|svc-caller"],
            dst_id=ids["span|svc-caller::GET /home"],
            src_name="service|svc-caller",
            dst_name="span|svc-caller::GET /home",
            kind=DepKind.includes,
            data=None,
        )
    )
    g.add_edge(
        Edge(
            src_id=ids["service|svc-quiet"],
            dst_id=ids["span|svc-quiet::GET /tick"],
            src_name="service|svc-quiet",
            dst_name="span|svc-quiet::GET /tick",
            kind=DepKind.includes,
            data=None,
        )
    )
    g.add_edge(
        Edge(
            src_id=ids["span|svc-caller::GET /home"],
            dst_id=ids["span|svc-callee::POST /api"],
            src_name="span|svc-caller::GET /home",
            dst_name="span|svc-callee::POST /api",
            kind=DepKind.calls,
            data=CallsEdgeData(),
        )
    )
    return g, ids


def _silent_path_baseline_rows(rng: np.random.Generator) -> list[dict[str, Any]]:
    """Steady ~3-span/s baseline 1000..1599 for callee/caller/quiet."""
    rows: list[dict[str, Any]] = []
    for ts in range(1000, 1600):
        for svc, span in (
            ("svc-callee", "POST /api"),
            ("svc-caller", "GET /home"),
            ("svc-quiet", "GET /tick"),
        ):
            n = int(rng.poisson(3.0))
            for i in range(n):
                rows.append(_trace_row(ts, svc, span, 10_000_000, 200, parent_id=f"p{i}"))
    return rows


def _silent_path_abnormal_rows(rng: np.random.Generator) -> list[dict[str, Any]]:
    """Abnormal 2000..2299 — callee/caller carry on, quiet has zero spans."""
    rows: list[dict[str, Any]] = []
    for ts in range(2000, 2300):
        for svc, span in (
            ("svc-callee", "POST /api"),
            ("svc-caller", "GET /home"),
        ):
            n = int(rng.poisson(3.0))
            for i in range(n):
                rows.append(_trace_row(ts, svc, span, 5_000_000_000, 200, parent_id=f"p{i}"))
        # svc-quiet emits nothing in the abnormal window.
    return rows


class _SilentLoader:
    """Stub loader for the silent-path test.

    Wider baseline / abnormal windows than ``_StubLoader`` so the
    TraceVolumeAdapter calibrator has enough subwindows to stay out of
    the §11.2 opt-out regime.
    """

    def __init__(self, data_dir: Path, _polars_threads: int) -> None:
        self.data_dir = data_dir
        graph, ids = _make_graph_with_quiet_service()
        self._graph = graph
        self._ids = ids
        rng = np.random.default_rng(2026)
        self._baseline = pl.DataFrame(_silent_path_baseline_rows(rng))
        self._abnormal = pl.DataFrame(_silent_path_abnormal_rows(rng))

    def build_graph_from_parquet(self) -> HyperGraph:
        return self._graph

    def identify_alarm_nodes_v2(self) -> set[str]:
        return {"svc-caller::GET /home"}

    def load_traces(self, period: str = "abnormal") -> pl.DataFrame:
        return self._abnormal if period == "abnormal" else self._baseline

    def load_logs(self, period: str = "abnormal") -> pl.DataFrame:
        return pl.DataFrame(schema={"service_name": pl.Utf8, "level": pl.Utf8, "message": pl.Utf8})


def test_run_single_case_emits_silent_for_disappeared_service(tmp_path: Path, monkeypatch) -> None:
    """End-to-end: a service that vanishes from the abnormal window flips to ``silent``.

    Wires a deterministic ``trace_volume_rng_seed`` by monkeypatching
    ``cli.run_reasoning_ir`` with ``functools.partial`` rather than
    plumbing a new CLI-level argument — the test seed only matters for
    bootstrap reproducibility and production callers should keep getting
    fresh randomness.
    """
    monkeypatch.setattr(reasoning_cli, "ParquetDataLoader", _SilentLoader)

    captured: dict[str, Any] = {}

    def _capture_save(*args: Any, **kwargs: Any) -> None:
        captured["status"] = kwargs.get("status") or (args[2] if len(args) > 2 else None)

    monkeypatch.setattr(reasoning_cli, "_save_case_result", _capture_save)

    # Inject a deterministic seed into the TraceVolumeAdapter without
    # widening the public CLI signature: wrap run_reasoning_ir so every
    # invocation through cli sees ``trace_volume_rng_seed=42``.
    monkeypatch.setattr(
        reasoning_cli,
        "run_reasoning_ir",
        partial(_real_run_reasoning_ir, trace_volume_rng_seed=42),
    )

    # Capture the timelines that run_reasoning_ir produces so we can
    # assert on the svc-quiet state directly. We wrap the patched callable
    # one more time to grab the result on its way back to run_single_case.
    timelines_holder: dict[str, Any] = {}
    patched = reasoning_cli.run_reasoning_ir

    def _capture_timelines(*args: Any, **kwargs: Any) -> Any:
        out = patched(*args, **kwargs)
        timelines_holder["timelines"] = out
        return out

    monkeypatch.setattr(reasoning_cli, "run_reasoning_ir", _capture_timelines)

    injection_data = {
        "fault_type": "HTTPResponseDelay",
        "display_config": '{"injection_point": {"app_name": "svc-callee", "method": "POST", "route": "/api"}}',
        "ground_truth": {"service": ["svc-callee"]},
    }

    result = reasoning_cli.run_single_case(
        data_dir=tmp_path,
        max_hops=4,
        return_graph=False,
        injection_data=injection_data,
    )

    assert result["status"] == "success", result
    timelines = timelines_holder.get("timelines")
    assert timelines is not None, "run_reasoning_ir was not invoked"

    quiet_key = "service|svc-quiet"
    assert quiet_key in timelines, sorted(timelines.keys())
    quiet_timeline = timelines[quiet_key]
    final_state = quiet_timeline.windows[-1].state
    assert final_state == "silent", quiet_timeline.windows
