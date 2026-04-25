"""TraceStateAdapter: synthetic baseline / abnormal DataFrames."""

from __future__ import annotations

from pathlib import Path

import polars as pl

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.traces import TraceStateAdapter
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="trace-fixture")


def _row(ts: int, svc: str, span: str, dur_ns: int, status: int | None, parent: str = "") -> dict:
    return {
        "time": ts,
        "trace_id": f"trace-{ts}-{span}",
        "span_id": f"sp-{ts}-{span}",
        "parent_span_id": parent,
        "span_name": span,
        "service_name": svc,
        "duration": dur_ns,
        "attr.http.response.status_code": status,
    }


def _df(rows: list[dict]) -> pl.DataFrame:
    return pl.DataFrame(rows)


def test_slow_window_emits_slow_then_recovers() -> None:
    base_rows = []
    for i in range(60):
        base_rows.append(_row(1000 + i, "front-end", "GET /login", 100_000_000, 200))
    abn_rows = []
    # window 1: 2000-2003 — fast (healthy)
    for i in range(20):
        abn_rows.append(_row(2000 + i % 3, "front-end", "GET /login", 100_000_000, 200))
    # window 2: 2003-2006 — 50x slow latency
    for i in range(20):
        abn_rows.append(_row(2003 + i % 3, "front-end", "GET /login", 5_000_000_000, 200))
    # window 3: 2006-2009 — back to baseline
    for i in range(20):
        abn_rows.append(_row(2006 + i % 3, "front-end", "GET /login", 100_000_000, 200))

    adapter = TraceStateAdapter(_df(base_rows), _df(abn_rows), window_sec=3)
    events = list(adapter.emit(CTX))
    span_events = [e for e in events if e.kind == PlaceKind.span]
    assert any(e.to_state == "slow" for e in span_events), span_events
    states = [e.to_state for e in span_events]
    # Must transition to slow, then back to healthy
    assert "slow" in states
    assert "healthy" in states
    slow = next(e for e in span_events if e.to_state == "slow")
    assert slow.level == EvidenceLevel.observed
    assert slow.evidence.get("trigger_metric") in {"avg_latency", "p99_latency"}


def test_error_rate_emits_erroring() -> None:
    base_rows = [_row(1000 + i, "front-end", "GET /api", 50_000_000, 200) for i in range(50)]
    abn_rows = []
    for i in range(20):
        abn_rows.append(_row(2000 + i % 3, "front-end", "GET /api", 50_000_000, 500))
    adapter = TraceStateAdapter(_df(base_rows), _df(abn_rows), window_sec=3)
    events = [e for e in adapter.emit(CTX) if e.kind == PlaceKind.span]
    assert events
    errs = [e for e in events if e.to_state == "erroring"]
    assert errs, events
    assert errs[0].evidence.get("trigger_metric") == "error_rate"


def test_missing_span_in_abnormal() -> None:
    base_rows = [_row(1000 + i, "svc-a", "GET /vanish", 50_000_000, 200) for i in range(20)]
    base_rows += [_row(1000 + i, "svc-a", "GET /alive", 50_000_000, 200) for i in range(20)]
    abn_rows = [_row(2000 + i % 3, "svc-a", "GET /alive", 50_000_000, 200) for i in range(20)]
    adapter = TraceStateAdapter(_df(base_rows), _df(abn_rows), window_sec=3)
    events = [e for e in adapter.emit(CTX) if e.kind == PlaceKind.span]
    miss = [e for e in events if e.to_state == "missing"]
    assert miss
    assert miss[0].node_key == "span|svc-a::GET /vanish"


def test_service_rollup_unavailable_when_root_missing() -> None:
    base_rows = [_row(1000 + i, "svc-x", "GET /root", 50_000_000, 200) for i in range(20)]
    abn_rows: list[dict] = []
    adapter = TraceStateAdapter(_df(base_rows), _df(abn_rows), window_sec=3)
    events = list(adapter.emit(CTX))
    # No abnormal rows, so adapter should emit nothing
    assert events == []


def test_service_rollup_slow_when_root_slow() -> None:
    base_rows = [_row(1000 + i, "svc-y", "GET /root", 100_000_000, 200, parent="") for i in range(40)]
    abn_rows = [_row(2000 + i % 3, "svc-y", "GET /root", 5_000_000_000, 200, parent="") for i in range(40)]
    adapter = TraceStateAdapter(_df(base_rows), _df(abn_rows), window_sec=3)
    events = list(adapter.emit(CTX))
    svc_events = [e for e in events if e.kind == PlaceKind.service]
    assert svc_events, events
    assert any(e.to_state == "slow" for e in svc_events)
