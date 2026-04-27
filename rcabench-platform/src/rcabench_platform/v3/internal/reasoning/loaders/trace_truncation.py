"""Structural trace-truncation alarm signal.

Detects abnormal traces whose downstream subtree is structurally absent —
even when no descendant span carries an error attribute. The signal is the
*shape* of the trace versus the per-endpoint baseline shape distribution,
not its attribute payload.

Forensic motivation (4 silent-injection TT cases on
``ts5-ts-{contacts,travel-plan,verification-code,preserve}-service``): the
abnormal traces collapse to ~2 spans (loadgen Client + frontend Server)
while the baseline median for the same endpoint is 4-411 spans. No
descendant carries HTTP 5xx or OTel ``Error`` status, so attribute-based
alarm detection (latency / error / missing span) misses them. Structural
divergence is the only signal.

Pipeline (per detector call):

  1. Per-trace signature: ``span_count``, ``service_set``,
     ``service_edge_set``, and ``topmost_non_loadgen`` (the §7.1
     alarm-root pivot, used to derive the endpoint key).
  2. Per-endpoint baseline profile: span-count percentiles, top-K
     canonical service-set / edge-set shapes, ubiquitous services /
     edges. Endpoints with fewer than ``MIN_BASELINE_TRACES`` baseline
     traces are skipped — too noisy to compare.
  3. Per-abnormal-trace scoring across 4 binary signals (S1..S4); a
     trace is flagged iff at least ``SIGNAL_SCORE_GATE`` signals fire.
  4. Per-endpoint aggregation: an endpoint becomes an alarm iff its
     truncated-trace fraction crosses ``TRUNCATION_RATE_THRESHOLD`` AND
     at least ``MIN_FAILED_TRACES`` truncated traces exist.

Loadgen exclusion: when computing service_set / service_edge_set we
drop spans whose service is in :data:`LOADGEN_SERVICES` (lowercased).
Synthetic loadgen ingress is not part of the structural shape.

The detector is a pure function over polars DataFrames and emits a
sidecar :class:`TruncationAlarmInfo` dict that
``ParquetDataLoader.identify_alarm_nodes_v2`` stashes for downstream
consumers.
"""

from __future__ import annotations

import logging
from collections import Counter
from dataclasses import dataclass, field
from typing import TYPE_CHECKING

import polars as pl

if TYPE_CHECKING:
    pass

logger = logging.getLogger(__name__)


MIN_BASELINE_TRACES = 5
MAX_CANONICAL_SHAPES = 20
CANONICAL_SHAPES_COVERAGE = 0.95
UBIQUITY_THRESHOLD = 0.95
JACCARD_DIVERGENCE_THRESHOLD = 0.5
SIGNAL_SCORE_GATE = 3
TRUNCATION_RATE_THRESHOLD = 0.05
MIN_FAILED_TRACES = 3


@dataclass(frozen=True, slots=True)
class TruncationAlarmInfo:
    """Sidecar metadata for an endpoint flagged as structurally truncated."""

    endpoint: str  # "service::span_name"
    failed_count: int
    total_abnormal_count: int
    typical_baseline_services: tuple[str, ...]  # most-common baseline service_set
    failed_services: tuple[str, ...]  # union of services across truncated traces
    missing_services: tuple[str, ...]  # ubiquitous baseline services NOT in failed


@dataclass(frozen=True, slots=True)
class BaselineProfile:
    """Per-endpoint baseline structural fingerprint.

    All collections are deterministically ordered (descending frequency
    for canonical shapes; lexicographic for sets) so that downstream
    fixtures and snapshots remain stable across runs.
    """

    endpoint: str
    trace_count: int
    span_count_p1: float
    span_count_p10: float
    span_count_p50: float
    canonical_shapes: tuple[frozenset[str], ...]
    canonical_edge_shapes: tuple[frozenset[tuple[str, str]], ...]
    ubiquitous_services: frozenset[str]
    ubiquitous_edges: frozenset[tuple[str, str]]
    most_common_service_set: frozenset[str] = field(default_factory=frozenset)


# ---------------------------------------------------------------------------
# Per-trace signature extraction (polars-vectorised over spans)
# ---------------------------------------------------------------------------


