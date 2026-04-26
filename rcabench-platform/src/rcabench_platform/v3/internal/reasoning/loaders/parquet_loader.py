import logging
import re
from collections.abc import Mapping
from datetime import datetime
from pathlib import Path
from typing import Any

import numpy as np
import polars as pl

from ..algorithms.baseline_detector import get_adaptive_threshold
from ..models.graph import CallsEdgeData, DepKind, Edge, HyperGraph, Node, PlaceKind
from .utils import fmap_threadpool, timeit

logger = logging.getLogger(__name__)


PATTERN_REPLACEMENTS = [
    # UUID patterns (must come before shorter hex patterns)
    (
        r"(.*?)/api/v1/contactservice/contacts/account/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
        r"$1/api/v1/contactservice/contacts/account/{accountId}",
    ),
    (
        r"(.*?)/api/v1/userservice/users/id/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
        r"$1/api/v1/userservice/users/id/{userId}",
    ),
    (
        r"(.*?)/api/v1/consignservice/consigns/order/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
        r"$1/api/v1/consignservice/consigns/order/{id}",
    ),
    (
        r"(.*?)/api/v1/consignservice/consigns/account/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
        r"$1/api/v1/consignservice/consigns/account/{id}",
    ),
    # Date and multi-parameter patterns
    (
        r"(.*?)GET (.*?)/api/v1/foodservice/foods/[0-9]{4}-[0-9]{2}-[0-9]{2}/[a-z]+/[a-z]+/[A-Z0-9]+",
        r"$1GET $2/api/v1/foodservice/foods/{date}/{startStation}/{endStation}/{tripId}",
    ),
    # Alphanumeric ID patterns
    (
        r"(.*?)GET (.*?)/api/v1/verifycode/verify/[0-9a-zA-Z]+",
        r"$1GET $2/api/v1/verifycode/verify/{verifyCode}",
    ),
    # Fallback hex patterns for shorter IDs
    (
        r"(.*?)GET (.*?)/api/v1/contactservice/contacts/account/[0-9a-f]+",
        r"$1GET $2/api/v1/contactservice/contacts/account/{accountId}",
    ),
    (
        r"(.*?)GET (.*?)/api/v1/userservice/users/id/[0-9a-f]+",
        r"$1GET $2/api/v1/userservice/users/id/{userId}",
    ),
    (
        r"(.*?)GET (.*?)/api/v1/consignservice/consigns/order/[0-9a-f]+",
        r"$1GET $2/api/v1/consignservice/consigns/order/{id}",
    ),
    (
        r"(.*?)GET (.*?)/api/v1/consignservice/consigns/account/[0-9a-f]+",
        r"$1GET $2/api/v1/consignservice/consigns/account/{id}",
    ),
    (
        r"(.*?)GET (.*?)/api/v1/executeservice/execute/collected/[0-9a-f]+",
        r"$1GET $2/api/v1/executeservice/execute/collected/{orderId}",
    ),
    # cancelservice with full UUID patterns (must come before shorter hex patterns)
    (
        r"(.*?)/api/v1/cancelservice/cancel/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
        r"$1/api/v1/cancelservice/cancel/{orderId}/{loginId}",
    ),
    (
        r"(.*?)/api/v1/cancelservice/cancel/refound/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
        r"$1/api/v1/cancelservice/cancel/refound/{orderId}",
    ),
    (
        r"(.*?)/api/v1/cancelservice/cancel/refound/[0-9a-f]+",
        r"$1/api/v1/cancelservice/cancel/refound/{orderId}",
    ),
    (
        r"(.*?)GET (.*?)/api/v1/executeservice/execute/execute/[0-9a-f]+",
        r"$1GET $2/api/v1/executeservice/execute/execute/{orderId}",
    ),
    (
        r"(.*?)DELETE (.*?)/api/v1/adminorderservice/adminorder/[0-9a-f]+/[A-Z0-9]+",
        r"$1DELETE $2/api/v1/adminorderservice/adminorder/{orderId}/{trainNumber}",
    ),
    (
        r"(.*?)DELETE (.*?)/api/v1/adminrouteservice/adminroute/[0-9a-f]+",
        r"$1DELETE $2/api/v1/adminrouteservice/adminroute/{routeId}",
    ),
]

# Compile regex patterns once at module load for performance
COMPILED_PATTERNS = [(re.compile(pattern), replacement) for pattern, replacement in PATTERN_REPLACEMENTS]

# Generic HTTP method span names without endpoints to filter out (exact match, case-insensitive)
# These are spans that only contain the HTTP method without any path information
GENERIC_HTTP_METHOD_SPANS = {
    "GET",
    "POST",
    "PUT",
    "DELETE",
    "PATCH",
    "HEAD",
    "OPTIONS",
    "CONNECT",
    "TRACE",
    "HTTP GET",
    "HTTP POST",
    "HTTP PUT",
    "HTTP DELETE",
    "HTTP PATCH",
    "HTTP HEAD",
    "HTTP OPTIONS",
    "HTTP CONNECT",
    "HTTP TRACE",
}


def is_generic_http_method_span(span_name: str) -> bool:
    """
    Check if a span name is a pure HTTP method without any endpoint path.

    Only filters out spans like 'GET', 'POST', etc. that don't include
    any path information. Spans like 'GET /api/v1/...' are NOT filtered.

    Args:
        span_name: The span name to check

    Returns:
        True if this is a generic HTTP method span that should be filtered out
    """
    if span_name is None:
        return True

    # Check exact match (case-insensitive)
    return span_name.upper().strip() in GENERIC_HTTP_METHOD_SPANS


# Purely synthetic traffic sources — excluded from root-span alarm detection because
# their baselines don't reflect real user behavior. Kept separate from CALLER_LIKE_SERVICES
# since frontend aggregators ARE the real user entry points and should participate in
# alarm detection even though they're "callers" for span-service disambiguation.
LOADGEN_LIKE_SERVICES: set[str] = {
    "loadgenerator",
    "load-generator",
    "locust",
    "wrk2",
    "dsb-wrk2",
    "k6",
}

# Methodology-spec name (§7.1) for the same set; provided as an alias so future
# PRs can refer to LOADGEN_SERVICES without renaming.
LOADGEN_SERVICES: set[str] = LOADGEN_LIKE_SERVICES

# Frontend aggregators + loadgens. Used when multiple services share the same span_name
# to prefer the backend service that owns the URL path rather than the caller.
CALLER_LIKE_SERVICES: set[str] = LOADGEN_LIKE_SERVICES | {
    "ts-ui-dashboard",  # TrainTicket frontend
    "front-end",  # SockShop / otel-demo
    "frontend",
}


def _is_caller_like(service_name: str) -> bool:
    return service_name.lower() in CALLER_LIKE_SERVICES


def is_root_server_row(span: Mapping[str, Any], trace_index: dict[tuple[str, str], dict]) -> bool:
    """Methodology §7.1 ``is_root_server`` predicate, implemented as a
    Python function over a span-row dict.

    A span is a root Server iff:
      * its kind is ``Server``,
      * its owner service is NOT in ``LOADGEN_SERVICES``, and
      * its parent (looked up via ``(trace_id, parent_span_id) -> span``)
        is either missing (true root) or NOT itself ``Server``-kind.

    The "parent not Server-kind" rule is broader than the spec's "parent
    in LOADGEN_SERVICES" clause and subsumes it: a loadgen Client parent
    is non-Server, so it always passes; a Server-kind parent (whether
    loadgen or not) is always rejected.
    """
    if span.get("attr.span_kind") != "Server":
        return False
    service = span.get("service_name") or ""
    if service.lower() in LOADGEN_SERVICES:
        return False
    parent_span_id = span.get("parent_span_id") or ""
    if not parent_span_id:
        return True
    trace_id = span.get("trace_id") or ""
    parent = trace_index.get((trace_id, parent_span_id))
    if parent is None:
        return True
    return parent.get("attr.span_kind") != "Server"


def filter_root_server_spans(df: pl.DataFrame) -> pl.DataFrame:
    """Keep only the topmost Server-kind span per trace (methodology §7.1).

    Implements the ``is_root_server`` predicate as a polars anti-join:
    a Server-kind span is kept iff its parent (matched on
    ``(trace_id, parent_span_id) -> span_id``) is empty / missing OR its
    parent is NOT itself ``Server``-kind. Services in
    ``LOADGEN_LIKE_SERVICES`` are always excluded.
    """
    loadgens = list(LOADGEN_LIKE_SERVICES)
    server_df = df.filter(
        (pl.col("attr.span_kind") == "Server") & (~pl.col("service_name").str.to_lowercase().is_in(loadgens))
    )
    if len(server_df) == 0:
        return server_df

    # Build a (trace_id, span_id) -> attr.span_kind lookup for ALL spans in
    # the input df (parents may be Client/Internal/etc., not just Server).
    parent_kinds = df.select(
        pl.col("trace_id"),
        pl.col("span_id").alias("parent_span_id"),
        pl.col("attr.span_kind").alias("parent_span_kind"),
    )

    joined = server_df.join(parent_kinds, on=["trace_id", "parent_span_id"], how="left")

    # Keep rows where the parent lookup missed (true root or out-of-trace
    # parent) OR where the parent's kind is not Server.
    return joined.filter(
        pl.col("parent_span_kind").is_null() | (pl.col("parent_span_kind") != "Server")
    ).drop("parent_span_kind")


def _ts_to_int_seconds(values) -> np.ndarray:
    """Coerce a per-pod/container timestamp list to int64 unix-seconds.

    Real OTel parquet emits ``time`` as ``Datetime[ns]``; after a groupby agg the
    list contains ``datetime.datetime`` objects, so a naive ``np.array(...)`` gives
    dtype=object which crashes every downstream ``int(np.min(ts))`` /
    ``range(ts_min, ts_max+1)`` site in the IR adapters. Normalize once at the
    boundary so consumers never have to know what shape the parquet stored.
    """
    arr = np.asarray(values)
    if arr.dtype == np.int64 or arr.dtype == np.int32:
        return arr.astype(np.int64, copy=False)
    if arr.dtype.kind == "M":  # numpy datetime64
        return arr.astype("datetime64[s]").astype(np.int64)
    if arr.dtype == object and arr.size > 0 and isinstance(arr.flat[0], datetime):
        return np.fromiter((int(x.timestamp()) for x in arr), dtype=np.int64, count=arr.size)
    if arr.dtype.kind == "i":
        x = arr.astype(np.int64, copy=False)
        # Heuristic match to ir/adapters/traces._ts_seconds: detect ns/μs/s ints
        if x.size > 0:
            sample = int(x.flat[0])
            if sample > 10**14:
                return x // 1_000_000_000
            if sample > 10**11:
                return x // 1_000
        return x
    return arr.astype(np.int64, copy=False)


def _resolve_span_service(span_name: str, service_names: list[str]) -> str | None:
    """Resolve the correct service for a span when multiple services have the same span_name.

    For URL path spans (e.g., 'POST /api/v1/xxxservice/...'), the span should belong to
    the backend service that matches the URL path, not a caller-like service (frontend /
    loadgen — see ``CALLER_LIKE_SERVICES``).
    """
    if not service_names:
        return None

    if len(service_names) == 1:
        return service_names[0]

    url_match = re.match(r"^(?:GET|POST|PUT|DELETE|PATCH)\s+/api/v\d+/([a-zA-Z0-9_-]+)/", span_name, re.IGNORECASE)
    if url_match:
        service_path = url_match.group(1).lower()
        for svc in service_names:
            if _is_caller_like(svc):
                continue
            # e.g. URL segment "travel2service" matches "ts-travel2-service",
            # "orderservice" matches "ts-order-service"
            service_core = service_path.replace("service", "").replace("_", "").replace("-", "")
            svc_core = svc.lower().replace("ts-", "").replace("-service", "").replace("-", "")
            if service_core == svc_core:
                return svc
        non_caller = [s for s in service_names if not _is_caller_like(s)]
        if non_caller:
            return non_caller[0]

    return service_names[0]


