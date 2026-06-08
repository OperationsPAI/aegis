"""Regression: otel-demo's load generator is named ``load-generator`` (hyphen),
not ``loadgenerator``. The detector's primary entrance filter used to match the
exact string ``loadgenerator`` only, so for otel-demo it MISSED and fell back to
``OtelDemoPedestal._ENTRANCE`` -- previously the sparse ``frontend-proxy`` ingress
(~1 root/min). The real user-journey roots (``user_browse_product`` etc.) live on
``load-generator`` at ~60/min, so 60x the samples were discarded and the
success-rate / latency gates never reached significance -> ~97% no_anomaly even
though faults took effect.

These tests assert the broadened filter selects the high-volume ``load-generator``
roots for otel-demo (NOT the sparse ``frontend-proxy`` ingress) and that
ts-style ``loadgenerator`` still selects correctly (no regression).
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

import polars as pl
import pytest

# cli/ is a script dir, not an installed package; tests/ rootdir doesn't have
# the repo root on sys.path, so add it before importing the detector helper.
sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from cli.detector import preprocess_trace  # type: ignore[import-not-found]  # noqa: E402
from rcabench_platform.v3.sdk.pedestals import get_pedestal  # noqa: E402


def _span(
    service: str,
    span_name: str,
    *,
    parent: str,
    span_id: str,
    duration_ns: int,
    status_code: str = "200",
) -> dict:
    return {
        "ServiceName": service,
        "SpanName": span_name,
        "ParentSpanId": parent,
        "SpanId": span_id,
        "Timestamp": 1_700_000_000_000_000_000,
        "Duration": duration_ns,
        "StatusCode": "Unset",
        "SpanAttributes": json.dumps({"http.status_code": status_code}),
    }


def _write_parquet(rows: list[dict], path: Path) -> Path:
    pl.DataFrame(rows).write_parquet(path)
    return path


def test_otel_demo_selects_load_generator_roots_not_frontend_proxy(tmp_path: Path) -> None:
    """otel-demo's hyphenated `load-generator` user-journey roots must be the
    selected entrance, vastly outnumbering the sparse `frontend-proxy` ingress."""
    rows: list[dict] = []

    # High-volume user-journey roots on the (hyphenated) load generator.
    for i in range(60):
        rows.append(
            _span(
                "load-generator",
                "user_browse_product",
                parent="",
                span_id=f"lg-{i}",
                duration_ns=2_000_000,
            )
        )
    for i in range(30):
        rows.append(
            _span(
                "load-generator",
                "user_checkout",
                parent="",
                span_id=f"lc-{i}",
                duration_ns=3_000_000,
            )
        )

    # Sparse browser-facing ingress on frontend-proxy (the old, wrong entrance).
    for i in range(2):
        rows.append(
            _span(
                "frontend-proxy",
                "ingress",
                parent="",
                span_id=f"fp-{i}",
                duration_ns=1_500_000,
            )
        )

    # A non-root frontend-proxy child (must never be picked as an entrance).
    rows.append(
        _span(
            "frontend-proxy",
            "egress",
            parent="lg-0",
            span_id="fp-child",
            duration_ns=900_000,
        )
    )

    file = _write_parquet(rows, tmp_path / "trace.parquet")
    pedestal = get_pedestal("otel-demo")

    stat = preprocess_trace(file, pedestal)

    # The user-journey roots are the entrance; the ingress root is NOT.
    assert "user_browse_product" in stat
    assert "user_checkout" in stat
    assert "ingress" not in stat
    assert "egress" not in stat

    load_gen_samples = len(stat["user_browse_product"]["timestamp"]) + len(stat["user_checkout"]["timestamp"])
    assert load_gen_samples == 90, load_gen_samples
    # 90 load-generator samples vs the 2 frontend-proxy ingress roots that the
    # old code would have used -- ~45x more here (60x in production).
    assert load_gen_samples > 30


def test_loadgenerator_still_selected_no_regression(tmp_path: Path) -> None:
    """ts-style `loadgenerator` (no hyphen) must keep selecting its roots."""
    rows: list[dict] = []
    for i in range(40):
        rows.append(
            _span(
                "loadgenerator",
                "GET /index",
                parent="",
                span_id=f"lg-{i}",
                duration_ns=2_000_000,
            )
        )
    # An internal service that must never be picked while loadgenerator roots exist.
    rows.append(
        _span(
            "ts-ui-dashboard",
            "GET /home",
            parent="",
            span_id="ui-0",
            duration_ns=5_000_000,
        )
    )

    file = _write_parquet(rows, tmp_path / "trace.parquet")
    pedestal = get_pedestal("ts")

    stat = preprocess_trace(file, pedestal)

    assert "GET /index" in stat
    assert "GET /home" not in stat
    assert len(stat["GET /index"]["timestamp"]) == 40