def _build_trace_signatures(
    df: pl.DataFrame,
    loadgen_services: frozenset[str],
) -> pl.DataFrame:
    """Return one row per ``trace_id`` carrying:

    - ``span_count`` (int): total span count in the trace (loadgen
      INCLUDED — span_count is a raw cardinality signal, not a shape)
    - ``service_set`` (list[str]): distinct non-loadgen services
    - ``service_edge_set`` (list[struct{parent, child}]): distinct
      ``(parent_service, child_service)`` pairs derived from in-trace
      ``parent_span_id``→``span_id`` linkage, with parent loadgen
      pairs DROPPED.
    - ``endpoint`` (str): ``"{service}::{span_name}"`` of the topmost
      non-loadgen span (smallest ``time`` if multiple). Empty string if
      the trace has no non-loadgen span at all (rare; e.g. pure-loadgen
      synthetic trace) — those traces are dropped at the join stage.

    All aggregation is polars-side; no Python per-row loops.
    """
    if len(df) == 0:
        # Build an empty frame with the right schema so the join below
        # never sees a missing column.
        return pl.DataFrame(
            schema={
                "trace_id": pl.Utf8,
                "span_count": pl.UInt32,
                "service_set": pl.List(pl.Utf8),
                "service_edge_set": pl.List(pl.Struct([pl.Field("parent", pl.Utf8), pl.Field("child", pl.Utf8)])),
                "endpoint": pl.Utf8,
            }
        )

    loadgens = list(loadgen_services)

    # 1. Span count per trace (loadgen included — raw cardinality).
    span_count_df = df.group_by("trace_id").agg(pl.len().alias("span_count"))

    # 2. Distinct non-loadgen services per trace.
    non_loadgen = df.filter(~pl.col("service_name").str.to_lowercase().is_in(loadgens))
    service_set_df = non_loadgen.group_by("trace_id").agg(pl.col("service_name").unique().sort().alias("service_set"))

    # 3. Edge set per trace via self-join on (trace_id, parent_span_id) → (trace_id, span_id).
    parents = df.select(
        pl.col("trace_id"),
        pl.col("span_id").alias("parent_span_id"),
        pl.col("service_name").alias("parent_service"),
    )
    edges = (
        df.select(
            pl.col("trace_id"),
            pl.col("parent_span_id"),
            pl.col("service_name").alias("child_service"),
        )
        .filter(pl.col("parent_span_id") != "")
        .join(parents, on=["trace_id", "parent_span_id"], how="inner")
        .filter(~pl.col("parent_service").str.to_lowercase().is_in(loadgens))
        .filter(pl.col("parent_service") != pl.col("child_service"))  # drop intra-service self-loops
        .select(
            pl.col("trace_id"),
            pl.struct(
                [
                    pl.col("parent_service").alias("parent"),
                    pl.col("child_service").alias("child"),
                ]
            ).alias("edge"),
        )
        .unique()
    )
    edge_set_df = edges.group_by("trace_id").agg(pl.col("edge").alias("service_edge_set"))

    # 4. Topmost non-loadgen endpoint per trace via the §7.1 root-alarm filter.
    #    We re-derive in-line (avoid circular import on filter_root_alarm_candidate_spans).
    parent_services = df.select(
        pl.col("trace_id"),
        pl.col("span_id").alias("parent_span_id"),
        pl.col("service_name").alias("parent_service_name"),
    )
    candidates = (
        non_loadgen.join(parent_services, on=["trace_id", "parent_span_id"], how="left")
        .filter(
            pl.col("parent_service_name").is_null() | pl.col("parent_service_name").str.to_lowercase().is_in(loadgens)
        )
        .with_columns((pl.col("service_name") + "::" + pl.col("span_name")).alias("endpoint"))
        .sort(["trace_id", "time"])
        .group_by("trace_id")
        .agg(pl.col("endpoint").first().alias("endpoint"))
    )

    out = (
        span_count_df.join(candidates, on="trace_id", how="left")
        .join(service_set_df, on="trace_id", how="left")
        .join(edge_set_df, on="trace_id", how="left")
    )
    # Fill nulls for traces with zero non-loadgen activity / zero non-self edges.
    out = out.with_columns(
        pl.col("service_set").fill_null(pl.lit([], dtype=pl.List(pl.Utf8))),
        pl.col("service_edge_set").fill_null(
            pl.lit(
                [],
                dtype=pl.List(pl.Struct([pl.Field("parent", pl.Utf8), pl.Field("child", pl.Utf8)])),
            )
        ),
    )
    return out


