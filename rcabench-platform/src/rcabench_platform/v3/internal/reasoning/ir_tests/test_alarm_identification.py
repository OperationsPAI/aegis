"""Unit tests for ``ParquetDataLoader.identify_alarm_nodes_v2``'s root-span filter.

The default behaviour previously kept every Server-kind span across the full
call tree, which on TrainTicket cases inflated the alarm set to ~54 spans
and drove the propagator's path-enumeration into a multi-GB/many-minute
stall. The corrected filter keeps only the *topmost* Server-kind span per
trace — i.e. a Server span whose parent (``trace_id, parent_span_id``) is
not itself Server-kind. These tests exercise the corrected behaviour with
a small synthetic trace dataframe.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import polars as pl

from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import ParquetDataLoader


_TRACE_COLUMNS: dict[str, Any] = {
    "time": pl.Datetime("ns"),
    "trace_id": pl.Utf8,
    "span_id": pl.Utf8,
    "parent_span_id": pl.Utf8,
    "span_name": pl.Utf8,
    "attr.span_kind": pl.Utf8,
    "service_name": pl.Utf8,
    "duration": pl.Int64,
    "attr.http.response.status_code": pl.Int64,
}


def _make_trace_df(rows: list[dict[str, Any]]) -> pl.DataFrame:
    """Build a polars DataFrame from a list of span dicts, ensuring all the
    columns referenced by ``identify_alarm_nodes_v2`` exist with the right
    dtypes.
    """
    # Fill any missing columns with sensible defaults.
    n = len(rows)
    base_time = pl.Series("time", [0] * n).cast(pl.Datetime("ns"))
    full: dict[str, Any] = {
        "time": base_time,
    }
    for col in (
        "trace_id",
        "span_id",
        "parent_span_id",
        "span_name",
        "attr.span_kind",
        "service_name",
    ):
        full[col] = [r.get(col, "") for r in rows]
    full["duration"] = [int(r.get("duration", 1_000_000)) for r in rows]
    full["attr.http.response.status_code"] = [
        r.get("attr.http.response.status_code") for r in rows
    ]
    df = pl.DataFrame(full)
    return df.with_columns(
        pl.col("attr.http.response.status_code").cast(pl.Int64),
        pl.col("duration").cast(pl.Int64),
    )


class _StubLoader(ParquetDataLoader):
    """ParquetDataLoader that returns canned baseline/abnormal trace
    DataFrames instead of loading from disk."""

    def __init__(self, baseline: pl.DataFrame, abnormal: pl.DataFrame):
        super().__init__(data_dir=Path("/dev/null/unused"))
        self._stub_baseline = baseline
        self._stub_abnormal = abnormal

    def load_traces(self, period: str = "abnormal") -> pl.DataFrame:  # type: ignore[override]
        if period == "normal":
            return self._stub_baseline
        return self._stub_abnormal


def _three_deep_trace(
    *,
    trace_id: str,
    a_dur_ns: int,
    b_dur_ns: int,
    c_dur_ns: int,
    a_status: int = 200,
    b_status: int = 200,
    c_status: int = 200,
) -> list[dict[str, Any]]:
    """Build a 4-row trace: loadgen-Client → A-Server → B-Server → C-Server.

    Span ids are deterministic per ``trace_id`` so multiple traces stay
    independent across the dataframe.
    """
    return [
        {
            # External loadgen client wrapping the request.
            "trace_id": trace_id,
            "span_id": f"{trace_id}-loadgen",
            "parent_span_id": "",
            "span_name": "loadgen call",
            "attr.span_kind": "Client",
            "service_name": "loadgenerator",
            "duration": 5 * 1_000_000,
            "attr.http.response.status_code": 200,
        },
        {
            # A: topmost Server (parent is loadgen Client) — the only
            # alarm candidate per the corrected filter.
            "trace_id": trace_id,
            "span_id": f"{trace_id}-a",
            "parent_span_id": f"{trace_id}-loadgen",
            "span_name": "GET /a",
            "attr.span_kind": "Server",
            "service_name": "svc-a",
            "duration": a_dur_ns,
            "attr.http.response.status_code": a_status,
        },
        {
            # B: nested Server under A — must be filtered out.
            "trace_id": trace_id,
            "span_id": f"{trace_id}-b",
            "parent_span_id": f"{trace_id}-a",
            "span_name": "GET /b",
            "attr.span_kind": "Server",
            "service_name": "svc-b",
            "duration": b_dur_ns,
            "attr.http.response.status_code": b_status,
        },
        {
            # C: nested Server under B — must be filtered out.
            "trace_id": trace_id,
            "span_id": f"{trace_id}-c",
            "parent_span_id": f"{trace_id}-b",
            "span_name": "GET /c",
            "attr.span_kind": "Server",
            "service_name": "svc-c",
            "duration": c_dur_ns,
            "attr.http.response.status_code": c_status,
        },
    ]


def test_topmost_server_only_when_all_anomalous() -> None:
    """3-deep trace where A, B, C all have anomalous metrics in the
    abnormal period vs baseline. Only A (topmost Server, parent is Client)
    should appear in the alarm set; B and C are excluded because their
    parents are Server-kind."""
    # Build a baseline: 20 traces, low latency on all three services.
    baseline_rows: list[dict[str, Any]] = []
    for i in range(20):
        baseline_rows.extend(
            _three_deep_trace(
                trace_id=f"base-{i}",
                a_dur_ns=10 * 1_000_000,  # 10ms
                b_dur_ns=5 * 1_000_000,   # 5ms
                c_dur_ns=2 * 1_000_000,   # 2ms
            )
        )

    # Build an abnormal: 20 traces, latency on A, B, and C all blown up
    # by 50× — every Server-kind span looks anomalous.
    abnormal_rows: list[dict[str, Any]] = []
    for i in range(20):
        abnormal_rows.extend(
            _three_deep_trace(
                trace_id=f"abn-{i}",
                a_dur_ns=500 * 1_000_000,  # 500ms
                b_dur_ns=250 * 1_000_000,  # 250ms
                c_dur_ns=100 * 1_000_000,  # 100ms
            )
        )

    loader = _StubLoader(_make_trace_df(baseline_rows), _make_trace_df(abnormal_rows))
    alarms = loader.identify_alarm_nodes_v2()

    # Only svc-a's topmost Server endpoint should be alarmed; svc-b and
    # svc-c are nested under another Server and must be filtered out.
    assert "svc-a::GET /a" in alarms, alarms
    assert "svc-b::GET /b" not in alarms, alarms
    assert "svc-c::GET /c" not in alarms, alarms
    # Hard upper bound: the corrected filter keeps exactly one entry on
    # this fixture.
    assert len(alarms) == 1, alarms


def test_loadgen_parent_excluded_svc_below_kept() -> None:
    """When ``loadgenerator`` is the immediate parent of a non-loadgen span,
    the non-loadgen child IS the topmost-non-loadgen span and must be kept.

    The methodology is kind-agnostic: ``span_kind`` is unreliable across
    instrumentation, so the alarm-root predicate is purely
    "owner is non-loadgen AND parent is loadgen-or-missing." Loadgen's
    own span is always excluded by the loadgen list; its child is the
    user-perceptible boundary regardless of how loadgen happens to be
    labelled.
    """
    base_rows: list[dict[str, Any]] = []
    abn_rows: list[dict[str, Any]] = []
    for i in range(15):
        for tid_prefix, dur_a in (("base", 10), ("abn", 500)):
            rows = [
                {
                    "trace_id": f"{tid_prefix}-{i}",
                    "span_id": f"{tid_prefix}-{i}-load",
                    "parent_span_id": "",
                    "span_name": "loadgen entry",
                    "attr.span_kind": "Server",  # kind label is irrelevant
                    "service_name": "loadgenerator",
                    "duration": 1_000_000,
                    "attr.http.response.status_code": 200,
                },
                {
                    "trace_id": f"{tid_prefix}-{i}",
                    "span_id": f"{tid_prefix}-{i}-a",
                    "parent_span_id": f"{tid_prefix}-{i}-load",
                    "span_name": "GET /a",
                    "attr.span_kind": "Server",
                    "service_name": "svc-a",
                    "duration": dur_a * 1_000_000,
                    "attr.http.response.status_code": 200,
                },
            ]
            if tid_prefix == "base":
                base_rows.extend(rows)
            else:
                abn_rows.extend(rows)

    loader = _StubLoader(_make_trace_df(base_rows), _make_trace_df(abn_rows))
    alarms = loader.identify_alarm_nodes_v2()
    # svc-a is the topmost non-loadgen span; its 50× latency increase makes
    # it an alarm. loadgenerator is excluded by the loadgen list.
    assert "svc-a::GET /a" in alarms, alarms


def test_root_with_empty_parent_passes_filter() -> None:
    """A true root Server span (``parent_span_id == ""``) on a non-loadgen
    service must pass the anti-join: empty string never matches a real
    span_id, so the row survives the anti-join and is kept by the Server
    + non-loadgen filter."""
    base_rows = []
    abn_rows = []
    for i in range(20):
        # Real frontend that has NO loadgen wrapper above it — root span.
        base_rows.append(
            {
                "trace_id": f"base-{i}",
                "span_id": f"base-{i}-fe",
                "parent_span_id": "",
                "span_name": "GET /home",
                "attr.span_kind": "Server",
                "service_name": "ts-ui-dashboard",
                "duration": 10 * 1_000_000,
                "attr.http.response.status_code": 200,
            }
        )
        abn_rows.append(
            {
                "trace_id": f"abn-{i}",
                "span_id": f"abn-{i}-fe",
                "parent_span_id": "",
                "span_name": "GET /home",
                "attr.span_kind": "Server",
                "service_name": "ts-ui-dashboard",
                "duration": 500 * 1_000_000,
                "attr.http.response.status_code": 200,
            }
        )

    loader = _StubLoader(_make_trace_df(base_rows), _make_trace_df(abn_rows))
    alarms = loader.identify_alarm_nodes_v2()
    assert "ts-ui-dashboard::GET /home" in alarms, alarms
