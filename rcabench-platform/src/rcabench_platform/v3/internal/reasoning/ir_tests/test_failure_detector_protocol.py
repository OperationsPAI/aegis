"""FailureDetector Protocol (§9.2) — defaults, registry, adapter wiring.

Covers:
- HTTPFailureDetector flags status_code >= 500, leaves null/2xx alone.
- Default OR composition picks up gRPC fallback when only the gRPC
  status column is populated.
- Per-system registry returns custom detectors and composes app-level
  failure signals.
- Empty OrFailureDetector is safe (constant False) and never raises on
  evaluation.
- TraceStateAdapter wires in the configured detector and the default
  matches HTTP-500 behavior bit-for-bit.
"""

from __future__ import annotations

from pathlib import Path

import polars as pl

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.traces import TraceStateAdapter
from rcabench_platform.v3.internal.reasoning.ir.protocols import (
    FailureDetector,
    GRPCFailureDetector,
    HTTPFailureDetector,
    OrFailureDetector,
    _clear_failure_detector_registry_for_tests,
    default_failure_detector,
    get_failure_detector,
    register_failure_detector,
)
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="failure-detector-fixture")


def _eval(detector: FailureDetector, df: pl.DataFrame) -> list[bool]:
    out = df.with_columns(detector.is_failure_expr().alias("e")).select("e").to_series().to_list()
    return [bool(x) if x is not None else False for x in out]


# ---------------------------------------------------------------------------
# Test 1 — default detector flags HTTP 500 and only HTTP 500.
# ---------------------------------------------------------------------------
def test_default_flags_http_500() -> None:
    df = pl.DataFrame(
        {
            "attr.http.response.status_code": [200, 500, None],
            "attr.grpc.status_code": [None, None, None],
        },
        schema={"attr.http.response.status_code": pl.Int64, "attr.grpc.status_code": pl.Int64},
    )
    assert _eval(default_failure_detector(), df) == [False, True, False]


# ---------------------------------------------------------------------------
# Test 2 — gRPC fallback fires when HTTP column is null but gRPC is non-zero.
# ---------------------------------------------------------------------------
def test_default_picks_up_grpc_nonzero_when_http_null() -> None:
    df = pl.DataFrame(
        {
            "attr.http.response.status_code": [None, 200, None],
            "attr.grpc.status_code": [13, 0, None],
        },
        schema={"attr.http.response.status_code": pl.Int64, "attr.grpc.status_code": pl.Int64},
    )
    # Row 0: gRPC=13 (failure)  Row 1: HTTP=200 + gRPC=0 (ok)  Row 2: both null (ok)
    assert _eval(default_failure_detector(), df) == [True, False, False]


# ---------------------------------------------------------------------------
# Test 3 — per-system registered detector composes an app-level signal.
# ---------------------------------------------------------------------------
class _AppErrorCodeDetector:
    """TT-style: the application emits ``attr.app.error.code`` for soft
    failures that don't surface as HTTP 5xx (e.g. AUTH_FAIL → 401)."""

    def is_failure_expr(self) -> pl.Expr:
        return pl.col("attr.app.error.code").is_not_null()

    def required_columns(self):  # type: ignore[no-untyped-def]
        return ("attr.app.error.code",)


def test_registered_per_system_detector_returned_by_lookup() -> None:
    _clear_failure_detector_registry_for_tests()
    try:
        custom = OrFailureDetector(HTTPFailureDetector(), GRPCFailureDetector(), _AppErrorCodeDetector())
        register_failure_detector("trainticket", custom)

        # Lookup roundtrips the exact instance.
        assert get_failure_detector("trainticket") is custom

        # Unknown system_code falls back to default.
        fallback = get_failure_detector("not-registered")
        assert isinstance(fallback, OrFailureDetector)
        # Default has 2 detectors (HTTP + gRPC), not 3.
        assert len(fallback.detectors) == 2

        # None also falls back to default.
        assert isinstance(get_failure_detector(None), OrFailureDetector)

        df = pl.DataFrame(
            {
                "attr.http.response.status_code": [None],
                "attr.grpc.status_code": [None],
                "attr.app.error.code": ["AUTH_FAIL"],
            },
            schema={
                "attr.http.response.status_code": pl.Int64,
                "attr.grpc.status_code": pl.Int64,
                "attr.app.error.code": pl.Utf8,
            },
        )
        assert _eval(custom, df) == [True]
    finally:
        _clear_failure_detector_registry_for_tests()


# ---------------------------------------------------------------------------
# Test 4 — empty OrFailureDetector evaluates to all-False.
# ---------------------------------------------------------------------------
def test_empty_or_failure_detector_is_constant_false() -> None:
    empty = OrFailureDetector()
    df = pl.DataFrame({"x": [1, 2, 3]})
    assert _eval(empty, df) == [False, False, False]


# ---------------------------------------------------------------------------
# Test 5 — TraceStateAdapter stores the configured FailureDetector.
# Default ctor: gRPC column absent in fixture, so filter_by_columns drops
# GRPCFailureDetector and the stored detector keeps only HTTP.
# ---------------------------------------------------------------------------
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


def test_trace_adapter_default_filters_to_http_only_when_grpc_column_absent() -> None:
    base = pl.DataFrame([_row(1000 + i, "front-end", "GET /api", 50_000_000, 200) for i in range(40)])
    abn = pl.DataFrame([_row(2000 + i % 3, "front-end", "GET /api", 50_000_000, 500) for i in range(20)])
    adapter = TraceStateAdapter(base, abn, window_sec=3)
    detector = adapter._failure_detector  # type: ignore[attr-defined]
    assert isinstance(detector, OrFailureDetector)
    # Only HTTPFailureDetector survived filter_by_columns (no grpc col).
    assert len(detector.detectors) == 1
    assert isinstance(detector.detectors[0], HTTPFailureDetector)

    # And ERRORING is still emitted exactly like before the refactor.
    events = [e for e in adapter.emit(CTX) if e.kind == PlaceKind.span]
    errs = [e for e in events if e.to_state == "erroring"]
    assert errs, events
    assert errs[0].evidence.get("trigger_metric") == "error_rate"


# ---------------------------------------------------------------------------
# Test 6 — TraceStateAdapter accepts a custom detector and stores it as-is.
# Custom detector flips the rule (status_code < 500 = "failure"); we only
# assert that the adapter's stored reference matches what was passed in.
# ---------------------------------------------------------------------------
class _InvertedHTTPDetector:
    def is_failure_expr(self) -> pl.Expr:
        return pl.col("attr.http.response.status_code").is_not_null() & (pl.col("attr.http.response.status_code") < 500)

    def required_columns(self):  # type: ignore[no-untyped-def]
        return ("attr.http.response.status_code",)


def test_trace_adapter_custom_detector_is_stored_unchanged() -> None:
    base = pl.DataFrame([_row(1000 + i, "front-end", "GET /api", 50_000_000, 200) for i in range(40)])
    abn = pl.DataFrame([_row(2000 + i % 3, "front-end", "GET /api", 50_000_000, 200) for i in range(20)])
    custom = _InvertedHTTPDetector()
    adapter = TraceStateAdapter(base, abn, window_sec=3, failure_detector=custom)
    # filter_by_columns leaves an atomic detector unchanged when its
    # columns are present.
    assert adapter._failure_detector is custom  # type: ignore[attr-defined]