def _row_service_set(row: dict) -> frozenset[str]:
    return frozenset(row.get("service_set") or [])


def _row_edge_set(row: dict) -> frozenset[tuple[str, str]]:
    edges = row.get("service_edge_set") or []
    out: set[tuple[str, str]] = set()
    for e in edges:
        # polars struct rows materialise as dict
        if isinstance(e, dict):
            p = e.get("parent")
            c = e.get("child")
        else:
            p = getattr(e, "parent", None)
            c = getattr(e, "child", None)
        if p is not None and c is not None:
            out.add((str(p), str(c)))
    return frozenset(out)


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def _loadgen_services_lowered() -> frozenset[str]:
    # Lazy import to avoid the (small) circular risk; LOADGEN_SERVICES
    # itself only depends on Python builtins.
    from .parquet_loader import LOADGEN_SERVICES

    return frozenset(s.lower() for s in LOADGEN_SERVICES)


def build_baseline_profile(baseline_traces: pl.DataFrame) -> dict[str, BaselineProfile]:
    """Build a per-endpoint baseline structural profile.

    Endpoints with fewer than ``MIN_BASELINE_TRACES`` baseline traces are
    excluded — Jaccard / ubiquity numbers on tiny populations are too
    noisy.
    """
    loadgens = _loadgen_services_lowered()
    sigs = _build_trace_signatures(baseline_traces, loadgens)
    if len(sigs) == 0:
        return {}

    # Drop traces without an identified endpoint (pure-loadgen / no
    # non-loadgen span).
    sigs = sigs.filter(pl.col("endpoint").is_not_null() & (pl.col("endpoint") != ""))
    if len(sigs) == 0:
        return {}

    profiles: dict[str, BaselineProfile] = {}
    for endpoint, group in sigs.group_by("endpoint"):
        # polars 1.x returns endpoint as tuple[str] from group_by("endpoint")
        ep = endpoint[0] if isinstance(endpoint, tuple) else endpoint
        ep_str = str(ep)
        if len(group) < MIN_BASELINE_TRACES:
            continue

        rows = group.to_dicts()
        n = len(rows)
        span_counts = [int(r["span_count"]) for r in rows]
        span_counts.sort()
        p1 = _percentile(span_counts, 0.01)
        p10 = _percentile(span_counts, 0.10)
        p50 = _percentile(span_counts, 0.50)

        service_sets = [_row_service_set(r) for r in rows]
        edge_sets = [_row_edge_set(r) for r in rows]

        canonical_shapes = _top_k_shapes(service_sets)
        canonical_edge_shapes = _top_k_shapes(edge_sets)

        # Ubiquitous services / edges: appear in >= UBIQUITY_THRESHOLD of traces.
        service_freq: Counter[str] = Counter()
        for s in service_sets:
            service_freq.update(s)
        ubiq_threshold_count = UBIQUITY_THRESHOLD * n
        ubiquitous_services = frozenset(svc for svc, cnt in service_freq.items() if cnt >= ubiq_threshold_count)

        edge_freq: Counter[tuple[str, str]] = Counter()
        for es in edge_sets:
            edge_freq.update(es)
        ubiquitous_edges = frozenset(e for e, cnt in edge_freq.items() if cnt >= ubiq_threshold_count)

        most_common = canonical_shapes[0] if canonical_shapes else frozenset()

        profiles[ep_str] = BaselineProfile(
            endpoint=ep_str,
            trace_count=n,
            span_count_p1=p1,
            span_count_p10=p10,
            span_count_p50=p50,
            canonical_shapes=tuple(canonical_shapes),
            canonical_edge_shapes=tuple(canonical_edge_shapes),
            ubiquitous_services=ubiquitous_services,
            ubiquitous_edges=ubiquitous_edges,
            most_common_service_set=most_common,
        )

    return profiles


