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
from rcabench_platform.v3.sdk.logging import logger  # noqa: E402
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


@pytest.fixture
def capture_warnings():
    """Collect WARNING-level loguru records emitted during the test."""
    records: list[str] = []
    sink_id = logger.add(lambda msg: records.append(str(msg)), level="WARNING")
    try:
        yield records
    finally:
        logger.remove(sink_id)


def test_tier3_falls_back_to_high_volume_root_when_entrance_is_sparse(
    tmp_path: Path, capture_warnings: list[str]
) -> None:
    """When the configured entrance has too few root spans for the SLO gates but a
    different service carries the real high-volume roots, tier 3 picks the
    high-volume service and emits a WARNING naming both."""
    rows: list[dict] = []

    # Configured entrance (hs's `frontend`) has only 2 root spans — below the
    # 10-root usable threshold the success-rate gate needs.
    for i in range(2):
        rows.append(
            _span(
                "frontend",
                "GET /sparse",
                parent="",
                span_id=f"fe-{i}",
                duration_ns=1_000_000,
            )
        )

    # A different service carries 50 high-volume user-journey roots.
    for i in range(50):
        rows.append(
            _span(
                "edge-router",
                "GET /home",
                parent="",
                span_id=f"er-{i}",
                duration_ns=2_000_000,
            )
        )

    # A non-root span on the high-volume service must not be counted as a root.
    rows.append(
        _span(
            "edge-router",
            "internal-call",
            parent="er-0",
            span_id="er-child",
            duration_ns=500_000,
        )
    )

    file = _write_parquet(rows, tmp_path / "trace.parquet")
    pedestal = get_pedestal("hs")  # entrance_service == "frontend"

    stat = preprocess_trace(file, pedestal)

    # Tier 3 resolved the entrance to the high-volume `edge-router` roots.
    assert "GET /home" in stat
    assert "GET /sparse" not in stat
    assert "internal-call" not in stat
    assert len(stat["GET /home"]["timestamp"]) == 50

    warning_text = "\n".join(capture_warnings)
    assert "frontend" in warning_text
    assert "edge-router" in warning_text
    assert "2 root span" in warning_text


def test_tier3_breaks_ties_deterministically_by_service_name(tmp_path: Path) -> None:
    """When two services have equal root-span counts, the alphabetically-first
    name wins so runs are reproducible."""
    rows: list[dict] = []
    # Configured entrance is absent; two services tie at 20 roots each.
    for i in range(20):
        rows.append(_span("zeta-svc", "GET /z", parent="", span_id=f"z-{i}", duration_ns=1_000_000))
        rows.append(_span("alpha-svc", "GET /a", parent="", span_id=f"a-{i}", duration_ns=1_000_000))

    file = _write_parquet(rows, tmp_path / "trace.parquet")
    pedestal = get_pedestal("hs")  # entrance_service "frontend" absent here

    stat = preprocess_trace(file, pedestal)

    assert "GET /a" in stat
    assert "GET /z" not in stat


def test_high_volume_entrance_does_not_trigger_tier3(tmp_path: Path, capture_warnings: list[str]) -> None:
    """An hs-style trace whose configured `frontend` entrance IS the high-volume
    root must resolve via tier 2 with no tier-3 fallback / WARNING."""
    rows: list[dict] = []
    for i in range(40):
        rows.append(_span("frontend", "GET /hotels", parent="", span_id=f"fe-{i}", duration_ns=2_000_000))
    # A noisier internal service that tier 3 would wrongly prefer if it fired.
    for i in range(80):
        rows.append(_span("search-svc", "internal", parent="", span_id=f"s-{i}", duration_ns=500_000))

    file = _write_parquet(rows, tmp_path / "trace.parquet")
    pedestal = get_pedestal("hs")

    stat = preprocess_trace(file, pedestal)

    assert "GET /hotels" in stat
    assert "internal" not in stat
    assert len(stat["GET /hotels"]["timestamp"]) == 40
    # Tier 2 resolved the entrance; no tier-3 fallback should have fired.
    assert not any("Falling back to highest-volume" in w for w in capture_warnings)


def test_empty_trace_no_roots_returns_empty_stat(tmp_path: Path) -> None:
    """No root spans on ANY service → loud fail preserved (returns {})."""
    rows = [
        _span("frontend", "child-a", parent="p1", span_id="c1", duration_ns=1_000_000),
        _span("search-svc", "child-b", parent="p2", span_id="c2", duration_ns=1_000_000),
    ]
    file = _write_parquet(rows, tmp_path / "trace.parquet")
    pedestal = get_pedestal("hs")

    stat = preprocess_trace(file, pedestal)

    assert stat == {}


def test_near_dark_window_below_threshold_returns_empty_stat(tmp_path: Path) -> None:
    """Entrance killed and only a few stray roots survive (all below the usable
    threshold) → entrance-unreachable signal preserved (returns {})."""
    rows: list[dict] = []
    # 3 stray roots scattered across services, none reaching the 10-root floor.
    rows.append(_span("frontend", "GET /x", parent="", span_id="r0", duration_ns=1_000_000))
    rows.append(_span("search-svc", "GET /y", parent="", span_id="r1", duration_ns=1_000_000))
    rows.append(_span("search-svc", "GET /y", parent="", span_id="r2", duration_ns=1_000_000))

    file = _write_parquet(rows, tmp_path / "trace.parquet")
    pedestal = get_pedestal("hs")

    stat = preprocess_trace(file, pedestal)

    assert stat == {}


def test_otel_demo_load_generator_user_journey_roots_selected(tmp_path: Path) -> None:
    """otel-demo's `load-generator` user-journey roots are selected (via tier 1),
    not the sparse `frontend-proxy` ingress — the case the 1.0.4 hard-code +
    tier-1 name match handle."""
    rows: list[dict] = []
    for i in range(60):
        rows.append(_span("load-generator", "user_browse_product", parent="", span_id=f"lg-{i}", duration_ns=2_000_000))
    for i in range(2):
        rows.append(_span("frontend-proxy", "ingress", parent="", span_id=f"fp-{i}", duration_ns=1_500_000))

    file = _write_parquet(rows, tmp_path / "trace.parquet")
    pedestal = get_pedestal("otel-demo")

    stat = preprocess_trace(file, pedestal)

    assert "user_browse_product" in stat
    assert "ingress" not in stat
    assert len(stat["user_browse_product"]["timestamp"]) == 60