class ParquetDataLoader:
    def __init__(self, data_dir: Path | str = Path("data/converted"), polars_threads: int | None = None):
        self.data_dir = Path(data_dir)

        # Configure Polars thread pool to avoid CPU contention when running multiple processes
        if polars_threads is not None:
            pl.Config.set_streaming_chunk_size(polars_threads)
            pl.Config.set_tbl_rows(polars_threads)

        # Set environment variable for Polars thread pool (affects all operations)
        import os

        if polars_threads is not None:
            os.environ["POLARS_MAX_THREADS"] = str(polars_threads)

        # Cached DataFrames for metric extraction
        self._baseline_metrics: pl.DataFrame | None = None
        self._abnormal_metrics: pl.DataFrame | None = None
        self._baseline_metrics_hist: pl.DataFrame | None = None
        self._abnormal_metrics_hist: pl.DataFrame | None = None
        self._baseline_metrics_sum: pl.DataFrame | None = None
        self._abnormal_metrics_sum: pl.DataFrame | None = None
        self._baseline_traces: pl.DataFrame | None = None
        self._abnormal_traces: pl.DataFrame | None = None
        self._baseline_logs: pl.DataFrame | None = None
        self._abnormal_logs: pl.DataFrame | None = None

    @staticmethod
    def normalize_span_name(span_name: str) -> str:
        """Apply pattern replacements to normalize span names with URL parameters."""
        for compiled_pattern, replacement in COMPILED_PATTERNS:
            span_name = compiled_pattern.sub(replacement, span_name)
        return span_name

    def load_metrics(self, period: str = "abnormal") -> pl.DataFrame:
        """
        Load metrics parquet into DataFrame.

        Args:
            period: Either 'normal' or 'abnormal'

        Returns:
            DataFrame with columns: time, metric, value, service_name, etc.
        """
        path = self.data_dir / f"{period}_metrics.parquet"
        if not path.exists():
            raise FileNotFoundError(f"Metrics file not found: {path}")
        return pl.read_parquet(path)

    def load_traces(self, period: str = "abnormal") -> pl.DataFrame:
        """
        Load traces parquet into DataFrame.

        Args:
            period: Either 'normal' or 'abnormal'

        Returns:
            DataFrame with columns: time, trace_id, span_id, parent_span_id,
            span_name, attr.span_kind, service_name, duration, attr.status_code,
            attr.http.response.status_code, attr.k8s.pod.name, etc.

        Note:
            Span names are automatically normalized to:
            1. Convert URL parameters into template variables (e.g., /users/123 -> /users/{id})
            2. Replace generic HTTP method spans with their first child's name for better semantics
        """
        path = self.data_dir / f"{period}_traces.parquet"
        if not path.exists():
            raise FileNotFoundError(f"Traces file not found: {path}")

        df = pl.read_parquet(path)

        # Normalize span names to reduce cardinality using vectorized string operations
        if "span_name" in df.columns:
            # Apply regex replacements in sequence using Polars native operations
            span_col = pl.col("span_name")
            for pattern, replacement in COMPILED_PATTERNS:
                span_col = span_col.str.replace_all(pattern.pattern, replacement)
            df = df.with_columns(span_col.alias("span_name"))

        # NOTE: _enhance_generic_span_names is disabled because it causes issues:
        # 1. It replaces Client span names with child span names, causing wrong service->span edges
        # 2. It creates self-loop edges when parent and child end up with the same span_name
        # df = self._enhance_generic_span_names(df)

        return df

    def load_logs(self, period: str = "abnormal") -> pl.DataFrame:
        """
        Load logs parquet into DataFrame.

        Args:
            period: Either 'normal' or 'abnormal'

        Returns:
            DataFrame with columns: time, trace_id, span_id, level,
            service_name, message, attr.k8s.pod.name, etc.
        """
        path = self.data_dir / f"{period}_logs.parquet"
        if not path.exists():
            raise FileNotFoundError(f"Logs file not found: {path}")
        return pl.read_parquet(path)

    def _enhance_generic_span_names(self, df: pl.DataFrame) -> pl.DataFrame:
        """
        Replace generic HTTP method-only spans (GET, POST, etc.) with their first child's name.

        This enhances semantic information by promoting meaningful child span names
        to replace their generic parent spans that only contain HTTP methods.

        Args:
            df: DataFrame with trace data

        Returns:
            DataFrame with enhanced span names
        """
        generic_methods = {"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}

        # Find spans that are just HTTP methods
        generic_mask = df.filter(pl.col("span_name").is_in(generic_methods))
        generic_count = len(generic_mask)

        if generic_count == 0:
            return df

        logger.debug(f"Found {generic_count} generic HTTP method spans to enhance")

        # Get all generic parent spans
        generic_parents = generic_mask.select(["trace_id", "span_id", "span_name"]).rename(
            {"span_id": "parent_span_id", "span_name": "parent_span_name"}
        )

        # Get all non-generic child spans
        non_generic_children = df.filter(~pl.col("span_name").is_in(generic_methods)).select(
            ["trace_id", "parent_span_id", "span_name"]
        )

        # Merge to find children of generic parents
        merged = generic_parents.join(non_generic_children, on=["trace_id", "parent_span_id"], how="inner")

        # Group by parent_span_id and take the first child's name
        if len(merged) > 0:
            # Sort to ensure consistent "first" child selection
            merged = merged.sort(["trace_id", "parent_span_id"])
            span_id_to_name = (
                merged.group_by("parent_span_id").agg(pl.col("span_name").first()).to_dict(as_series=False)
            )

            # Create mapping dict
            name_map = dict(zip(span_id_to_name["parent_span_id"], span_id_to_name["span_name"], strict=False))

            # Apply replacements using vectorized when-then
            df = df.with_columns(
                pl.when(pl.col("span_name").is_in(generic_methods) & pl.col("span_id").is_in(name_map.keys()))
                .then(pl.col("span_id").replace(name_map, default=pl.col("span_name")))
                .otherwise(pl.col("span_name"))
                .alias("span_name")
            )

            enhanced_count = len(name_map)
            logger.info(f"Enhanced {enhanced_count}/{generic_count} generic HTTP method spans with child names")
        else:
            logger.info("No children found for generic HTTP method spans")

        return df

    def load_metrics_histogram(self, period: str = "abnormal") -> pl.DataFrame:
        """
        Load histogram metrics parquet into DataFrame.

        Args:
            period: Either 'normal' or 'abnormal'

        Returns:
            DataFrame with columns: time, metric, service_name, count, sum, min, max, etc.
        """
        path = self.data_dir / f"{period}_metrics_histogram.parquet"
        if not path.exists():
            raise FileNotFoundError(f"Histogram metrics file not found: {path}")
        return pl.read_parquet(path)

    def load_metrics_sum(self, period: str = "abnormal") -> pl.DataFrame:
        """
        Load sum metrics parquet into DataFrame.

        Args:
            period: Either 'normal' or 'abnormal'

        Returns:
            DataFrame with columns: time, metric, value, service_name, etc.
        """
        path = self.data_dir / f"{period}_metrics_sum.parquet"
        if not path.exists():
            raise FileNotFoundError(f"Sum metrics file not found: {path}")
        return pl.read_parquet(path)

    def load_conclusion(self) -> pl.DataFrame:
        """
        Load ground truth fault injection labels.

        Returns:
            DataFrame with columns: SpanName, AbnormalAvgDuration, NormalAvgDuration,
            AbnormalSuccRate, NormalSuccRate, etc.
        """
        path = self.data_dir / "conclusion.parquet"
        if not path.exists():
            raise FileNotFoundError(f"Conclusion file not found: {path}")
        return pl.read_parquet(path)

    def identify_alarm_nodes(self) -> set[str]:
        """Identify alarm nodes from conclusion.parquet based on Issues field.

        Returns:
            Set of span names that have non-empty Issues (indicating detected problems)
        """
        import json

        conclusion_df = self.load_conclusion()

        # Identify spans with non-empty Issues field
        def has_issues(issues_str: str | None) -> bool:
            """Check if Issues field contains actual issues (not just empty dict)."""
            if not issues_str or issues_str == "{}":
                return False
            try:
                issues = json.loads(issues_str)
                return bool(issues)
            except (json.JSONDecodeError, TypeError):
                return False

        alarm_spans = (
            conclusion_df.filter(pl.col("Issues").map_elements(has_issues, return_dtype=pl.Boolean))
            .select("SpanName")
            .to_series()
            .to_list()
        )

        return set(alarm_spans)

    def identify_alarm_nodes_v2(
        self,
        error_rate_threshold: float = 0.1,
        min_call_count: int = 1,
    ) -> set[str]:
        """Identify alarm nodes by comparing baseline vs abnormal root-span metrics.

        Root spans are detected as ``parent_span_id == ""`` (server entry points). Spans
        whose service is in ``LOADGEN_LIKE_SERVICES`` are excluded (synthetic traffic only);
        real frontend aggregators like ``front-end`` / ``ts-ui-dashboard`` are kept since
        they carry the actual user-facing anomalies.

        Uses adaptive thresholds based on baseline statistics (matching state_detector logic).

        Args:
            error_rate_threshold: Minimum error rate in abnormal period to trigger alarm.
            min_call_count: Minimum number of calls required in both periods for comparison.

        Returns:
            Set of full span names in format "{service_name}::{span_name}" that have
            detected problems (latency increase, error rate increase, or disappearance).
        """
        baseline_traces = self.load_traces("normal")
        abnormal_traces = self.load_traces("abnormal")

        def filter_root_spans(df: pl.DataFrame) -> pl.DataFrame:
            # Methodology §7.1: ``alarm_set`` is the set of *topmost* Server-kind
            # nodes per trace — a Server span whose parent (in the same trace)
            # is NOT itself Server-kind. Plain ``parent_span_id == ""`` breaks
            # on TrainTicket-style data where the loadgenerator emits the only
            # true root, and naive ``span_kind == Server`` re-inflates the alarm
            # set to every Server span across the call tree.
            return filter_root_server_spans(df)

        baseline_root = filter_root_spans(baseline_traces)
        abnormal_root = filter_root_spans(abnormal_traces)

        if len(baseline_root) == 0 or len(abnormal_root) == 0:
            logger.warning("No backend root spans found in baseline or abnormal traces")
            return set()

        # Aggregate metrics by full_span_name (service_name::span_name) for baseline
        baseline_agg = self._aggregate_root_span_metrics(baseline_root)
        abnormal_agg = self._aggregate_root_span_metrics(abnormal_root)

        # Join baseline and abnormal aggregations on full_span_name
        comparison = baseline_agg.join(abnormal_agg, on="full_span_name", how="inner", suffix="_abn")

        alarm_spans: set[str] = set()

        for row in comparison.iter_rows(named=True):
            full_span_name = row["full_span_name"]
            b_count = row["call_count"]
            a_count = row["call_count_abn"]

            # Skip spans with insufficient data
            if b_count < min_call_count or a_count < min_call_count:
                continue

            b_avg = row["avg_duration"]
            b_std = row["std_duration"] or 0.0
            a_avg = row["avg_duration_abn"]

            b_p99 = row["p99_duration"]
            a_p99 = row["p99_duration_abn"]

            b_err = row["error_rate"]
            a_err = row["error_rate_abn"]

            # Calculate CV for adaptive threshold
            b_avg_cv = b_std / b_avg if b_avg > 1e-6 else 0.0

            # Latency detection (avg duration) with adaptive threshold
            if b_avg > 0:
                avg_threshold = get_adaptive_threshold(b_avg, b_avg_cv)
                if a_avg > b_avg * avg_threshold:
                    logger.debug(
                        f"Alarm: {full_span_name} avg latency increased "
                        f"({b_avg:.3f}s -> {a_avg:.3f}s, ratio={a_avg / b_avg:.2f}x, threshold={avg_threshold:.2f}x)"
                    )
                    alarm_spans.add(full_span_name)
                    continue

            # Latency detection (p99 duration) with adaptive threshold
            # Approximate p99 std as 1.5x of avg std
            b_p99_std = b_std * 1.5 if b_std else 0.0
            b_p99_cv = b_p99_std / b_p99 if b_p99 > 1e-6 else 0.0
            if b_p99 > 0:
                p99_threshold = get_adaptive_threshold(b_p99, b_p99_cv)
                if a_p99 > b_p99 * p99_threshold:
                    logger.debug(
                        f"Alarm: {full_span_name} p99 latency increased "
                        f"({b_p99:.3f}s -> {a_p99:.3f}s, ratio={a_p99 / b_p99:.2f}x, threshold={p99_threshold:.2f}x)"
                    )
                    alarm_spans.add(full_span_name)
                    continue

            # Error rate detection
            if a_err > b_err and a_err >= error_rate_threshold:
                logger.debug(f"Alarm: {full_span_name} error rate increased ({b_err:.2%} -> {a_err:.2%})")
                alarm_spans.add(full_span_name)

        # Detect missing spans: spans that exist in baseline but not in abnormal period
        # These are alarm candidates because they indicate complete failure
        baseline_span_names = set(baseline_agg["full_span_name"].to_list())
        abnormal_span_names = set(abnormal_agg["full_span_name"].to_list())
        missing_spans = baseline_span_names - abnormal_span_names

        for full_span_name in missing_spans:
            # Only consider spans with meaningful baseline traffic
            baseline_row = baseline_agg.filter(pl.col("full_span_name") == full_span_name)
            if len(baseline_row) > 0:
                b_count = baseline_row["call_count"][0]
                if b_count >= min_call_count:
                    logger.debug(f"Alarm: {full_span_name} missing in abnormal period (baseline had {b_count} calls)")
                    alarm_spans.add(full_span_name)

        logger.info(
            f"identify_alarm_nodes_v2: detected {len(alarm_spans)} alarm spans "
            f"(including {len(missing_spans & alarm_spans)} missing spans) "
            f"from {len(comparison)} backend root span types"
        )

        return alarm_spans

    def _aggregate_root_span_metrics(self, df: pl.DataFrame) -> pl.DataFrame:
        """Aggregate metrics for root spans grouped by service_name and span_name.

        Args:
            df: DataFrame with root span data (already filtered to exclude caller-like services).

        Returns:
            DataFrame with columns: full_span_name, call_count, error_count, avg_duration,
            p99_duration, std_duration, error_rate.
            full_span_name is in format "{service_name}::{span_name}".
        """
        # Prepare columns: duration in seconds, error flag, full span name
        df_prepared = df.with_columns(
            [
                (pl.col("duration") / 1e9).alias("duration_sec"),
                (
                    (pl.col("attr.http.response.status_code").is_not_null())
                    & (pl.col("attr.http.response.status_code") >= 500)
                )
                .cast(pl.Int32)
                .alias("is_error"),
                # Create full span name: service_name::span_name
                (pl.col("service_name") + "::" + pl.col("span_name")).alias("full_span_name"),
            ]
        )

        # Group by full_span_name (service_name::span_name) and aggregate
        agg_df = df_prepared.group_by("full_span_name").agg(
            [
                pl.len().alias("call_count"),
                pl.col("is_error").sum().alias("error_count"),
                pl.col("duration_sec").mean().alias("avg_duration"),
                pl.col("duration_sec").quantile(0.99).alias("p99_duration"),
                pl.col("duration_sec").std().alias("std_duration"),
            ]
        )

        # Calculate error rate
        agg_df = agg_df.with_columns((pl.col("error_count") / pl.col("call_count")).alias("error_rate"))

        return agg_df

    def get_span_issues(self) -> dict[str, dict]:
        """Get Issues information for all spans from conclusion.parquet.

        Returns:
            Dictionary mapping span names to their Issues dict
        """
        import json

        conclusion_df = self.load_conclusion()
        span_issues = {}

        for row in conclusion_df.iter_rows(named=True):
            span_name = row["SpanName"]
            issues_str = row["Issues"]

            if issues_str and issues_str != "{}":
                try:
                    issues = json.loads(issues_str)
                    if issues:
                        span_issues[span_name] = issues
                except (json.JSONDecodeError, TypeError):
                    pass

        return span_issues

    @timeit
    def build_graph_from_parquet(
        self,
        baseline_period: str = "normal",
        abnormal_period: str = "abnormal",
        selected_metrics: list[str] | None = None,
    ) -> HyperGraph:
        """
        Build HyperGraph from parquet files, including all K8s physical entities.

        Args:
            baseline_period: Which period to use as baseline ('normal')
            abnormal_period: Which period contains faults ('abnormal')
            endpoint_granularity: Use {service}::{endpoint} nodes if True, else service-level
            selected_metrics: List of metric names to extract. If None, uses comprehensive defaults
                covering CPU, memory, filesystem, HTTP, DB, JVM, and network metrics.

        Returns:
            HyperGraph with K8s entities (namespace, deployment, pod, container, service)
            and their relationships (owns, schedules, runs, calls)
        """
        if selected_metrics is None:
            selected_metrics = [
                # CPU metrics (gauge)
                "container.cpu.usage",
                "container.cpu.time",
                "k8s.pod.cpu.usage",
                "k8s.pod.cpu.time",
                "k8s.pod.cpu_limit_utilization",
                "k8s.pod.cpu.node.utilization",
                # Memory metrics (gauge)
                "container.memory.working_set",
                "container.memory.available",
                "container.memory.usage",
                "container.memory.rss",
                "k8s.pod.memory.working_set",
                "k8s.pod.memory.usage",
                "k8s.pod.memory.rss",
                "k8s.pod.memory_limit_utilization",
                "k8s.pod.memory.node.utilization",
                # Filesystem metrics (gauge)
                "container.filesystem.usage",
                "container.filesystem.available",
                "k8s.pod.filesystem.usage",
                "k8s.pod.filesystem.available",
                # K8s state metrics (gauge)
                "k8s.container.ready",
                "k8s.container.restarts",
                "k8s.pod.phase",
                "k8s.deployment.available",
                "k8s.deployment.desired",
                "k8s.replicaset.available",
                "k8s.replicaset.desired",
                "k8s.statefulset.ready_pods",
                "k8s.statefulset.current_pods",
                # HTTP duration metrics (histogram)
                "hubble_http_request_duration_seconds",
                "http.server.request.duration",
                "http.client.request.duration",
                # HTTP count metrics (sum)
                "hubble_http_requests_total",
                # Database connection metrics (histogram)
                "db.client.connections.use_time",
                "db.client.connections.wait_time",
                # Database connection state (gauge)
                "db.client.connections.usage",
                "db.client.connections.idle.min",
                "db.client.connections.max",
                "db.client.connections.pending_requests",
                # JVM GC metrics (histogram)
                "jvm.gc.duration",
                # JVM memory metrics (gauge)
                "jvm.memory.used",
                "jvm.memory.committed",
                "jvm.memory.limit",
                "jvm.memory.init",
                # JVM CPU and thread metrics (gauge)
                "jvm.cpu.recent_utilization",
                "jvm.cpu.time",
                "jvm.thread.count",
                "jvm.system.cpu.utilization",
                # Network metrics (sum)
                "k8s.pod.network.io",
                "k8s.pod.network.errors",
                "hubble_drop_total",
            ]

        graph = HyperGraph()

        # Load all data sources and cache in instance variables
        logger.info(f"Loading {baseline_period} data...")
        self._baseline_metrics = self.load_metrics(baseline_period)
        self._baseline_metrics_hist = self.load_metrics_histogram(baseline_period)
        self._baseline_metrics_sum = self.load_metrics_sum(baseline_period)
        self._baseline_traces = self.load_traces(baseline_period)
        self._baseline_logs = self.load_logs(baseline_period)

        logger.info(f"Loading {abnormal_period} data...")
        self._abnormal_metrics = self.load_metrics(abnormal_period)
        self._abnormal_metrics_hist = self.load_metrics_histogram(abnormal_period)
        self._abnormal_metrics_sum = self.load_metrics_sum(abnormal_period)
        self._abnormal_traces = self.load_traces(abnormal_period)
        self._abnormal_logs = self.load_logs(abnormal_period)

        # Use cached variables for resource extraction
        baseline_metrics = self._baseline_metrics
        baseline_metrics_hist = self._baseline_metrics_hist
        baseline_metrics_sum = self._baseline_metrics_sum
        baseline_traces = self._baseline_traces
        baseline_logs = self._baseline_logs
        abnormal_metrics = self._abnormal_metrics
        abnormal_metrics_hist = self._abnormal_metrics_hist
        abnormal_metrics_sum = self._abnormal_metrics_sum
        abnormal_traces = self._abnormal_traces
        abnormal_logs = self._abnormal_logs

        # Extract K8s resource hierarchy from all data sources
        logger.info("Extracting K8s resource hierarchy from all data sources...")
        resources = self._extract_k8s_resources_from_all_sources(
            baseline_metrics,
            abnormal_metrics,
            baseline_metrics_hist,
            abnormal_metrics_hist,
            baseline_metrics_sum,
            abnormal_metrics_sum,
            baseline_traces,
            abnormal_traces,
            baseline_logs,
            abnormal_logs,
        )

        # Build all K8s entity nodes
        logger.info("Building K8s entity nodes...")
        self._build_k8s_nodes(graph, resources, selected_metrics)

        # Build physical topology edges
        logger.info("Building physical topology edges...")
        self._build_physical_edges(graph, resources)

        # Build service nodes and service->pod mappings from traces
        logger.info("Building service nodes from traces...")
        self._build_service_nodes_from_traces(graph, selected_metrics)

        # Build span nodes with aggregated statistics
        logger.info("Building span nodes from traces...")
        self._build_span_nodes(graph, baseline_traces, abnormal_traces, baseline_logs, abnormal_logs)

        # Build logical call edges at span level from traces
        logger.info("Building span-to-span call edges from traces...")
        edges = self._build_edges_from_traces(baseline_traces, abnormal_traces, graph)

        for edge in edges:
            graph.add_edge(edge, strict=False)

        logger.info(f"Graph complete: {len(graph._node_id_map)} nodes, {len(graph._edge_id_map)} edges")

        return graph

    def _extract_k8s_resources_from_all_sources(
        self,
        baseline_metrics: pl.DataFrame,
        abnormal_metrics: pl.DataFrame,
        baseline_metrics_hist: pl.DataFrame,
        abnormal_metrics_hist: pl.DataFrame,
        baseline_metrics_sum: pl.DataFrame,
        abnormal_metrics_sum: pl.DataFrame,
        baseline_traces: pl.DataFrame,
        abnormal_traces: pl.DataFrame,
        baseline_logs: pl.DataFrame,
        abnormal_logs: pl.DataFrame,
    ) -> dict[str, dict[str, Any]]:
        """
        Extract K8s resource hierarchy from all data sources (metrics, traces, logs).

        Returns:
            Dictionary mapping resource types to {resource_name: metadata} structure.
            Metadata includes parent relationships and timestamps (first_seen, last_seen).
        """
        resources: dict[str, dict[str, Any]] = {
            "namespace": {},
            "node": {},
            "deployment": {},
            "statefulset": {},
            "replicaset": {},
            "pod": {},
            "container": {},
        }

        # Define K8s columns we need to extract
        k8s_cols = [
            "attr.k8s.namespace.name",
            "attr.k8s.node.name",
            "attr.k8s.deployment.name",
            "attr.k8s.statefulset.name",
            "attr.k8s.replicaset.name",
            "attr.k8s.pod.name",
            "attr.k8s.container.name",
            "time",
        ]

        # Process each dataframe separately to extract K8s resources
        all_dataframes = [
            baseline_metrics,
            abnormal_metrics,
            baseline_metrics_hist,
            abnormal_metrics_hist,
            baseline_metrics_sum,
            abnormal_metrics_sum,
            baseline_traces,
            abnormal_traces,
            baseline_logs,
            abnormal_logs,
        ]

        # Extract resources from each dataframe without concat
        for df in all_dataframes:
            # Select only K8s columns that exist in this dataframe
            available_cols = [col for col in k8s_cols if col in df.columns]
            if not available_cols:
                continue

            df_subset = df.select(available_cols)

            # Extract unique namespaces
            if "attr.k8s.namespace.name" in df_subset.columns:
                namespaces = df_subset.select("attr.k8s.namespace.name").drop_nulls().unique().to_series().to_list()
                for ns in namespaces:
                    if ns not in resources["namespace"]:
                        resources["namespace"][ns] = set()

            # Extract unique nodes
            if "attr.k8s.node.name" in df_subset.columns:
                nodes = df_subset.select("attr.k8s.node.name").drop_nulls().unique().to_series().to_list()
                for node in nodes:
                    if node not in resources["node"]:
                        resources["node"][node] = set()

            # Extract deployments with their namespaces
            if "attr.k8s.deployment.name" in df_subset.columns and "attr.k8s.namespace.name" in df_subset.columns:
                deployment_df = (
                    df_subset.select(["attr.k8s.deployment.name", "attr.k8s.namespace.name"])
                    .drop_nulls(subset=["attr.k8s.deployment.name"])
                    .unique()
                )
                deployments = dict(
                    zip(
                        deployment_df.select("attr.k8s.deployment.name").to_series().to_list(),
                        deployment_df.select("attr.k8s.namespace.name").to_series().to_list(),
                        strict=False,
                    )
                )
                resources["deployment"].update(deployments)

            # Extract statefulsets with their namespaces
            if "attr.k8s.statefulset.name" in df_subset.columns and "attr.k8s.namespace.name" in df_subset.columns:
                statefulset_df = (
                    df_subset.select(["attr.k8s.statefulset.name", "attr.k8s.namespace.name"])
                    .drop_nulls(subset=["attr.k8s.statefulset.name"])
                    .unique()
                )
                statefulsets = dict(
                    zip(
                        statefulset_df.select("attr.k8s.statefulset.name").to_series().to_list(),
                        statefulset_df.select("attr.k8s.namespace.name").to_series().to_list(),
                        strict=False,
                    )
                )
                resources["statefulset"].update(statefulsets)

            # Extract replicasets with their parents
            if "attr.k8s.replicaset.name" in df_subset.columns:
                replicaset_cols = ["attr.k8s.replicaset.name"]
                if "attr.k8s.deployment.name" in df_subset.columns:
                    replicaset_cols.append("attr.k8s.deployment.name")
                if "attr.k8s.statefulset.name" in df_subset.columns:
                    replicaset_cols.append("attr.k8s.statefulset.name")

                replicaset_df = (
                    df_subset.select(replicaset_cols).drop_nulls(subset=["attr.k8s.replicaset.name"]).unique()
                )

                if "attr.k8s.deployment.name" in replicaset_df.columns:
                    replicaset_df = replicaset_df.with_columns(
                        pl.when(pl.col("attr.k8s.deployment.name").is_not_null())
                        .then(pl.col("attr.k8s.deployment.name"))
                        .otherwise(
                            pl.col("attr.k8s.statefulset.name")
                            if "attr.k8s.statefulset.name" in replicaset_df.columns
                            else pl.lit(None)
                        )
                        .alias("parent")
                    )
                    replicasets = dict(
                        zip(
                            replicaset_df.select("attr.k8s.replicaset.name").to_series().to_list(),
                            replicaset_df.select("parent").to_series().to_list(),
                            strict=False,
                        )
                    )
                    resources["replicaset"].update(replicasets)

            # Extract pods with timestamps and parents
            if "attr.k8s.pod.name" in df_subset.columns and "time" in df_subset.columns:
                pod_cols = ["attr.k8s.pod.name", "time"]
                if "attr.k8s.replicaset.name" in df_subset.columns:
                    pod_cols.append("attr.k8s.replicaset.name")
                if "attr.k8s.node.name" in df_subset.columns:
                    pod_cols.append("attr.k8s.node.name")

                pod_df = df_subset.select(pod_cols).drop_nulls(subset=["attr.k8s.pod.name"])

                pod_agg = pod_df.group_by("attr.k8s.pod.name").agg(
                    [
                        pl.col("attr.k8s.replicaset.name").first().alias("replicaset")
                        if "attr.k8s.replicaset.name" in pod_cols
                        else pl.lit(None).alias("replicaset"),
                        pl.col("attr.k8s.node.name").first().alias("node")
                        if "attr.k8s.node.name" in pod_cols
                        else pl.lit(None).alias("node"),
                        pl.col("time").min().alias("first_seen"),
                        pl.col("time").max().alias("last_seen"),
                    ]
                )

                for row in pod_agg.iter_rows(named=True):
                    pod_name = row["attr.k8s.pod.name"]
                    if pod_name not in resources["pod"]:
                        resources["pod"][pod_name] = {
                            "replicaset": row["replicaset"],
                            "node": row["node"],
                            "first_seen": row["first_seen"],
                            "last_seen": row["last_seen"],
                        }
                    else:
                        # Update with non-null values
                        if row["replicaset"] is not None:
                            resources["pod"][pod_name]["replicaset"] = row["replicaset"]
                        if row["node"] is not None:
                            resources["pod"][pod_name]["node"] = row["node"]
                        # Update timestamps
                        if row["first_seen"] < resources["pod"][pod_name]["first_seen"]:
                            resources["pod"][pod_name]["first_seen"] = row["first_seen"]
                        if row["last_seen"] > resources["pod"][pod_name]["last_seen"]:
                            resources["pod"][pod_name]["last_seen"] = row["last_seen"]

            # Extract containers with timestamps and pods
            if "attr.k8s.container.name" in df_subset.columns and "time" in df_subset.columns:
                container_cols = ["attr.k8s.container.name", "time"]
                if "attr.k8s.pod.name" in df_subset.columns:
                    container_cols.append("attr.k8s.pod.name")

                container_df = df_subset.select(container_cols).drop_nulls(subset=["attr.k8s.container.name"])

                container_agg = container_df.group_by("attr.k8s.container.name").agg(
                    [
                        pl.col("attr.k8s.pod.name").first().alias("pod")
                        if "attr.k8s.pod.name" in container_cols
                        else pl.lit(None).alias("pod"),
                        pl.col("time").min().alias("first_seen"),
                        pl.col("time").max().alias("last_seen"),
                    ]
                )

                for row in container_agg.iter_rows(named=True):
                    container_name = row["attr.k8s.container.name"]
                    if container_name not in resources["container"]:
                        resources["container"][container_name] = {
                            "pod": row["pod"],
                            "first_seen": row["first_seen"],
                            "last_seen": row["last_seen"],
                        }
                    else:
                        # Update with non-null values
                        if row["pod"] is not None:
                            resources["container"][container_name]["pod"] = row["pod"]
                        # Update timestamps
                        if row["first_seen"] < resources["container"][container_name]["first_seen"]:
                            resources["container"][container_name]["first_seen"] = row["first_seen"]
                        if row["last_seen"] > resources["container"][container_name]["last_seen"]:
                            resources["container"][container_name]["last_seen"] = row["last_seen"]

        logger.info(
            f"Extracted K8s resources: "
            f"{len(resources['namespace'])} namespaces, "
            f"{len(resources['node'])} nodes, "
            f"{len(resources['deployment'])} deployments, "
            f"{len(resources['statefulset'])} statefulsets, "
            f"{len(resources['replicaset'])} replicasets, "
            f"{len(resources['pod'])} pods, "
            f"{len(resources['container'])} containers"
        )

        return resources

    def _build_k8s_nodes(
        self,
        graph: HyperGraph,
        resources: dict[str, dict[str, Any]],
        selected_metrics: list[str],
    ) -> None:
        """Build nodes for all K8s entities with their metrics."""

        # Build namespace nodes
        for namespace_name in resources["namespace"]:
            node = Node(
                kind=PlaceKind.namespace,
                self_name=namespace_name,
            )
            graph.add_node(node, strict=False)

        # Build node (machine) nodes
        for node_name in resources["node"]:
            node = Node(
                kind=PlaceKind.machine,
                self_name=node_name,
            )
            graph.add_node(node, strict=False)

        # Build deployment nodes
        for deployment_name in resources["deployment"]:
            node = Node(
                kind=PlaceKind.deployment,
                self_name=deployment_name,
            )
            graph.add_node(node, strict=False)

        # Build statefulset nodes
        for statefulset_name in resources["statefulset"]:
            node = Node(
                kind=PlaceKind.stateful_set,
                self_name=statefulset_name,
            )
            graph.add_node(node, strict=False)

        # Build replicaset nodes
        for replicaset_name in resources["replicaset"]:
            node = Node(
                kind=PlaceKind.replica_set,
                self_name=replicaset_name,
            )
            graph.add_node(node, strict=False)

        # Build pod nodes with aggregated container metrics (parallel)
        logger.info(f"Extracting metrics for {len(resources['pod'])} pods...")
        pod_names = list(resources["pod"].keys())

        def extract_pod_task(pod_name: str) -> tuple[str, dict, dict]:
            baseline, abnormal = self._extract_pod_metrics(pod_name, selected_metrics)
            return pod_name, baseline, abnormal

        pod_tasks = [lambda pn=pn: extract_pod_task(pn) for pn in pod_names]
        pod_results = fmap_threadpool(pod_tasks, parallel=8, ignore_exceptions=False, show_progress=False)

        for pod_name, baseline_metrics, abnormal_metrics in pod_results:
            node = Node(
                kind=PlaceKind.pod,
                self_name=pod_name,
                baseline_metrics=baseline_metrics,
                abnormal_metrics=abnormal_metrics,
            )
            graph.add_node(node, strict=False)

        # Build container nodes with their metrics (parallel)
        logger.info(f"Extracting metrics for {len(resources['container'])} containers...")
        container_names = list(resources["container"].keys())

        def extract_container_task(container_name: str) -> tuple[str, dict, dict]:
            baseline, abnormal = self._extract_container_metrics(container_name, selected_metrics)
            return container_name, baseline, abnormal

        container_tasks = [lambda cn=cn: extract_container_task(cn) for cn in container_names]
        container_results = fmap_threadpool(container_tasks, parallel=8, ignore_exceptions=False, show_progress=False)

        for container_name, baseline_metrics, abnormal_metrics in container_results:
            node = Node(
                kind=PlaceKind.container,
                self_name=container_name,
                baseline_metrics=baseline_metrics,
                abnormal_metrics=abnormal_metrics,
            )
            graph.add_node(node, strict=False)

    def _build_physical_edges(self, graph: HyperGraph, resources: dict[str, dict[str, Any]]) -> None:
        """Build physical topology edges between K8s entities."""

        # Build node name lookup cache to avoid repeated string formatting and graph lookups
        node_cache: dict[str, Node | None] = {}

        def get_cached_node(kind: PlaceKind, name: str) -> Node | None:
            key = f"{kind}|{name}"
            if key not in node_cache:
                node_cache[key] = graph.get_node_by_name(key)
            return node_cache[key]

        # namespace -> deployment (owns)
        for deployment_name, namespace_name in resources["deployment"].items():
            if namespace_name:
                ns_node = get_cached_node(PlaceKind.namespace, namespace_name)
                dep_node = get_cached_node(PlaceKind.deployment, deployment_name)

                if ns_node and dep_node:
                    assert dep_node.id is not None and ns_node.id is not None
                    edge = Edge(
                        src_id=ns_node.id,
                        dst_id=dep_node.id,
                        src_name=f"{PlaceKind.namespace}|{namespace_name}",
                        dst_name=f"{PlaceKind.deployment}|{deployment_name}",
                        kind=DepKind.owns,
                        weight=1.0,
                        data=None,
                    )
                    graph.add_edge(edge, strict=False)

        # namespace -> statefulset (owns)
        for statefulset_name, namespace_name in resources["statefulset"].items():
            if namespace_name:
                ns_node = get_cached_node(PlaceKind.namespace, namespace_name)
                ss_node = get_cached_node(PlaceKind.stateful_set, statefulset_name)

                if ns_node and ss_node:
                    assert ss_node.id is not None and ns_node.id is not None
                    edge = Edge(
                        src_id=ns_node.id,
                        dst_id=ss_node.id,
                        src_name=f"{PlaceKind.namespace}|{namespace_name}",
                        dst_name=f"{PlaceKind.stateful_set}|{statefulset_name}",
                        kind=DepKind.owns,
                        weight=1.0,
                        data=None,
                    )
                    graph.add_edge(edge, strict=False)

        # deployment -> replicaset (scales)
        # statefulset -> replicaset (manages)
        for replicaset_name, parent_name in resources["replicaset"].items():
            if parent_name:
                rs_node = get_cached_node(PlaceKind.replica_set, replicaset_name)
                # Try deployment first
                dep_node = get_cached_node(PlaceKind.deployment, parent_name)

                if dep_node and rs_node:
                    assert dep_node.id is not None and rs_node.id is not None
                    edge = Edge(
                        src_id=dep_node.id,
                        dst_id=rs_node.id,
                        src_name=f"{PlaceKind.deployment}|{parent_name}",
                        dst_name=f"{PlaceKind.replica_set}|{replicaset_name}",
                        kind=DepKind.scales,
                        weight=1.0,
                        data=None,
                    )
                    graph.add_edge(edge, strict=False)
                else:
                    # Try statefulset
                    ss_node = get_cached_node(PlaceKind.stateful_set, parent_name)
                    if ss_node and rs_node:
                        assert ss_node.id is not None and rs_node.id is not None
                        edge = Edge(
                            src_id=ss_node.id,
                            dst_id=rs_node.id,
                            src_name=f"{PlaceKind.stateful_set}|{parent_name}",
                            dst_name=f"{PlaceKind.replica_set}|{replicaset_name}",
                            kind=DepKind.manages,
                            weight=1.0,
                            data=None,
                        )
                        graph.add_edge(edge, strict=False)

        # replicaset -> pod (manages)
        # node -> pod (schedules)
        for pod_name, pod_info in resources["pod"].items():
            pod_node = get_cached_node(PlaceKind.pod, pod_name)
            if not pod_node:
                continue

            # replicaset -> pod
            replicaset_name = pod_info.get("replicaset")
            if replicaset_name:
                rs_node = get_cached_node(PlaceKind.replica_set, replicaset_name)
                if rs_node:
                    assert rs_node.id is not None and pod_node.id is not None
                    edge = Edge(
                        src_id=rs_node.id,
                        dst_id=pod_node.id,
                        src_name=f"{PlaceKind.replica_set}|{replicaset_name}",
                        dst_name=f"{PlaceKind.pod}|{pod_name}",
                        kind=DepKind.manages,
                        weight=1.0,
                        data=None,
                    )
                    graph.add_edge(edge, strict=False)

            # node -> pod
            node_name = pod_info.get("node")
            if node_name:
                node = get_cached_node(PlaceKind.machine, node_name)
                if node:
                    assert node.id is not None and pod_node.id is not None
                    edge = Edge(
                        src_id=node.id,
                        dst_id=pod_node.id,
                        src_name=f"{PlaceKind.machine}|{node_name}",
                        dst_name=f"{PlaceKind.pod}|{pod_name}",
                        kind=DepKind.schedules,
                        weight=1.0,
                        data=None,
                    )
                    graph.add_edge(edge, strict=False)

        # pod -> container (runs)
        for container_name, container_info in resources["container"].items():
            container_pod_name: str | None = (
                container_info.get("pod") if isinstance(container_info, dict) else container_info
            )
            if container_pod_name:
                pod_node = get_cached_node(PlaceKind.pod, container_pod_name)
                container_node = get_cached_node(PlaceKind.container, container_name)
                if pod_node and container_node:
                    assert container_node.id is not None and pod_node.id is not None
                    edge = Edge(
                        src_id=pod_node.id,
                        dst_id=container_node.id,
                        src_name=f"{PlaceKind.pod}|{container_pod_name}",
                        dst_name=f"{PlaceKind.container}|{container_name}",
                        kind=DepKind.runs,
                        weight=1.0,
                        data=None,
                    )
                    graph.add_edge(edge, strict=False)

    def _build_service_nodes_from_traces(
        self,
        graph: HyperGraph,
        selected_metrics: list[str],
    ) -> None:
        """Build service nodes and connect them to pods based on trace data."""

        assert self._abnormal_traces is not None, "Abnormal traces not loaded"

        # Extract service names from both abnormal and baseline traces
        abnormal_services = set(
            self._abnormal_traces.select("service_name").unique().drop_nulls().to_series().to_list()
        )
        baseline_services: set[str] = set()
        if self._baseline_traces is not None:
            baseline_services = set(
                self._baseline_traces.select("service_name").unique().drop_nulls().to_series().to_list()
            )

        # Union of all services from both periods
        all_services = abnormal_services | baseline_services

        logger.info(f"Extracting metrics for {len(all_services)} services...")

        # Parallel extraction of service metrics
        def extract_service_task(service_name: str) -> tuple[str, dict, dict, set[str]]:
            baseline_metrics, abnormal_metrics = self._extract_service_metrics(service_name, selected_metrics)

            # Collect pod names for this service from both periods
            pod_names_set: set[str] = set()
            assert self._abnormal_traces is not None
            abnormal_service_traces = self._abnormal_traces.filter(pl.col("service_name") == service_name)
            pod_names_set.update(
                abnormal_service_traces.select("attr.k8s.pod.name").drop_nulls().unique().to_series().to_list()
            )
            if self._baseline_traces is not None:
                baseline_service_traces = self._baseline_traces.filter(pl.col("service_name") == service_name)
                pod_names_set.update(
                    baseline_service_traces.select("attr.k8s.pod.name").drop_nulls().unique().to_series().to_list()
                )

            return service_name, baseline_metrics, abnormal_metrics, pod_names_set

        service_tasks = [lambda sn=sn: extract_service_task(sn) for sn in all_services]
        service_results = fmap_threadpool(service_tasks, parallel=8, ignore_exceptions=False, show_progress=False)

        for service_name, baseline_metrics, abnormal_metrics, pod_names_set in service_results:
            node = Node(
                kind=PlaceKind.service,
                self_name=service_name,
                baseline_metrics=baseline_metrics,
                abnormal_metrics=abnormal_metrics,
            )
            graph.add_node(node, strict=False)

            # Use cached lookups for service-pod edges
            service_node = graph.get_node_by_name(f"{PlaceKind.service}|{service_name}")
            if service_node:
                for pod_name in pod_names_set:
                    pod_node = graph.get_node_by_name(f"{PlaceKind.pod}|{pod_name}")
                    if pod_node:
                        assert service_node.id is not None and pod_node.id is not None
                        # service -> pod (routes_to)
                        edge = Edge(
                            src_id=service_node.id,
                            dst_id=pod_node.id,
                            src_name=f"{PlaceKind.service}|{service_name}",
                            dst_name=f"{PlaceKind.pod}|{pod_name}",
                            kind=DepKind.routes_to,
                            weight=1.0,
                            data=None,
                        )
                        graph.add_edge(edge, strict=False)
                        graph.add_edge(edge, strict=False)

    def _aggregate_service_trace_metrics(
        self, service_name: str, baseline_traces_df: pl.DataFrame, abnormal_traces_df: pl.DataFrame
    ) -> dict[str, np.ndarray]:
        """Aggregate trace statistics for a service (error rate, latency, etc.).

        Args:
            service_name: Service to aggregate metrics for
            baseline_traces_df: Baseline trace data
            abnormal_traces_df: Abnormal trace data

        Returns:
            Dictionary with aggregated metrics:
            - error_rate: Proportion of failed requests (5xx HTTP status)
            - p99_duration: 99th percentile latency
            - avg_duration: Average latency
            - baseline_p99_duration: Baseline P99 latency for comparison
            - baseline_error_rate: Baseline error rate
        """
        # Process baseline period
        baseline_service = baseline_traces_df.filter(pl.col("service_name") == service_name)
        baseline_stats = None
        if len(baseline_service) > 0:
            baseline_stats = (
                baseline_service.with_columns(
                    [
                        (pl.col("duration") / 1e9).alias("duration_sec"),
                        (
                            pl.col("attr.http.response.status_code").is_not_null()
                            & (pl.col("attr.http.response.status_code") >= 500)
                        )
                        .cast(pl.Int32)
                        .alias("is_error"),
                    ]
                )
                .select(
                    [
                        pl.len().alias("total_count"),
                        pl.col("is_error").sum().alias("error_count"),
                        pl.col("duration_sec").mean().alias("avg_duration"),
                        pl.col("duration_sec").quantile(0.99).alias("p99_duration"),
                    ]
                )
                .row(0, named=True)
            )

        # Process abnormal period
        abnormal_service = abnormal_traces_df.filter(pl.col("service_name") == service_name)

        if len(abnormal_service) == 0:
            return {}

        abnormal_stats = (
            abnormal_service.with_columns(
                [
                    (pl.col("duration") / 1e9).alias("duration_sec"),
                    (
                        pl.col("attr.http.response.status_code").is_not_null()
                        & (pl.col("attr.http.response.status_code") >= 500)
                    )
                    .cast(pl.Int32)
                    .alias("is_error"),
                ]
            )
            .select(
                [
                    pl.len().alias("total_count"),
                    pl.col("is_error").sum().alias("error_count"),
                    pl.col("duration_sec").mean().alias("avg_duration"),
                    pl.col("duration_sec").quantile(0.99).alias("p99_duration"),
                ]
            )
            .row(0, named=True)
        )

        total_count = abnormal_stats["total_count"]
        error_count = abnormal_stats["error_count"]

        metrics = {
            "error_rate": np.array([error_count / total_count if total_count > 0 else 0.0]),
            "p99_duration": np.array([abnormal_stats["p99_duration"] or 0.0]),
            "avg_duration": np.array([abnormal_stats["avg_duration"] or 0.0]),
        }

        # Add baseline metrics if available
        if baseline_stats:
            baseline_total = baseline_stats["total_count"]
            baseline_errors = baseline_stats["error_count"]
            metrics["baseline_p99_duration"] = np.array([baseline_stats["p99_duration"] or 0.0])
            metrics["baseline_error_rate"] = np.array([baseline_errors / baseline_total if baseline_total > 0 else 0.0])

        return metrics

    def _batch_extract_entity_metrics(
        self,
        entity_col: str,
        entity_names: list[str],
        selected_metrics: list[str],
        filter_null_container: bool = False,
    ) -> dict[str, tuple[dict[str, tuple[np.ndarray, np.ndarray]], dict[str, tuple[np.ndarray, np.ndarray]]]]:
        """
        Batch extract metrics for multiple entities (pods/containers/services).

        Args:
            entity_col: Column name to filter by (e.g., "attr.k8s.pod.name")
            entity_names: List of entity names to extract metrics for
            selected_metrics: List of metric names to extract
            filter_null_container: If True, only get metrics where container.name is null

        Returns:
            Dict mapping entity_name -> (baseline_metrics, abnormal_metrics)
        """
        assert self._baseline_metrics is not None and self._abnormal_metrics is not None
        assert self._baseline_metrics_hist is not None and self._abnormal_metrics_hist is not None
        assert self._baseline_metrics_sum is not None and self._abnormal_metrics_sum is not None

        # Categorize metrics by type
        histogram_metrics = {
            "hubble_http_request_duration_seconds",
            "http.server.request.duration",
            "http.client.request.duration",
            "db.client.connections.use_time",
            "db.client.connections.wait_time",
            "jvm.gc.duration",
        }
        sum_metrics = {
            "hubble_http_requests_total",
            "k8s.pod.network.io",
            "k8s.pod.network.errors",
            "hubble_drop_total",
        }
        gauge_metrics = [m for m in selected_metrics if m not in histogram_metrics and m not in sum_metrics]
        hist_metrics = [m for m in selected_metrics if m in histogram_metrics]
        sum_metrics_list = [m for m in selected_metrics if m in sum_metrics]

        result: dict[
            str, tuple[dict[str, tuple[np.ndarray, np.ndarray]], dict[str, tuple[np.ndarray, np.ndarray]]]
        ] = {}

        # Pre-filter data by entities
        entity_filter = pl.col(entity_col).is_in(entity_names)

        # Process gauge metrics - optimized with group_by instead of nested loops
        if gauge_metrics:
            gauge_filter = entity_filter & pl.col("metric").is_in(gauge_metrics)
            if filter_null_container:
                gauge_filter = gauge_filter & pl.col("attr.k8s.container.name").is_null()

            baseline_gauge = self._baseline_metrics.filter(gauge_filter).sort("time")
            abnormal_gauge = self._abnormal_metrics.filter(gauge_filter).sort("time")

            # Group by entity and metric in one pass
            if len(abnormal_gauge) > 0:
                abnormal_grouped = abnormal_gauge.group_by([entity_col, "metric"]).agg(
                    [pl.col("time").alias("timestamps"), pl.col("value").alias("values")]
                )

                for row in abnormal_grouped.iter_rows(named=True):
                    entity = row[entity_col]
                    metric = row["metric"]

                    if entity not in result:
                        result[entity] = ({}, {})

                    abnormal_metrics = result[entity][1]
                    abnormal_metrics[metric] = (_ts_to_int_seconds(row["timestamps"]), np.array(row["values"]))

            if len(baseline_gauge) > 0:
                baseline_grouped = baseline_gauge.group_by([entity_col, "metric"]).agg(
                    [pl.col("time").alias("timestamps"), pl.col("value").alias("values")]
                )

                for row in baseline_grouped.iter_rows(named=True):
                    entity = row[entity_col]
                    metric = row["metric"]

                    if entity not in result:
                        result[entity] = ({}, {})

                    baseline_metrics = result[entity][0]
                    baseline_metrics[metric] = (_ts_to_int_seconds(row["timestamps"]), np.array(row["values"]))

        # Process histogram metrics - optimized with group_by
        if hist_metrics:
            hist_filter = entity_filter & pl.col("metric").is_in(hist_metrics)
            baseline_hist = self._baseline_metrics_hist.filter(hist_filter).sort("time")
            abnormal_hist = self._abnormal_metrics_hist.filter(hist_filter).sort("time")

            # Process each stat column
            for stat_col, suffix in [
                ("p99", ".p99"),
                ("p90", ".p90"),
                ("p50", ".p50"),
                ("sum", ".sum"),
                ("count", ".count"),
            ]:
                # Abnormal period
                if len(abnormal_hist) > 0 and stat_col in abnormal_hist.columns:
                    abnormal_grouped = abnormal_hist.group_by([entity_col, "metric"]).agg(
                        [pl.col("time").alias("timestamps"), pl.col(stat_col).alias("values")]
                    )

                    for row in abnormal_grouped.iter_rows(named=True):
                        entity = row[entity_col]
                        metric = row["metric"]

                        if entity not in result:
                            result[entity] = ({}, {})

                        abnormal_metrics = result[entity][1]
                        abnormal_metrics[f"{metric}{suffix}"] = (
                            _ts_to_int_seconds(row["timestamps"]),
                            np.array(row["values"]),
                        )

                # Baseline period
                if len(baseline_hist) > 0 and stat_col in baseline_hist.columns:
                    baseline_grouped = baseline_hist.group_by([entity_col, "metric"]).agg(
                        [pl.col("time").alias("timestamps"), pl.col(stat_col).alias("values")]
                    )

                    for row in baseline_grouped.iter_rows(named=True):
                        entity = row[entity_col]
                        metric = row["metric"]

                        if entity not in result:
                            result[entity] = ({}, {})

                        baseline_metrics = result[entity][0]
                        baseline_metrics[f"{metric}{suffix}"] = (
                            _ts_to_int_seconds(row["timestamps"]),
                            np.array(row["values"]),
                        )

        # Process sum metrics - optimized with group_by
        if sum_metrics_list:
            sum_filter = entity_filter & pl.col("metric").is_in(sum_metrics_list)
            baseline_sum = self._baseline_metrics_sum.filter(sum_filter).sort("time")
            abnormal_sum = self._abnormal_metrics_sum.filter(sum_filter).sort("time")

            # Abnormal period
            if len(abnormal_sum) > 0:
                abnormal_grouped = abnormal_sum.group_by([entity_col, "metric"]).agg(
                    [pl.col("time").alias("timestamps"), pl.col("value").alias("values")]
                )

                for row in abnormal_grouped.iter_rows(named=True):
                    entity = row[entity_col]
                    metric = row["metric"]

                    if entity not in result:
                        result[entity] = ({}, {})

                    abnormal_metrics = result[entity][1]
                    abnormal_metrics[metric] = (_ts_to_int_seconds(row["timestamps"]), np.array(row["values"]))

            # Baseline period
            if len(baseline_sum) > 0:
                baseline_grouped = baseline_sum.group_by([entity_col, "metric"]).agg(
                    [pl.col("time").alias("timestamps"), pl.col("value").alias("values")]
                )

                for row in baseline_grouped.iter_rows(named=True):
                    entity = row[entity_col]
                    metric = row["metric"]

                    if entity not in result:
                        result[entity] = ({}, {})

                    baseline_metrics = result[entity][0]
                    baseline_metrics[metric] = (_ts_to_int_seconds(row["timestamps"]), np.array(row["values"]))

        return result

    def _extract_pod_metrics(
        self, pod_name: str, selected_metrics: list[str]
    ) -> tuple[dict[str, tuple[np.ndarray, np.ndarray]], dict[str, tuple[np.ndarray, np.ndarray]]]:
        """Extract pod-level metrics including gauge, histogram, and sum types.

        Pod nodes only contain metrics at the pod level (e.g., k8s.pod.*).
        Container-level metrics (e.g., container.*) belong to container nodes.

        Returns:
            Tuple of (baseline_metrics, abnormal_metrics)
            Each dict: metric_name -> (timestamps, values)
            For histogram metrics, extracts p99, p90, p50, avg, count as separate series
        """
        # Use batch extraction for single pod
        batch_result = self._batch_extract_entity_metrics(
            entity_col="attr.k8s.pod.name",
            entity_names=[pod_name],
            selected_metrics=selected_metrics,
            filter_null_container=True,
        )
        if pod_name not in batch_result:
            logger.warning(f"No metrics found for pod {pod_name}, returning empty metrics")
            return {}, {}
        return batch_result[pod_name]

    def _extract_container_metrics(
        self, container_name: str, selected_metrics: list[str]
    ) -> tuple[dict[str, tuple[np.ndarray, np.ndarray]], dict[str, tuple[np.ndarray, np.ndarray]]]:
        """Extract metrics for a specific container including gauge, histogram, and sum types.

        Returns:
            Tuple of (baseline_metrics, abnormal_metrics)
            Each dict: metric_name -> (timestamps, values)
            For histogram metrics, extracts p99, p90, p50, avg, count as separate series
        """
        # Filter to only gauge metrics (containers don't have histogram/sum attribution)
        histogram_metrics = {
            "hubble_http_request_duration_seconds",
            "http.server.request.duration",
            "http.client.request.duration",
            "db.client.connections.use_time",
            "db.client.connections.wait_time",
            "jvm.gc.duration",
        }
        sum_metrics = {
            "hubble_http_requests_total",
            "k8s.pod.network.io",
            "k8s.pod.network.errors",
            "hubble_drop_total",
        }
        gauge_only = [m for m in selected_metrics if m not in histogram_metrics and m not in sum_metrics]

        if not gauge_only:
            return {}, {}

        # Use batch extraction for single container
        batch_result = self._batch_extract_entity_metrics(
            entity_col="attr.k8s.container.name",
            entity_names=[container_name],
            selected_metrics=gauge_only,
            filter_null_container=False,
        )
        if container_name not in batch_result:
            logger.warning(f"No metrics found for container {container_name}, returning empty metrics")
            return {}, {}
        return batch_result[container_name]

    def _build_service_nodes(self, selected_metrics: list[str]) -> list[Node]:
        """Build service-level nodes with aggregated metrics."""
        assert self._abnormal_metrics is not None

        nodes = []
        services = self._abnormal_metrics.select("service_name").unique().drop_nulls().to_series().to_list()

        for service in services:
            # Extract metrics for this service
            baseline_metrics, abnormal_metrics = self._extract_service_metrics(service, selected_metrics)

            if not baseline_metrics and not abnormal_metrics:
                logger.warning(f"No metrics found for service {service}, skipping")
                continue

            node = Node(
                kind=PlaceKind.service,
                self_name=service,
                baseline_metrics=baseline_metrics,
                abnormal_metrics=abnormal_metrics,
            )
            nodes.append(node)

        return nodes

    def _build_endpoint_nodes(self, selected_metrics: list[str]) -> list[Node]:
        """Build endpoint-level nodes (service::endpoint)."""

        # Get unique service-endpoint pairs from traces
        # For now, use service-level since traces don't have endpoint info in the schema
        # This can be extended when endpoint data is available
        logger.warning("Endpoint granularity not fully supported with current schema, using service-level")
        return self._build_service_nodes(selected_metrics)

    def _extract_service_metrics(
        self, service: str, selected_metrics: list[str]
    ) -> tuple[dict[str, tuple[np.ndarray, np.ndarray]], dict[str, tuple[np.ndarray, np.ndarray]]]:
        """
        Extract and process metrics for a service including gauge, histogram, and sum types.

        Returns:
            Tuple of (baseline_metrics, abnormal_metrics)
            Each dict: metric_name -> (timestamps, values)
            For histogram metrics, extracts p99, p90, p50, avg, count as separate series
        """
        assert self._baseline_metrics is not None and self._abnormal_metrics is not None
        assert self._baseline_metrics_hist is not None and self._abnormal_metrics_hist is not None
        assert self._baseline_metrics_sum is not None and self._abnormal_metrics_sum is not None

        baseline_metrics: dict[str, tuple[np.ndarray, np.ndarray]] = {}
        abnormal_metrics: dict[str, tuple[np.ndarray, np.ndarray]] = {}

        for metric_name in selected_metrics:
            # Determine metric type based on name
            is_histogram = metric_name in {
                "hubble_http_request_duration_seconds",
                "http.server.request.duration",
                "http.client.request.duration",
                "db.client.connections.use_time",
                "db.client.connections.wait_time",
                "jvm.gc.duration",
            }
            is_sum = metric_name in {
                "hubble_http_requests_total",
                "k8s.pod.network.io",
                "k8s.pod.network.errors",
                "hubble_drop_total",
            }

            if is_histogram:
                # Extract histogram metrics for service
                self._extract_histogram_metric_for_service(service, metric_name, baseline_metrics, abnormal_metrics)
            elif is_sum:
                # Extract sum metrics for service
                self._extract_sum_metric_for_service(service, metric_name, baseline_metrics, abnormal_metrics)
            else:
                # Get baseline gauge data
                baseline_data = (
                    self._baseline_metrics.filter(
                        (pl.col("service_name") == service) & (pl.col("metric") == metric_name)
                    )
                    .sort("time")
                    .select(["time", "value"])
                )
                baseline_timestamps = _ts_to_int_seconds(baseline_data.select("time").to_series().to_list())
                baseline_values = baseline_data.select("value").to_series().to_numpy()

                # Get abnormal gauge data
                abnormal_data = (
                    self._abnormal_metrics.filter(
                        (pl.col("service_name") == service) & (pl.col("metric") == metric_name)
                    )
                    .sort("time")
                    .select(["time", "value"])
                )
                abnormal_timestamps = _ts_to_int_seconds(abnormal_data.select("time").to_series().to_list())
                abnormal_values = abnormal_data.select("value").to_series().to_numpy()

                if len(abnormal_values) == 0:
                    continue

                if len(baseline_values) > 0:
                    baseline_metrics[metric_name] = (baseline_timestamps, baseline_values)
                abnormal_metrics[metric_name] = (abnormal_timestamps, abnormal_values)

        return baseline_metrics, abnormal_metrics

    def _extract_histogram_metric_for_service(
        self,
        service_name: str,
        metric_name: str,
        baseline_metrics: dict[str, tuple[np.ndarray, np.ndarray]],
        abnormal_metrics: dict[str, tuple[np.ndarray, np.ndarray]],
    ) -> None:
        """Extract histogram metric for service (p99, p90, p50, avg, count as separate time series)."""
        assert self._baseline_metrics_hist is not None and self._abnormal_metrics_hist is not None

        baseline_data = self._baseline_metrics_hist.filter(
            (pl.col("service_name") == service_name) & (pl.col("metric") == metric_name)
        ).sort("time")

        abnormal_data = self._abnormal_metrics_hist.filter(
            (pl.col("service_name") == service_name) & (pl.col("metric") == metric_name)
        ).sort("time")

        for stat_col, suffix in [
            ("p99", ".p99"),
            ("p90", ".p90"),
            ("p50", ".p50"),
            ("sum", ".sum"),
            ("count", ".count"),
        ]:
            if stat_col in abnormal_data.columns:
                abnormal_ts = abnormal_data.select("time").to_series().to_numpy()
                abnormal_values = abnormal_data.select(stat_col).to_series().to_numpy()

                if len(abnormal_values) > 0:
                    abnormal_metrics[f"{metric_name}{suffix}"] = (abnormal_ts, abnormal_values)

                    if stat_col in baseline_data.columns and len(baseline_data) > 0:
                        baseline_ts = baseline_data.select("time").to_series().to_numpy()
                        baseline_values = baseline_data.select(stat_col).to_series().to_numpy()
                        baseline_metrics[f"{metric_name}{suffix}"] = (baseline_ts, baseline_values)

    def _extract_sum_metric_for_service(
        self,
        service_name: str,
        metric_name: str,
        baseline_metrics: dict[str, tuple[np.ndarray, np.ndarray]],
        abnormal_metrics: dict[str, tuple[np.ndarray, np.ndarray]],
    ) -> None:
        """Extract sum (counter) metric for service."""
        assert self._baseline_metrics_sum is not None and self._abnormal_metrics_sum is not None

        baseline_data = (
            self._baseline_metrics_sum.filter(
                (pl.col("service_name") == service_name) & (pl.col("metric") == metric_name)
            )
            .sort("time")
            .select(["time", "value"])
        )

        abnormal_data = (
            self._abnormal_metrics_sum.filter(
                (pl.col("service_name") == service_name) & (pl.col("metric") == metric_name)
            )
            .sort("time")
            .select(["time", "value"])
        )

        if len(abnormal_data) > 0:
            abnormal_ts = abnormal_data.select("time").to_series().to_numpy()
            abnormal_values = abnormal_data.select("value").to_series().to_numpy()
            abnormal_metrics[metric_name] = (abnormal_ts, abnormal_values)

            if len(baseline_data) > 0:
                baseline_ts = baseline_data.select("time").to_series().to_numpy()
                baseline_values = baseline_data.select("value").to_series().to_numpy()
                baseline_metrics[metric_name] = (baseline_ts, baseline_values)

    def _compute_residual(self, abnormal: np.ndarray, baseline: np.ndarray) -> np.ndarray:
        """
        Compute residual signal (abnormal - baseline).

        Handles length mismatch by using the minimum length.
        """
        if len(baseline) == 0:
            return abnormal.copy()

        min_len = min(len(abnormal), len(baseline))
        result: np.ndarray = abnormal[:min_len] - baseline[:min_len]
        return result

    def _normalize_residual(self, residual: np.ndarray) -> np.ndarray:
        if len(residual) == 0:
            return residual.copy()

        # Handle all-zero case
        if np.all(residual == 0):
            return residual.copy()

        # Handle NaN values
        if np.all(np.isnan(residual)):
            return np.zeros_like(residual)

        # Filter out NaN for statistics calculation
        valid_residual = residual[~np.isnan(residual)]
        if len(valid_residual) == 0:
            return np.zeros_like(residual)

        # Robust z-score: use median and MAD instead of mean and std
        median = np.median(valid_residual)
        mad = np.median(np.abs(valid_residual - median))

        z_scores: np.ndarray
        if mad == 0:
            # Fall back to standard normalization if MAD is zero
            std = np.std(valid_residual)
            if std == 0:
                return np.zeros_like(residual)
            z_scores = (residual - np.mean(valid_residual)) / std
        else:
            z_scores = (residual - median) / (1.4826 * mad)

        # Replace NaN with 0
        z_scores = np.nan_to_num(z_scores, nan=0.0)

        # Clip to [-3, 3] std then map to [0, 1]
        clipped = np.clip(z_scores, -3, 3)
        normalized: np.ndarray = (clipped + 3) / 6

        return normalized

    def _build_span_nodes(
        self,
        graph: HyperGraph,
        baseline_traces_df: pl.DataFrame,
        abnormal_traces_df: pl.DataFrame,
        baseline_logs_df: pl.DataFrame,
        abnormal_logs_df: pl.DataFrame,
    ) -> None:
        """
        Build span nodes from trace data using (service_name, span_name) as identifier.

        Each span node is uniquely identified by its service and span_name combination,
        which prevents ambiguity when the same span_name appears in multiple services
        (e.g., TripRepository.findByTripId in both ts-travel-service and ts-travel2-service).

        The span node's self_name format is: "{service_name}::{span_name}"
        This ensures that spans with the same name but in different services are distinct nodes.
        """
        # Process baseline traces - group by (service_name, span_name)
        baseline_valid = baseline_traces_df.drop_nulls(subset=["span_name", "service_name"]).with_columns(
            [
                (pl.col("duration") / 1e9).alias("duration_sec"),
                (
                    pl.col("attr.http.response.status_code").is_not_null()
                    & (pl.col("attr.http.response.status_code") >= 500)
                )
                .cast(pl.Int32)
                .alias("is_error"),
            ]
        )

        # Add 5-second time bucketing for time-series metrics
        baseline_with_time = baseline_valid.with_columns(pl.col("time").dt.truncate("5s").alias("time_window"))

        # Group by (service_name, span_name) instead of just span_name
        baseline_agg = (
            baseline_with_time.group_by(["service_name", "span_name", "time_window"])
            .agg(
                [
                    pl.len().alias("baseline_call_count"),
                    pl.col("is_error").sum().alias("baseline_error_count"),
                    pl.col("duration_sec").mean().alias("baseline_avg_duration"),
                    pl.col("duration_sec").quantile(0.5).alias("baseline_p50_duration"),
                    pl.col("duration_sec").quantile(0.99).alias("baseline_p99_duration"),
                    pl.col("duration_sec").max().alias("baseline_max_duration"),
                ]
            )
            .with_columns((pl.col("baseline_error_count") / pl.col("baseline_call_count")).alias("baseline_error_rate"))
            .sort(["service_name", "span_name", "time_window"])
        )

        # Process abnormal traces
        abnormal_valid = abnormal_traces_df.drop_nulls(subset=["span_name", "service_name"])

        if len(abnormal_valid) == 0:
            logger.warning("No valid abnormal traces found for building span nodes")
            return

        abnormal_valid = abnormal_valid.with_columns(
            [
                (pl.col("duration") / 1e9).alias("duration_sec"),
                (
                    pl.col("attr.http.response.status_code").is_not_null()
                    & (pl.col("attr.http.response.status_code") >= 500)
                )
                .cast(pl.Int32)
                .alias("is_error"),
            ]
        )

        # Add 5-second time bucketing for time-series metrics
        abnormal_with_time = abnormal_valid.with_columns(pl.col("time").dt.truncate("5s").alias("time_window"))

        # Group by (service_name, span_name) instead of just span_name
        abnormal_agg = (
            abnormal_with_time.group_by(["service_name", "span_name", "time_window"])
            .agg(
                [
                    pl.len().alias("call_count"),
                    pl.col("is_error").sum().alias("error_count"),
                    pl.col("duration_sec").mean().alias("avg_duration"),
                    pl.col("duration_sec").quantile(0.5).alias("p50_duration"),
                    pl.col("duration_sec").quantile(0.99).alias("p99_duration"),
                    pl.col("duration_sec").max().alias("max_duration"),
                ]
            )
            .with_columns((pl.col("error_count") / pl.col("call_count")).alias("error_rate"))
            .sort(["service_name", "span_name", "time_window"])
        )

        # Get unique (service_name, span_name) pairs from both periods
        all_service_span_pairs: set[tuple[str, str]] = set()
        if len(baseline_agg) > 0:
            for row in baseline_agg.select(["service_name", "span_name"]).unique().iter_rows():
                all_service_span_pairs.add((row[0], row[1]))
        if len(abnormal_agg) > 0:
            for row in abnormal_agg.select(["service_name", "span_name"]).unique().iter_rows():
                all_service_span_pairs.add((row[0], row[1]))

        # Create span nodes with time-series metrics
        filtered_count = 0
        for service_name, span_name in sorted(all_service_span_pairs):
            if span_name is None or service_name is None:
                continue

            # Filter out pure HTTP method spans without endpoint paths (e.g., 'GET', 'POST')
            if is_generic_http_method_span(span_name):
                filtered_count += 1
                continue

            # Create unique span identifier: service_name::span_name
            span_full_name = f"{service_name}::{span_name}"

            # Extract trace time-series metrics
            baseline_trace_metrics: dict[str, tuple[np.ndarray, np.ndarray]] = {}
            abnormal_trace_metrics: dict[str, tuple[np.ndarray, np.ndarray]] = {}

            # Extract baseline trace time-series
            baseline_span_data = baseline_agg.filter(
                (pl.col("service_name") == service_name) & (pl.col("span_name") == span_name)
            )
            if len(baseline_span_data) > 0:
                timestamps = baseline_span_data.select("time_window").to_series().to_numpy()
                baseline_trace_metrics["request_count"] = (
                    timestamps,
                    baseline_span_data.select("baseline_call_count").to_series().to_numpy().astype(np.float64),
                )
                baseline_trace_metrics["error_rate"] = (
                    timestamps,
                    baseline_span_data.select("baseline_error_rate").to_series().to_numpy().astype(np.float64),
                )
                baseline_trace_metrics["avg_duration"] = (
                    timestamps,
                    baseline_span_data.select("baseline_avg_duration").to_series().to_numpy().astype(np.float64),
                )
                baseline_trace_metrics["p50_duration"] = (
                    timestamps,
                    baseline_span_data.select("baseline_p50_duration").to_series().to_numpy().astype(np.float64),
                )
                baseline_trace_metrics["p99_duration"] = (
                    timestamps,
                    baseline_span_data.select("baseline_p99_duration").to_series().to_numpy().astype(np.float64),
                )
                baseline_trace_metrics["max_duration"] = (
                    timestamps,
                    baseline_span_data.select("baseline_max_duration").to_series().to_numpy().astype(np.float64),
                )

            # Extract abnormal trace time-series
            abnormal_span_data = abnormal_agg.filter(
                (pl.col("service_name") == service_name) & (pl.col("span_name") == span_name)
            )
            if len(abnormal_span_data) > 0:
                timestamps = abnormal_span_data.select("time_window").to_series().to_numpy()
                abnormal_trace_metrics["request_count"] = (
                    timestamps,
                    abnormal_span_data.select("call_count").to_series().to_numpy().astype(np.float64),
                )
                abnormal_trace_metrics["error_rate"] = (
                    timestamps,
                    abnormal_span_data.select("error_rate").to_series().to_numpy().astype(np.float64),
                )
                abnormal_trace_metrics["avg_duration"] = (
                    timestamps,
                    abnormal_span_data.select("avg_duration").to_series().to_numpy().astype(np.float64),
                )
                abnormal_trace_metrics["p50_duration"] = (
                    timestamps,
                    abnormal_span_data.select("p50_duration").to_series().to_numpy().astype(np.float64),
                )
                abnormal_trace_metrics["p99_duration"] = (
                    timestamps,
                    abnormal_span_data.select("p99_duration").to_series().to_numpy().astype(np.float64),
                )
                abnormal_trace_metrics["max_duration"] = (
                    timestamps,
                    abnormal_span_data.select("max_duration").to_series().to_numpy().astype(np.float64),
                )

            # Note: Log metrics extraction is skipped for now as it requires span_id mapping
            # which is more complex with the new (service, span) grouping
            baseline_metrics = baseline_trace_metrics
            abnormal_metrics = abnormal_trace_metrics

            node = Node(
                kind=PlaceKind.span,
                self_name=span_full_name,
                baseline_metrics=baseline_metrics,
                abnormal_metrics=abnormal_metrics,
            )
            graph.add_node(node, strict=False)

            # Create includes edge: service -> span
            service_node = graph.get_node_by_name(f"{PlaceKind.service}|{service_name}")
            span_node = graph.get_node_by_name(f"{PlaceKind.span}|{span_full_name}")

            if service_node and span_node:
                assert service_node.id is not None and span_node.id is not None
                edge = Edge(
                    src_id=service_node.id,
                    dst_id=span_node.id,
                    src_name=f"{PlaceKind.service}|{service_name}",
                    dst_name=f"{PlaceKind.span}|{span_full_name}",
                    kind=DepKind.includes,
                    weight=1.0,
                    data=None,
                )
                graph.add_edge(edge, strict=False)

        logger.info(
            f"Built {len(all_service_span_pairs) - filtered_count} span nodes "
            f"(filtered out {filtered_count} generic HTTP method spans)"
        )

    def _batch_extract_span_log_metrics(
        self,
        span_names: list[str],
        baseline_traces_df: pl.DataFrame,
        abnormal_traces_df: pl.DataFrame,
        baseline_logs_df: pl.DataFrame,
        abnormal_logs_df: pl.DataFrame,
    ) -> dict[str, tuple[dict[str, tuple[np.ndarray, np.ndarray]], dict[str, tuple[np.ndarray, np.ndarray]]]]:
        """
        Batch extract log metrics for multiple spans.

        Returns:
            Dict mapping span_name -> (baseline_metrics, abnormal_metrics)
        """
        result: dict[
            str, tuple[dict[str, tuple[np.ndarray, np.ndarray]], dict[str, tuple[np.ndarray, np.ndarray]]]
        ] = {}

        # Build span_name -> span_ids mapping for baseline
        if "level" in baseline_logs_df.columns and "span_id" in baseline_logs_df.columns:
            baseline_span_mapping = (
                baseline_traces_df.filter(pl.col("span_name").is_in(span_names))
                .select(["span_name", "span_id"])
                .unique()
            )

            # Join logs with span mapping and aggregate
            baseline_logs_joined = baseline_logs_df.join(baseline_span_mapping, on="span_id", how="inner").with_columns(
                [
                    pl.col("time").dt.truncate("5s").alias("time_window"),
                    (pl.col("level") == "ERROR").cast(pl.Int32).alias("is_error"),
                    (pl.col("level") == "WARN").cast(pl.Int32).alias("is_warn"),
                ]
            )

            baseline_agg = (
                baseline_logs_joined.group_by(["span_name", "time_window"])
                .agg(
                    [
                        pl.col("is_error").sum().alias("error_count"),
                        pl.col("is_warn").sum().alias("warn_count"),
                        pl.len().alias("total_count"),
                    ]
                )
                .sort(["span_name", "time_window"])
            )

            # Extract per span
            for span in span_names:
                span_data = baseline_agg.filter(pl.col("span_name") == span)
                baseline_metrics: dict[str, tuple[np.ndarray, np.ndarray]] = {}

                if len(span_data) > 0:
                    timestamps = span_data.select("time_window").to_series().to_numpy()
                    baseline_metrics["log.error_count"] = (
                        timestamps,
                        span_data.select("error_count").to_series().to_numpy().astype(np.float64),
                    )
                    baseline_metrics["log.warn_count"] = (
                        timestamps,
                        span_data.select("warn_count").to_series().to_numpy().astype(np.float64),
                    )
                    baseline_metrics["log.total_count"] = (
                        timestamps,
                        span_data.select("total_count").to_series().to_numpy().astype(np.float64),
                    )

                result[span] = (baseline_metrics, {})

        # Build span_name -> span_ids mapping for abnormal
        if "level" in abnormal_logs_df.columns and "span_id" in abnormal_logs_df.columns:
            abnormal_span_mapping = (
                abnormal_traces_df.filter(pl.col("span_name").is_in(span_names))
                .select(["span_name", "span_id"])
                .unique()
            )

            # Join logs with span mapping and aggregate
            abnormal_logs_joined = abnormal_logs_df.join(abnormal_span_mapping, on="span_id", how="inner").with_columns(
                [
                    pl.col("time").dt.truncate("5s").alias("time_window"),
                    (pl.col("level") == "ERROR").cast(pl.Int32).alias("is_error"),
                    (pl.col("level") == "WARN").cast(pl.Int32).alias("is_warn"),
                ]
            )

            abnormal_agg = (
                abnormal_logs_joined.group_by(["span_name", "time_window"])
                .agg(
                    [
                        pl.col("is_error").sum().alias("error_count"),
                        pl.col("is_warn").sum().alias("warn_count"),
                        pl.len().alias("total_count"),
                    ]
                )
                .sort(["span_name", "time_window"])
            )

            # Extract per span
            for span in span_names:
                span_data = abnormal_agg.filter(pl.col("span_name") == span)
                abnormal_metrics: dict[str, tuple[np.ndarray, np.ndarray]] = {}

                if len(span_data) > 0:
                    timestamps = span_data.select("time_window").to_series().to_numpy()
                    abnormal_metrics["log.error_count"] = (
                        timestamps,
                        span_data.select("error_count").to_series().to_numpy().astype(np.float64),
                    )
                    abnormal_metrics["log.warn_count"] = (
                        timestamps,
                        span_data.select("warn_count").to_series().to_numpy().astype(np.float64),
                    )
                    abnormal_metrics["log.total_count"] = (
                        timestamps,
                        span_data.select("total_count").to_series().to_numpy().astype(np.float64),
                    )

                if span in result:
                    baseline_metrics, _ = result[span]
                    result[span] = (baseline_metrics, abnormal_metrics)
                else:
                    result[span] = ({}, abnormal_metrics)

        # Fill in missing spans with empty metrics
        for span in span_names:
            if span not in result:
                result[span] = ({}, {})

        return result

    def _build_edges_from_traces(
        self,
        baseline_traces_df: pl.DataFrame,
        abnormal_traces_df: pl.DataFrame,
        graph: HyperGraph,
    ) -> list[Edge]:
        """
        Build logical edges from trace data at span level.

        Creates span->span call edges based on parent_span_id relationships.
        Aggregates both baseline and abnormal statistics for each edge.
        Service-to-service edges are NOT created.

        Span names use the format "{service_name}::{span_name}" to uniquely
        identify spans across different services.
        """
        edges: list[Edge] = []

        # Process baseline and abnormal traces separately
        baseline_edge_stats = self._aggregate_trace_edges(baseline_traces_df)
        abnormal_edge_stats = self._aggregate_trace_edges(abnormal_traces_df)

        # Merge baseline and abnormal statistics
        all_edge_pairs = set(baseline_edge_stats.keys()) | set(abnormal_edge_stats.keys())

        # Create span->span call edges
        for edge_key in all_edge_pairs:
            parent_full_name, child_full_name = edge_key

            # Edge may exist only in baseline or abnormal period - empty dict is valid default
            baseline_stats = baseline_edge_stats.get(edge_key, {})
            abnormal_stats = abnormal_edge_stats.get(edge_key, {})

            parent_node = graph.get_node_by_name(f"{PlaceKind.span}|{parent_full_name}")
            child_node = graph.get_node_by_name(f"{PlaceKind.span}|{child_full_name}")

            if not parent_node or not child_node:
                logger.debug(f"Skipping edge {parent_full_name} -> {child_full_name}: nodes not found")
                continue

            assert parent_node.id is not None and child_node.id is not None

            # Helper to safely extract stat with explicit default for missing period
            def get_stat(stats: dict, key: str, default: float = 0.0) -> float:
                return float(stats[key]) if key in stats else default

            # Create edge from parent to child with both baseline and abnormal stats
            edge = Edge(
                src_id=parent_node.id,
                dst_id=child_node.id,
                src_name=f"{PlaceKind.span}|{parent_full_name}",
                dst_name=f"{PlaceKind.span}|{child_full_name}",
                kind=DepKind.calls,
                weight=get_stat(abnormal_stats, "call_count"),
                data=CallsEdgeData(
                    baseline_call_count=int(get_stat(baseline_stats, "call_count")),
                    baseline_error_count=int(get_stat(baseline_stats, "error_count")),
                    baseline_avg_latency=get_stat(baseline_stats, "avg_latency"),
                    baseline_median_latency=get_stat(baseline_stats, "median_latency"),
                    baseline_p90_latency=get_stat(baseline_stats, "p90_latency"),
                    baseline_p99_latency=get_stat(baseline_stats, "p99_latency"),
                    abnormal_call_count=int(get_stat(abnormal_stats, "call_count")),
                    abnormal_error_count=int(get_stat(abnormal_stats, "error_count")),
                    abnormal_avg_latency=get_stat(abnormal_stats, "avg_latency"),
                    abnormal_median_latency=get_stat(abnormal_stats, "median_latency"),
                    abnormal_p90_latency=get_stat(abnormal_stats, "p90_latency"),
                    abnormal_p99_latency=get_stat(abnormal_stats, "p99_latency"),
                ),
            )
            edges.append(edge)

        return edges

    def _aggregate_trace_edges(self, traces_df: pl.DataFrame) -> dict[tuple[str, str], dict[str, float]]:
        """
        Aggregate trace edges (parent->child span relationships) with statistics.

        Generic HTTP method spans (GET, POST, etc.) are treated as bridges:
        if parent -> GET -> child exists, it becomes parent -> child directly.

        Span names are prefixed with service_name to create unique identifiers:
        "{service_name}::{span_name}"

        Args:
            traces_df: Trace dataframe

        Returns:
            Dictionary mapping (parent_full_name, child_full_name) -> stats dict
            where full_name = "{service_name}::{span_name}"
            Stats include: call_count, error_count, avg_latency, median_latency, p90_latency, p99_latency
        """
        # Filter valid spans with span_name and service_name
        valid_spans = traces_df.drop_nulls(subset=["span_name", "service_name"])

        if len(valid_spans) == 0:
            logger.warning("No valid spans found for aggregating edges")
            return {}

        # Build span_id -> (span_name, service_name, parent_span_id) mapping for bridge resolution
        span_info = {}
        for row in valid_spans.select(["trace_id", "span_id", "span_name", "service_name", "parent_span_id"]).iter_rows(
            named=True
        ):
            key = (row["trace_id"], row["span_id"])
            span_info[key] = {
                "span_name": row["span_name"],
                "service_name": row["service_name"],
                "parent_span_id": row["parent_span_id"],
            }

        def resolve_real_parent(trace_id: str, parent_span_id: str | None, max_depth: int = 10) -> str | None:
            """
            Resolve the real parent by skipping generic HTTP method spans.
            Returns the parent_span_id of the first non-generic ancestor.
            """
            current_parent_id = parent_span_id
            depth = 0
            while current_parent_id is not None and depth < max_depth:
                parent_key = (trace_id, current_parent_id)
                if parent_key not in span_info:
                    # Parent span not found, return current
                    return current_parent_id
                parent_info = span_info[parent_key]
                parent_name = parent_info["span_name"]
                # If parent is a generic HTTP method span, skip to its parent
                if is_generic_http_method_span(parent_name):
                    current_parent_id = parent_info["parent_span_id"]
                    depth += 1
                else:
                    # Found a non-generic parent
                    return current_parent_id
            return current_parent_id

        # Create resolved parent_span_id for each span
        resolved_parents = []
        for row in valid_spans.select(["trace_id", "span_id", "parent_span_id"]).iter_rows(named=True):
            resolved_parent_id = resolve_real_parent(row["trace_id"], row["parent_span_id"])
            resolved_parents.append(
                {
                    "trace_id": row["trace_id"],
                    "span_id": row["span_id"],
                    "resolved_parent_span_id": resolved_parent_id,
                }
            )

        resolved_df = pl.DataFrame(resolved_parents)

        # Join resolved parents back to valid_spans
        valid_spans = valid_spans.join(resolved_df, on=["trace_id", "span_id"], how="left")

        # Convert duration to seconds and mark errors
        valid_spans = valid_spans.with_columns(
            [
                (pl.col("duration") / 1e9).alias("duration_sec"),
                (
                    pl.col("attr.http.response.status_code").is_not_null()
                    & (pl.col("attr.http.response.status_code") >= 500)
                )
                .cast(pl.Int32)
                .alias("is_error"),
                # Create full span name: service_name::span_name
                (pl.col("service_name") + "::" + pl.col("span_name")).alias("child_full_name"),
            ]
        )

        # Filter out generic HTTP method spans from being children (they are bridges, not endpoints)
        valid_spans = valid_spans.filter(
            ~pl.col("span_name").map_elements(is_generic_http_method_span, return_dtype=pl.Boolean)
        )

        # Build parent info with full name
        parent_spans = valid_spans.select(
            [
                pl.col("trace_id"),
                pl.col("span_id").alias("resolved_parent_span_id"),
                (pl.col("service_name") + "::" + pl.col("span_name")).alias("parent_full_name"),
            ]
        )

        # Merge child spans with their resolved parents
        call_edges = valid_spans.join(parent_spans, on=["trace_id", "resolved_parent_span_id"], how="inner")

        # Group by parent-child span pairs (using full names) and aggregate
        agg_exprs = [
            pl.len().alias("call_count"),
            pl.col("is_error").sum().alias("error_count"),
            pl.col("duration_sec").mean().alias("avg_latency"),
            pl.col("duration_sec").quantile(0.5).alias("median_latency"),
            pl.col("duration_sec").quantile(0.9).alias("p90_latency"),
            pl.col("duration_sec").quantile(0.99).alias("p99_latency"),
        ]

        edge_agg = call_edges.group_by(["parent_full_name", "child_full_name"]).agg(agg_exprs)

        # Filter out self-loops
        edge_agg = edge_agg.filter(pl.col("parent_full_name") != pl.col("child_full_name"))

        # Convert to dictionary
        result = {}
        for row in edge_agg.iter_rows(named=True):
            key = (str(row["parent_full_name"]), str(row["child_full_name"]))
            result[key] = {
                "call_count": int(row["call_count"]),
                "error_count": int(row["error_count"]),
                "avg_latency": float(row["avg_latency"]),
                "median_latency": float(row["median_latency"]),
                "p90_latency": float(row["p90_latency"]),
                "p99_latency": float(row["p99_latency"]),
            }

        return result
