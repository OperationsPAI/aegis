"""Per-system adapter contracts (§9.2).

Layer 2 (the IR adapter family) defines Protocol shapes; Layer 3 supplies
per-system implementations. The first contract materialized here is
``FailureDetector`` — the ERRORING-state seam that the methodology
explicitly calls out as currently mixed into ``TraceStateAdapter``.

Why ``is_failure_expr() -> pl.Expr`` rather than a per-row callable:
``TraceStateAdapter`` aggregates spans inside polars groupby/agg. A
vectorized ``Expr`` keeps the adapter on its lazy/columnar path and lets
detectors compose via ``OrFailureDetector`` without round-tripping
through Python.

A column-presence filter (``filter_by_columns``) is provided because
polars raises ``ColumnNotFoundError`` when an ``Expr`` references a
column missing from the frame. The default detector composition
includes HTTP + gRPC even though gRPC columns are absent from most
parquet test fixtures; the filter trims unsupported detectors at
adapter-init time so the default works on the existing schema.
"""

from __future__ import annotations

from typing import Iterable, Protocol, runtime_checkable

import polars as pl


@runtime_checkable
class FailureDetector(Protocol):
    """Per-system contract for ERRORING-state detection (§9.2).

    ``is_failure_expr`` returns a polars ``Expr`` that evaluates to True
    iff the span row represents a failed call. Implementations should
    encode null-handling internally (e.g. ``col.is_not_null() & ...``).

    ``required_columns`` advertises which dataframe columns the
    expression references. The adapter uses this to drop detectors
    whose columns are absent from the input parquet schema; without
    this guard, polars would raise ``ColumnNotFoundError`` at evaluation
    time. Implementations that reference no columns may return an empty
    iterable.
    """

    def is_failure_expr(self) -> pl.Expr: ...

    def required_columns(self) -> Iterable[str]: ...


class HTTPFailureDetector:
    """status_code >= 500 — the methodology's HTTP fallback."""

    def is_failure_expr(self) -> pl.Expr:
        return pl.col("attr.http.response.status_code").is_not_null() & (
            pl.col("attr.http.response.status_code") >= 500
        )

    def required_columns(self) -> Iterable[str]:
        return ("attr.http.response.status_code",)


class GRPCFailureDetector:
    """grpc.status_code != 0 — gRPC fallback (0 is OK)."""

    def is_failure_expr(self) -> pl.Expr:
        return pl.col("attr.grpc.status_code").is_not_null() & (pl.col("attr.grpc.status_code") != 0)

    def required_columns(self) -> Iterable[str]:
        return ("attr.grpc.status_code",)


class ExceptionEventFailureDetector:
    """Span carries an exception event (``attr.exception.type`` non-null).

    OTel records exceptions as span events; the flattened parquet
    representation stores the event's ``exception.type`` attribute on
    the span row. If your schema flattens events differently, register
    a per-system detector instead of relying on this one.
    """

    def is_failure_expr(self) -> pl.Expr:
        return pl.col("attr.exception.type").is_not_null()

    def required_columns(self) -> Iterable[str]:
        return ("attr.exception.type",)


class OrFailureDetector:
    """Compose detectors with OR — methodology's default 'OR-of' fallback.

    An empty composition returns a constant False expression so the
    aggregation pipeline still produces a valid boolean column when no
    detector is applicable to the current schema.
    """

    def __init__(self, *detectors: FailureDetector) -> None:
        self._detectors: tuple[FailureDetector, ...] = detectors

    @property
    def detectors(self) -> tuple[FailureDetector, ...]:
        return self._detectors

    def is_failure_expr(self) -> pl.Expr:
        if not self._detectors:
            return pl.lit(False)
        expr = self._detectors[0].is_failure_expr()
        for d in self._detectors[1:]:
            expr = expr | d.is_failure_expr()
        return expr

    def required_columns(self) -> Iterable[str]:
        cols: list[str] = []
        for d in self._detectors:
            cols.extend(d.required_columns())
        return tuple(cols)


def filter_by_columns(detector: FailureDetector, available_columns: Iterable[str]) -> FailureDetector:
    """Drop detectors whose required columns are missing from ``available_columns``.

    For ``OrFailureDetector``, recursively trims its child detectors and
    rebuilds the composition. For atomic detectors, returns the detector
    unchanged if all its required columns are present, else returns an
    empty ``OrFailureDetector`` (the safe constant-False fallback).
    """
    avail = set(available_columns)
    if isinstance(detector, OrFailureDetector):
        kept = [d for d in detector.detectors if all(c in avail for c in d.required_columns())]
        return OrFailureDetector(*kept)
    if all(c in avail for c in detector.required_columns()):
        return detector
    return OrFailureDetector()


def default_failure_detector() -> FailureDetector:
    """Methodology default: HTTP OR gRPC.

    ``ExceptionEventFailureDetector`` is intentionally excluded by
    default — its column presence isn't guaranteed across the existing
    parquet fixtures, and adding it silently inflates ERRORING when the
    column happens to be present for unrelated reasons.
    """
    return OrFailureDetector(HTTPFailureDetector(), GRPCFailureDetector())


# ---------------------------------------------------------------------------
# Per-system registry
# ---------------------------------------------------------------------------
# PR#5 puts the registry in place but does NOT yet wire it into cli.py to
# consume ``injection.system_code``. The activation lands in a follow-up.

_FAILURE_DETECTOR_REGISTRY: dict[str, FailureDetector] = {}


def register_failure_detector(system_code: str, detector: FailureDetector) -> None:
    """Register a per-system FailureDetector. Replaces any prior entry."""
    _FAILURE_DETECTOR_REGISTRY[system_code] = detector


def get_failure_detector(system_code: str | None) -> FailureDetector:
    """Look up a registered detector, falling back to the default OR composition."""
    if system_code and system_code in _FAILURE_DETECTOR_REGISTRY:
        return _FAILURE_DETECTOR_REGISTRY[system_code]
    return default_failure_detector()


def _clear_failure_detector_registry_for_tests() -> None:
    _FAILURE_DETECTOR_REGISTRY.clear()


__all__ = [
    "ExceptionEventFailureDetector",
    "FailureDetector",
    "GRPCFailureDetector",
    "HTTPFailureDetector",
    "OrFailureDetector",
    "_clear_failure_detector_registry_for_tests",
    "default_failure_detector",
    "filter_by_columns",
    "get_failure_detector",
    "register_failure_detector",
]