def detect_truncated_endpoints(
    abnormal_traces: pl.DataFrame,
    baseline_profile: dict[str, BaselineProfile],
) -> dict[str, TruncationAlarmInfo]:
    """Score each abnormal trace against its endpoint's baseline profile.

    Returns a dict from endpoint full_span_name → :class:`TruncationAlarmInfo`
    for endpoints that crossed both the truncation-rate and
    minimum-failed-count thresholds.
    """
    if not baseline_profile:
        return {}

    loadgens = _loadgen_services_lowered()
    sigs = _build_trace_signatures(abnormal_traces, loadgens)
    if len(sigs) == 0:
        return {}

    sigs = sigs.filter(pl.col("endpoint").is_not_null() & (pl.col("endpoint") != ""))
    if len(sigs) == 0:
        return {}

    alarms: dict[str, TruncationAlarmInfo] = {}

    for endpoint, group in sigs.group_by("endpoint"):
        ep = endpoint[0] if isinstance(endpoint, tuple) else endpoint
        ep_str = str(ep)
        prof = baseline_profile.get(ep_str)
        if prof is None:
            continue

        rows = group.to_dicts()
        total = len(rows)
        failed = 0
        failed_service_union: set[str] = set()
        truncated_rows: list[dict] = []

        for r in rows:
            span_count = int(r["span_count"])
            svc_set = _row_service_set(r)
            edge_set = _row_edge_set(r)

            # S1: span count low
            s1 = int(span_count <= max(2, prof.span_count_p1))

            # S2: service set diverged from all canonical shapes
            s2_jacc = (
                max((_jaccard(svc_set, shape) for shape in prof.canonical_shapes), default=0.0)
                if prof.canonical_shapes
                else 0.0
            )
            s2 = int(s2_jacc < JACCARD_DIVERGENCE_THRESHOLD)

            # S3: edge set diverged
            s3_jacc = (
                max((_jaccard(edge_set, shape) for shape in prof.canonical_edge_shapes), default=0.0)
                if prof.canonical_edge_shapes
                else 0.0
            )
            s3 = int(s3_jacc < JACCARD_DIVERGENCE_THRESHOLD)

            # S4: missing ubiquitous services
            s4 = int(len(prof.ubiquitous_services - svc_set) >= 1)

            score = s1 + s2 + s3 + s4
            if score >= SIGNAL_SCORE_GATE:
                failed += 1
                truncated_rows.append(r)
                failed_service_union.update(svc_set)

        if total == 0:
            continue
        rate = failed / total
        if failed < MIN_FAILED_TRACES or rate < TRUNCATION_RATE_THRESHOLD:
            continue

        missing = prof.ubiquitous_services - failed_service_union

        alarms[ep_str] = TruncationAlarmInfo(
            endpoint=ep_str,
            failed_count=failed,
            total_abnormal_count=total,
            typical_baseline_services=tuple(sorted(prof.most_common_service_set)),
            failed_services=tuple(sorted(failed_service_union)),
            missing_services=tuple(sorted(missing)),
        )

    return alarms


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _jaccard(a: frozenset, b: frozenset) -> float:
    """Jaccard with the spec's empty-set convention.

    Empty vs empty = 1.0 (vacuous match — used downstream for endpoints
    where baseline edges genuinely don't exist). Empty vs non-empty = 0.0
    (per spec). Non-empty vs non-empty = |∩| / |∪|.
    """
    if not a and not b:
        return 1.0
    union = a | b
    if not union:
        return 0.0
    return len(a & b) / len(union)


def _percentile(sorted_values: list[int], q: float) -> float:
    """Linear-interp percentile on a pre-sorted list. Empty → 0.0."""
    if not sorted_values:
        return 0.0
    n = len(sorted_values)
    if n == 1:
        return float(sorted_values[0])
    pos = q * (n - 1)
    lo = int(pos)
    hi = min(lo + 1, n - 1)
    frac = pos - lo
    return float(sorted_values[lo] * (1 - frac) + sorted_values[hi] * frac)


def _top_k_shapes(shapes: list[frozenset]) -> list[frozenset]:
    """Top-K most frequent shapes covering ≥ ``CANONICAL_SHAPES_COVERAGE``
    of the input population, capped at ``MAX_CANONICAL_SHAPES``.

    Returned in descending-frequency order (ties broken by sorted-tuple
    of the shape contents for determinism).
    """
    if not shapes:
        return []
    counter: Counter[frozenset] = Counter(shapes)
    n = len(shapes)
    target = CANONICAL_SHAPES_COVERAGE * n
    items = sorted(counter.items(), key=lambda kv: (-kv[1], tuple(sorted(map(str, kv[0])))))
    out: list[frozenset] = []
    cumulative = 0
    for shape, cnt in items:
        out.append(shape)
        cumulative += cnt
        if cumulative >= target or len(out) >= MAX_CANONICAL_SHAPES:
            break
    return out
