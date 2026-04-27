"""Unit tests for the structural trace-truncation alarm signal.

Synthetic-only fixtures — no dependency on real case parquet. Mirrors
the polars DataFrame shape used by ``test_alarm_identification.py``.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import polars as pl

from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import (
    ParquetDataLoader,
)
from rcabench_platform.v3.internal.reasoning.loaders.trace_truncation import (
    BaselineProfile,
    build_baseline_profile,
    detect_truncated_endpoints,
)

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
    times = [int(r.get("_time_offset", idx)) for idx, r in enumerate(rows)]
    base_time = pl.Series("time", times).cast(pl.Datetime("ns"))
    full: dict[str, Any] = {"time": base_time}
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
    full["attr.http.response.status_code"] = [r.get("attr.http.response.status_code") for r in rows]
    df = pl.DataFrame(full)
    return df.with_columns(
        pl.col("attr.http.response.status_code").cast(pl.Int64),
        pl.col("duration").cast(pl.Int64),
    )


def _full_chain_trace(
    trace_id: str,
    services: list[str],
    *,
    span_name: str = "GET /home",
    loadgen: str = "loadgenerator",
    time_offset: int = 0,
) -> list[dict[str, Any]]:
    """Build a linear chain: loadgen → services[0] → services[1] → ...

    The topmost-non-loadgen span is ``services[0]`` with ``span_name``,
    so the endpoint key is ``"{services[0]}::{span_name}"``.
    """
    rows: list[dict[str, Any]] = [
        {
            "trace_id": trace_id,
            "span_id": f"{trace_id}-load",
            "parent_span_id": "",
            "span_name": "loadgen call",
            "attr.span_kind": "Client",
            "service_name": loadgen,
            "duration": 1_000_000,
            "attr.http.response.status_code": 200,
            "_time_offset": time_offset,
        }
    ]
    parent = f"{trace_id}-load"
    for i, svc in enumerate(services):
        sid = f"{trace_id}-{i}"
        rows.append(
            {
                "trace_id": trace_id,
                "span_id": sid,
                "parent_span_id": parent,
                "span_name": span_name if i == 0 else f"call-{svc}",
                "attr.span_kind": "Server",
                "service_name": svc,
                "duration": 5_000_000,
                "attr.http.response.status_code": 200,
                "_time_offset": time_offset + i + 1,
            }
        )
        parent = sid
    return rows


# ---------------------------------------------------------------------------
# 1. baseline profile basic
# ---------------------------------------------------------------------------


def test_baseline_profile_basic() -> None:
    rows: list[dict[str, Any]] = []
    for i in range(10):
        rows.extend(
            _full_chain_trace(
                f"base-{i}",
                services=["ts-ui-dashboard", "ts-A", "ts-B"],
            )
        )
    df = _make_trace_df(rows)
    profiles = build_baseline_profile(df)

    endpoint = "ts-ui-dashboard::GET /home"
    assert endpoint in profiles, list(profiles.keys())
    p = profiles[endpoint]
    assert p.trace_count == 10
    assert p.ubiquitous_services == frozenset({"ts-ui-dashboard", "ts-A", "ts-B"})
    # Exactly one canonical shape (all baseline traces share it).
    assert len(p.canonical_shapes) == 1
    assert p.canonical_shapes[0] == frozenset({"ts-ui-dashboard", "ts-A", "ts-B"})
    # Edge shape: (ts-ui-dashboard, ts-A) and (ts-A, ts-B); loadgen edge dropped.
    assert ("ts-ui-dashboard", "ts-A") in p.canonical_edge_shapes[0]
    assert ("ts-A", "ts-B") in p.canonical_edge_shapes[0]
    # Span counts: each baseline trace has 4 spans (loadgen + 3 services).
    assert p.span_count_p50 == 4.0


# ---------------------------------------------------------------------------
# 2. truncated trace flagged
# ---------------------------------------------------------------------------


def test_truncated_trace_flagged() -> None:
    base_rows: list[dict[str, Any]] = []
    for i in range(10):
        base_rows.extend(
            _full_chain_trace(
                f"base-{i}",
                services=["ts-ui-dashboard", "ts-A", "ts-B"],
            )
        )
    profiles = build_baseline_profile(_make_trace_df(base_rows))

    # Abnormal: 5 truncated traces (loadgen + ts-ui-dashboard only) on the SAME endpoint.
    abn_rows: list[dict[str, Any]] = []
    for i in range(5):
        abn_rows.extend(
            _full_chain_trace(
                f"abn-{i}",
                services=["ts-ui-dashboard"],  # subtree absent
            )
        )
    alarms = detect_truncated_endpoints(_make_trace_df(abn_rows), profiles)

    endpoint = "ts-ui-dashboard::GET /home"
    assert endpoint in alarms, list(alarms.keys())
    info = alarms[endpoint]
    assert info.failed_count == 5
    assert info.total_abnormal_count == 5
    assert set(info.missing_services) == {"ts-A", "ts-B"}
    assert "ts-ui-dashboard" in info.failed_services


# ---------------------------------------------------------------------------
# 3. below MIN_BASELINE_TRACES skipped
# ---------------------------------------------------------------------------


def test_below_min_baseline_skipped() -> None:
    base_rows: list[dict[str, Any]] = []
    for i in range(3):  # < MIN_BASELINE_TRACES = 5
        base_rows.extend(
            _full_chain_trace(
                f"base-{i}",
                services=["ts-ui-dashboard", "ts-A", "ts-B"],
            )
        )
    profiles = build_baseline_profile(_make_trace_df(base_rows))
    # Endpoint excluded from profile because it has too few baseline traces.
    assert profiles == {}, profiles

    abn_rows: list[dict[str, Any]] = []
    for i in range(20):
        abn_rows.extend(
            _full_chain_trace(
                f"abn-{i}",
                services=["ts-ui-dashboard"],  # 100% truncated
            )
        )
    alarms = detect_truncated_endpoints(_make_trace_df(abn_rows), profiles)
    assert alarms == {}, alarms


# ---------------------------------------------------------------------------
# 4. legit short variant (cache hit) NOT flagged
# ---------------------------------------------------------------------------


def test_legit_short_variant_not_flagged() -> None:
    """Baseline has TWO common shapes for the same endpoint (cache-hit
    with 3 services, cache-miss with 6). Abnormal traces matching the
    cache-hit shape have Jaccard 1.0 against a canonical shape, so S2
    and S3 don't trip.
    """
    base_rows: list[dict[str, Any]] = []
    # 50/50 split so both shapes are canonical (each ≥ 50% frequency).
    for i in range(10):
        base_rows.extend(
            _full_chain_trace(
                f"basehit-{i}",
                services=["ts-ui-dashboard", "ts-A", "ts-B"],
            )
        )
    for i in range(10):
        base_rows.extend(
            _full_chain_trace(
                f"basemiss-{i}",
                services=[
                    "ts-ui-dashboard",
                    "ts-A",
                    "ts-B",
                    "ts-C",
                    "ts-D",
                    "ts-E",
                ],
            )
        )
    profiles = build_baseline_profile(_make_trace_df(base_rows))
    endpoint = "ts-ui-dashboard::GET /home"
    assert endpoint in profiles
    # No service is ubiquitous across BOTH shapes... wait, ts-ui-dashboard,
    # ts-A, ts-B all appear in both shapes (100%). ts-C/D/E only in miss
    # shape (50%). So ubiquitous = {ts-ui-dashboard, ts-A, ts-B}.
    p = profiles[endpoint]
    assert p.ubiquitous_services == frozenset({"ts-ui-dashboard", "ts-A", "ts-B"})

    # Abnormal: 20 cache-hit-shape traces (matches a canonical shape exactly).
    abn_rows: list[dict[str, Any]] = []
    for i in range(20):
        abn_rows.extend(
            _full_chain_trace(
                f"abn-{i}",
                services=["ts-ui-dashboard", "ts-A", "ts-B"],
            )
        )
    alarms = detect_truncated_endpoints(_make_trace_df(abn_rows), profiles)
    # Must NOT be flagged: S2/S3 see Jaccard 1.0 against the cache-hit canonical.
    # S4 also doesn't fire (no ubiquitous services missing). S1 doesn't fire
    # (4 spans > p1).
    assert endpoint not in alarms, alarms


# ---------------------------------------------------------------------------
# 5. below truncation rate threshold
# ---------------------------------------------------------------------------


def test_below_truncation_rate_threshold() -> None:
    base_rows: list[dict[str, Any]] = []
    for i in range(20):
        base_rows.extend(
            _full_chain_trace(
                f"base-{i}",
                services=["ts-ui-dashboard", "ts-A", "ts-B"],
            )
        )
    profiles = build_baseline_profile(_make_trace_df(base_rows))

    # 1 truncated abnormal trace + 99 healthy ones — 1% rate < 5% threshold,
    # AND failed_count = 1 < MIN_FAILED_TRACES = 3.
    abn_rows: list[dict[str, Any]] = []
    for i in range(99):
        abn_rows.extend(
            _full_chain_trace(
                f"abn-ok-{i}",
                services=["ts-ui-dashboard", "ts-A", "ts-B"],
            )
        )
    abn_rows.extend(
        _full_chain_trace(
            "abn-bad-0",
            services=["ts-ui-dashboard"],
        )
    )
    alarms = detect_truncated_endpoints(_make_trace_df(abn_rows), profiles)
    assert "ts-ui-dashboard::GET /home" not in alarms, alarms


# ---------------------------------------------------------------------------
# 6. topmost-non-loadgen grouping (kind-agnostic, even with weird kinds)
# ---------------------------------------------------------------------------


def test_topmost_non_loadgen_grouping() -> None:
    """Even when loadgen is labelled with a non-Client kind, the
    endpoint pivot is still the topmost non-loadgen span.
    """
    base_rows: list[dict[str, Any]] = []
    for i in range(10):
        # Loadgen labelled "Server" (unusual) — must still be treated as
        # the synthetic ingress and excluded from the endpoint pivot.
        rows = _full_chain_trace(
            f"base-{i}",
            services=["ts-ui-dashboard", "ts-A"],
        )
        rows[0]["attr.span_kind"] = "Server"
        base_rows.extend(rows)
    profiles = build_baseline_profile(_make_trace_df(base_rows))

    # Endpoint must be ts-ui-dashboard::GET /home, NOT loadgenerator::*.
    assert "ts-ui-dashboard::GET /home" in profiles, list(profiles.keys())
    assert not any(k.startswith("loadgenerator::") for k in profiles)


# ---------------------------------------------------------------------------
# 7. End-to-end integration via ParquetDataLoader.identify_alarm_nodes_v2
# ---------------------------------------------------------------------------


class _StubLoader(ParquetDataLoader):
    def __init__(self, baseline: pl.DataFrame, abnormal: pl.DataFrame):
        super().__init__(data_dir=Path("/dev/null/unused"))
        self._stub_baseline = baseline
        self._stub_abnormal = abnormal

    def load_traces(self, period: str = "abnormal") -> pl.DataFrame:  # type: ignore[override]
        if period == "normal":
            return self._stub_baseline
        return self._stub_abnormal


def test_truncation_alarm_surfaces_in_identify_alarm_nodes_v2() -> None:
    """The truncation pass adds endpoints to the alarm set even when no
    descendant carries an error attribute and latency is not elevated.
    """
    base_rows: list[dict[str, Any]] = []
    for i in range(15):
        base_rows.extend(
            _full_chain_trace(
                f"base-{i}",
                services=["ts-ui-dashboard", "ts-A", "ts-B"],
            )
        )
    abn_rows: list[dict[str, Any]] = []
    for i in range(10):
        abn_rows.extend(
            _full_chain_trace(
                f"abn-{i}",
                services=["ts-ui-dashboard"],  # truncated, no error attr
            )
        )

    loader = _StubLoader(_make_trace_df(base_rows), _make_trace_df(abn_rows))
    alarms = loader.identify_alarm_nodes_v2()
    assert "ts-ui-dashboard::GET /home" in alarms, alarms
    sidecar = loader.get_truncation_alarms()
    assert "ts-ui-dashboard::GET /home" in sidecar, sidecar
    info = sidecar["ts-ui-dashboard::GET /home"]
    assert info.failed_count == 10
    assert set(info.missing_services) == {"ts-A", "ts-B"}
