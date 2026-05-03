"""cli.run_single_case happy path on a synthetic graph + mocked loader.

End-to-end real-datapack testing is owner-driven follow-up; this test
exercises the CLI's wiring of ``run_reasoning_ir`` into the propagator
without touching parquet on disk.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import polars as pl
import pytest

from rcabench_platform.v3.internal.reasoning import cli as reasoning_cli
from rcabench_platform.v3.internal.reasoning.manifests import (
    ManifestRegistry,
    set_default_registry,
)
from rcabench_platform.v3.internal.reasoning.models.graph import (
    CallsEdgeData,
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)


@pytest.fixture(autouse=True)
def _empty_manifest_registry():
    """Force generic-rule path for these synthetic-graph tests.

    ``get_default_registry`` lazy-loads the bundled manifests when nobody
    has installed an explicit registry. The synthetic stubs in this
    module don't carry realistic feature samples, so manifest-aware
    gates would (correctly) reject the path. Pin an empty registry for
    the duration of each test, then restore the previous one.
    """
    from rcabench_platform.v3.internal.reasoning.manifests import registry as _reg

    prev = _reg._DEFAULT_REGISTRY
    set_default_registry(ManifestRegistry({}))
    yield
    _reg._DEFAULT_REGISTRY = prev


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
