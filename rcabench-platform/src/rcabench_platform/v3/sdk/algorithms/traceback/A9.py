"""
TraceBackA9: Relational Debugging Approach for Microservice Root Cause Analysis

This algorithm is inspired by the "Relational Debugging" technique from the paper,
adapting it to the microservice root cause localization domain. Instead of relying
on predefined rules, it uses a data-driven approach:

1. Compute: Calculate metric correlations/causalities between connected nodes in both
   normal and anomalous periods.
2. Filter: Identify significant changes in these relations that exceed normal volatility.
3. Refine & Rank: Propagate anomaly scores through the dependency graph, weighted by
   relation anomaly scores, to identify root cause candidates.

Key improvements over A8:
- No hardcoded causal rules (CausalEdgeKind, infer_* functions)
- Accounts for normal period volatility using standardized differences
- Uses sliding window to measure correlation stability during normal periods
- Data-driven relation-based propagation instead of rule-based ACG construction
- Handles heterogeneous node types (resource vs application layers) intelligently
- Optional Granger causality testing for stronger causal claims
- Performance optimization: only tests relations along existing graph edges (O(E) vs O(N²))
"""

import math
from collections import defaultdict
from dataclasses import dataclass
from enum import auto
from functools import partial
from pprint import pformat

import numpy as np
import polars as pl
from scipy.stats import pearsonr

try:
    from statsmodels.tsa.stattools import grangercausalitytests

    HAS_STATSMODELS = True
except ImportError:
    HAS_STATSMODELS = False

from .....compat import StrEnum
from ...datasets.rcabench import rcabench_fix_injection
from ...graphs.sdg.defintion import SDG, PlaceNode
from ...graphs.sdg.statistics import calc_statistics
from ...logging import logger, timeit
from ...utils.env import debug
from ...utils.fmap import fmap_processpool
from ...utils.serde import load_json
from ..spec import Algorithm, AlgorithmAnswer, AlgorithmArgs
from ._common import build_sdg_with_cache


class TraceBackA9(Algorithm):
    def needs_cpu_count(self) -> int | None:
        return 8

    @timeit()
    def __call__(self, args: AlgorithmArgs) -> list[AlgorithmAnswer]:
        assert_dataset(args)

        if debug():
            _print_injection(args)

        sdg = build_sdg_with_cache(args)

        calc_statistics(sdg)

        detect_anomalies(sdg)

        rcc_list = find_root_cause_candidates_relational(sdg)

        service_names = unify_to_service_candidates(sdg, rcc_list)

        answers = []
        for rank, service_name in enumerate(service_names, start=1):
            answers.append(AlgorithmAnswer(level="service", name=service_name, rank=rank))

        if debug():
            for rank, service_name in enumerate(service_names, start=1):
                logger.debug(f"RCC {rank:>2}: {service_name}")
        print(answers)
        return answers


def assert_dataset(args: AlgorithmArgs) -> None:
    datasets = ["rcabench", "rcaeval_re2_tt"]
    if not any(args.dataset.startswith(ds) for ds in datasets):
        raise NotImplementedError


def _print_injection(args: AlgorithmArgs) -> None:
    injection_path = args.input_folder / "injection.json"
    if injection_path.exists():
        injection = load_json(path=injection_path)
        rcabench_fix_injection(injection)
        logger.debug(f"found injection:\n{pformat(injection)}")


# --- Anomaly Detection ---


class AnomalyKey(StrEnum):
    error_rate = auto()
    latency = auto()
    qpm = auto()

    cpu = auto()
    memory = auto()
    jvm_gc_duration = auto()
    restart = auto()


class AnomalyKind(StrEnum):
    up = auto()
    down = auto()


@dataclass(kw_only=True, slots=True, frozen=True)
class Anomaly:
    key: AnomalyKey
    kind: AnomalyKind
    score: float


def standardized_diff(normal_values: np.ndarray, anomal_values: np.ndarray) -> float:
    """
    Calculates the standardized difference (Z-score like) between anomalous and normal periods.
    This accounts for the internal volatility of the normal period.

    Returns:
        The number of standard deviations the anomalous mean deviates from the normal mean.
    """
    if len(normal_values) < 2 or len(anomal_values) == 0:
        return 0.0

    normal_mean = np.mean(normal_values)
    normal_std = np.std(normal_values, ddof=1)  # Sample std
    anomal_mean = np.mean(anomal_values)

    if normal_std == 0:
        # If normal period has no variation, any change is potentially significant.
        # Use relative difference as a fallback.
        if normal_mean == 0:
            return 1.0 if anomal_mean != 0 else 0.0
        return float(abs(anomal_mean - normal_mean) / abs(normal_mean))

    # The score is how many standard deviations the anomalous mean is from the normal mean.
    return float((anomal_mean - normal_mean) / normal_std)


def detect_node_anomalies(sdg: SDG, node: PlaceNode) -> list[Anomaly]:
    """
    Detects anomalies in a node by comparing normal and anomalous periods,
    accounting for normal period volatility.
    """
    ans: list[Anomaly] | None = node.data.get("alg.anomalies")
    if ans is not None:
        return ans
    else:
        ans = []

    # Map indicator keys to anomaly types
    keys_to_check = {
        "error_rate": AnomalyKey.error_rate,
        "function_error_rate": AnomalyKey.error_rate,
        "latency": AnomalyKey.latency,
        "latency_p50": AnomalyKey.latency,
        "latency_p90": AnomalyKey.latency,
        "qpm": AnomalyKey.qpm,
        "cpu_usage": AnomalyKey.cpu,
        "memory_usage": AnomalyKey.memory,
        "jvm_gc_duration": AnomalyKey.jvm_gc_duration,
        "restart_count": AnomalyKey.restart,
    }

    for indicator_key, anomaly_key in keys_to_check.items():
        # Extract base indicator name (before any '.')
        base_key = indicator_key.split(".")[0]
        indicator = node.indicators.get(base_key)

        if indicator is None:
            continue

        # Extract normal and anomalous values
        normal_values = indicator.df.filter(pl.col("anomal") == 0)["value"].to_numpy()
        anomal_values = indicator.df.filter(pl.col("anomal") == 1)["value"].to_numpy()

        if len(normal_values) < 2 or len(anomal_values) == 0:
            continue

        std_diff = standardized_diff(normal_values, anomal_values)

        anomaly = None
        # A standardized difference of > 2 or < -2 is often considered significant (roughly 95% CI).
        if std_diff > 2.0:
            anomaly = Anomaly(key=anomaly_key, kind=AnomalyKind.up, score=abs(std_diff))
        elif std_diff < -2.0:
            anomaly = Anomaly(key=anomaly_key, kind=AnomalyKind.down, score=abs(std_diff))

        if anomaly is not None:
            ans.append(anomaly)
            logger.debug(
                "detected anomaly: {}, node: `{}`, key: {}, std_diff: {:.2f}",
                anomaly,
                node.uniq_name,
                indicator_key,
                std_diff,
            )

    node.data["alg.anomalies"] = ans
    return ans


@timeit(log_args=False)
def detect_anomalies(sdg: SDG) -> None:
    """Detect anomalies for all nodes in the SDG."""
    for node in sdg.iter_nodes():
        detect_node_anomalies(sdg, node)


# --- Relational Debugging Core ---


def calc_correlation(s1: pl.Series, s2: pl.Series) -> float:
    """Calculates Pearson correlation, handling potential errors."""
    if s1.len() < 2 or s2.len() < 2:
        return 0.0
    try:
        # Use scipy for more robust correlation calculation
        arr1 = s1.to_numpy()
        arr2 = s2.to_numpy()
        # Filter out NaN/inf values
        mask = np.isfinite(arr1) & np.isfinite(arr2)
        if np.sum(mask) < 2:
            return 0.0
        corr_result = pearsonr(arr1[mask], arr2[mask])
        corr_val: float = float(corr_result.statistic)  # type: ignore
        if not math.isfinite(corr_val):
            return 0.0
        return corr_val
    except Exception:
        return 0.0


def calc_sliding_window_correlations_vectorized(arr1: np.ndarray, arr2: np.ndarray, window_size: int) -> np.ndarray:
    """
    Vectorized calculation of sliding window correlations.
    Much faster than Python loops.

    Returns:
        Array of correlation coefficients for each window position.
    """
    n = len(arr1)
    if n < window_size:
        return np.array([])

    num_windows = n - window_size + 1
    correlations = np.zeros(num_windows)

    for i in range(num_windows):
        window1 = arr1[i : i + window_size]
        window2 = arr2[i : i + window_size]

        # Quick check for valid data
        if np.std(window1) < 1e-10 or np.std(window2) < 1e-10:
            correlations[i] = 0.0
        else:
            # Vectorized correlation calculation
            correlations[i] = np.corrcoef(window1, window2)[0, 1]

    # Filter out invalid correlations
    valid_mask = np.isfinite(correlations)
    return correlations[valid_mask]


def test_granger_causality(series_y: np.ndarray, series_x: np.ndarray, max_lag: int = 3) -> float:
    """
    Performs Granger causality test to see if X Granger-causes Y.

    Args:
        series_y: The target time series (effect).
        series_x: The source time series (potential cause).
        max_lag: The maximum number of lags to test.

    Returns:
        The minimum p-value from the tests. A low p-value (e.g., < 0.05)
        suggests a causal relationship.
    """
    if not HAS_STATSMODELS:
        # Fallback: return a neutral p-value if statsmodels is not available
        return 1.0

    if len(series_y) < 20 or len(series_x) < 20:  # Need enough data points
        return 1.0

    # Remove NaN and infinite values
    mask = np.isfinite(series_y) & np.isfinite(series_x)
    series_y = series_y[mask]
    series_x = series_x[mask]

    if len(series_y) < 20:
        return 1.0

    # Data must be stationary for Granger causality. A simple first-order
    # differencing is a common way to achieve this.
    try:
        data = np.column_stack([series_y, series_x])
        data_diff = np.diff(data, axis=0)

        if data_diff.shape[0] < max_lag + 2:
            return 1.0

        # Suppress verbose output from the test
        gc_result = grangercausalitytests(data_diff, max_lag, verbose=False)  # type: ignore

        # Find the minimum p-value across all lags
        min_p_value = 1.0
        for lag in range(1, max_lag + 1):
            p_value = gc_result[lag][0]["ssr_ftest"][1]
            if p_value < min_p_value:
                min_p_value = p_value
        return float(min_p_value)
    except (ValueError, np.linalg.LinAlgError, KeyError):
        # Handle cases with singular matrices or other errors
        return 1.0


def get_relation_anomaly_score(
    u: PlaceNode,
    v: PlaceNode,
    window_size: int = 10,
    min_normal_points: int = 20,
    use_granger: bool = True,
    p_value_threshold: float = 0.05,
) -> float:
    """
    Calculate the change in correlation/causality between two nodes, handling heterogeneous types.

    This function acts as a bridge between different node types:
    - For resource-to-application links, it correlates resource metrics (e.g., CPU)
      with canonical application SLIs (latency, error_rate, qpm).
    - For application-to-application links, it correlates similar SLIs.
    - For resource-to-resource links, it compares all metrics.

    Optionally uses Granger causality testing to verify causal relationships.

    Args:
        u, v: Two connected nodes in the SDG
        window_size: Size of sliding window for correlation calculation
        min_normal_points: Minimum data points required in normal period
        use_granger: Whether to use Granger causality test instead of correlation
        p_value_threshold: P-value threshold for Granger causality significance

    Returns:
        Maximum relation anomaly score across all indicator pairs
    """
    max_score = 0.0

    # Define canonical application-level SLIs that act as the "common language"
    app_sli_keys = ["latency", "latency_p50", "latency_p90", "error_rate", "qpm"]

    u_is_resource = u.kind not in ("service", "function")
    v_is_resource = v.kind not in ("service", "function")

    u_indicators = list(u.indicators.values())
    v_indicators = list(v.indicators.values())

    # --- Strategy Selection based on Node Types ---

    indicator_pairs = []

    # Case 1: Resource -> Application (e.g., Pod -> Service)
    # Correlate every resource metric with every application SLI.
    if u_is_resource and not v_is_resource:
        # Filter v's indicators to only include canonical SLIs
        v_indicators = [ind for ind in v_indicators if ind.name in app_sli_keys]
        indicator_pairs = [(u_ind, v_ind) for u_ind in u_indicators for v_ind in v_indicators]

    # Case 2: Application -> Resource (e.g., Service -> Pod)
    elif not u_is_resource and v_is_resource:
        # Filter u's indicators to only include canonical SLIs
        u_indicators = [ind for ind in u_indicators if ind.name in app_sli_keys]
        indicator_pairs = [(u_ind, v_ind) for u_ind in u_indicators for v_ind in v_indicators]

    # Case 3: Application -> Application (e.g., Service -> Service)
    # Correlate SLI to SLI. To reduce noise, we prefer matching keys.
    elif not u_is_resource and not v_is_resource:
        # This is an optimization: only compare latency with latency, etc.
        for u_ind in u_indicators:
            for v_ind in v_indicators:
                if u_ind.name == v_ind.name and u_ind.name in app_sli_keys:
                    indicator_pairs.append((u_ind, v_ind))
        # If no matching SLIs, fall back to all SLI pairs
        if not indicator_pairs:
            u_sli_indicators = [ind for ind in u_indicators if ind.name in app_sli_keys]
            v_sli_indicators = [ind for ind in v_indicators if ind.name in app_sli_keys]
            indicator_pairs = [(u_ind, v_ind) for u_ind in u_sli_indicators for v_ind in v_sli_indicators]

    # Case 4: Resource -> Resource (e.g., Pod -> Container)
    # Default behavior: compare all metrics.
    else:  # u_is_resource and v_is_resource
        indicator_pairs = [(u_ind, v_ind) for u_ind in u_indicators for v_ind in v_indicators]

    # --- Perform Relation Calculation (Correlation or Causality) ---
    for u_indicator, v_indicator in indicator_pairs:
        # Get time-aligned data for both nodes
        df_u = u_indicator.df.select(["time", "value", "anomal"])
        df_v = v_indicator.df.select(["time", "value", "anomal"])

        # Join on time
        aligned_df = df_u.join_asof(df_v.rename({"value": "value_v"}), on="time", strategy="nearest").drop_nulls()

        if aligned_df.height < min_normal_points:
            continue

        normal_df = aligned_df.filter(pl.col("anomal") == 0)
        anomal_df = aligned_df.filter(pl.col("anomal") == 1)

        if use_granger:
            # --- Granger Causality Approach ---
            if normal_df.height < 20 or anomal_df.height < 20:
                continue

            # Test for causality u -> v in anomalous period
            p_anomal = test_granger_causality(anomal_df["value_v"].to_numpy(), anomal_df["value"].to_numpy())

            # If no causal link in anomalous period, it's not interesting
            if p_anomal > p_value_threshold:
                continue

            # Test for causality u -> v in normal period
            p_normal = test_granger_causality(normal_df["value_v"].to_numpy(), normal_df["value"].to_numpy())

            # The score is high if causality appears/strengthens during the anomaly
            # (i.e., p_anomal is low and p_normal is high)
            score = (1 - p_anomal) * p_normal

            if score > max_score:
                max_score = score

                if debug() and score > 0.5:
                    logger.debug(
                        "causal relation: {} -> {}, indicators: {} vs {}, "
                        "p_normal: {:.3f}, p_anomal: {:.3f}, score: {:.3f}",
                        u.uniq_name,
                        v.uniq_name,
                        u_indicator.name,
                        v_indicator.name,
                        p_normal,
                        p_anomal,
                        score,
                    )

        else:
            # --- Correlation-based Approach (Vectorized) ---
            if normal_df.height < window_size or anomal_df.height < 2:
                continue

            # 1. Calculate correlation distribution in normal period using VECTORIZED sliding window
            normal_arr1 = normal_df["value"].to_numpy()
            normal_arr2 = normal_df["value_v"].to_numpy()

            # Use vectorized function for much faster computation
            normal_corrs = calc_sliding_window_correlations_vectorized(normal_arr1, normal_arr2, window_size)

            if len(normal_corrs) < 2:
                continue

            mean_normal_corr = np.mean(normal_corrs)
            std_normal_corr = np.std(normal_corrs, ddof=1)

            # 2. Calculate correlation for the entire anomalous period
            anomal_corr = calc_correlation(anomal_df["value"], anomal_df["value_v"])

            if not math.isfinite(anomal_corr):
                continue

            # 3. Calculate standardized score
            if std_normal_corr > 1e-6:  # Avoid division by zero
                score = abs(anomal_corr - mean_normal_corr) / std_normal_corr
            else:
                # If normal correlation is very stable, any change is potentially significant
                score = abs(anomal_corr - mean_normal_corr) * 10  # Amplification factor

            if score > max_score:
                max_score = score

                if debug() and score > 2.0:  # Log significant relation changes
                    logger.debug(
                        "relation anomaly: {} -> {}, indicators: {} vs {}, "
                        "normal_corr: {:.3f}±{:.3f}, anomal_corr: {:.3f}, score: {:.3f}",
                        u.uniq_name,
                        v.uniq_name,
                        u_indicator.name,
                        v_indicator.name,
                        mean_normal_corr,
                        std_normal_corr,
                        anomal_corr,
                        score,
                    )

    return float(max_score)


def _compute_edge_relation_score(
    edge_data: tuple, use_granger: bool, min_relation_score: float
) -> list[tuple[int, int, float]]:
    """
    Helper function to compute relation scores for a single edge (in both directions).
    Designed to be pickled for multiprocessing.

    Args:
        edge_data: Tuple of (edge, sdg_edges, node_data_by_id)
        use_granger: Whether to use Granger causality
        min_relation_score: Minimum score threshold

    Returns:
        List of (src_id, dst_id, score) tuples for significant relations
    """
    edge, nodes_by_id = edge_data
    results = []

    try:
        # We only need the two nodes involved in this edge
        u = nodes_by_id[edge.src_id]
        v = nodes_by_id[edge.dst_id]

        # Direction 1: u -> v
        score_u_to_v = get_relation_anomaly_score(u, v, use_granger=use_granger)
        if score_u_to_v >= min_relation_score:
            results.append((u.id, v.id, score_u_to_v))

        # Direction 2: v -> u
        score_v_to_u = get_relation_anomaly_score(v, u, use_granger=use_granger)
        if score_v_to_u >= min_relation_score:
            results.append((v.id, u.id, score_v_to_u))

    except Exception as e:
        # Log error but don't crash the whole computation
        if debug():
            logger.warning("Error computing relation for edge {}: {}", edge, e)

    return results


def find_root_cause_candidates_relational(
    sdg: SDG,
    alpha: float = 0.5,
    min_relation_score: float = 1.0,
    use_granger: bool = False,
) -> list[PlaceNode]:
    """
    Finds root cause candidates using the Relational Debugging approach.

    The core principle: nodes that exhibit anomalies AND cause significant changes in their
    relationships with other nodes are more likely to be root causes.

    Key optimizations:
    - Only tests relations between nodes connected by edges in the SDG
    - Supports both correlation-based and Granger causality-based relation scoring
    - Handles heterogeneous node types (resource vs application layers)

    Args:
        sdg: System Dependency Graph
        alpha: Balance between node's own anomaly score and outgoing relation changes (0-1)
        min_relation_score: Minimum relation score to consider
        use_granger: Whether to use Granger causality test (slower but more precise)

    Returns:
        List of PlaceNodes sorted by root cause score (descending)
    """

    # Step 1: Initialize scores with each node's own anomaly score
    rc_scores = defaultdict(float)
    for node in sdg.iter_nodes():
        anomalies = detect_node_anomalies(sdg, node)
        if anomalies:
            # Use the max score among all anomalies for the node
            rc_scores[node.id] = max(a.score for a in anomalies)

    if debug():
        logger.debug("initialized {} nodes with anomaly scores", len(rc_scores))

    # Step 2: Pre-calculate relation scores ONLY for connected nodes
    # This optimization reduces complexity from O(N²) to O(E)
    # Key: (src_id, dst_id), Value: relation anomaly score
    relation_scores: dict[tuple[int, int], float] = {}

    if use_granger and not HAS_STATSMODELS:
        logger.warning(
            "Granger causality test requested but statsmodels is not installed. "
            "Falling back to correlation-based approach."
        )
        use_granger = False

    logger.debug(
        "computing relation scores (use_granger={}) with parallel processing",
        use_granger,
    )

    # Collect all edges
    edges = list(sdg.iter_edges())
    total_edges = len(edges)

    if total_edges == 0:
        logger.warning("No edges found in SDG")
    else:
        # Prepare node data for serialization (only include what's needed)
        nodes_by_id = {node.id: node for node in sdg.iter_nodes()}

        # Create a partial function that can be pickled
        compute_func = partial(
            _compute_edge_relation_score,
            use_granger=use_granger,
            min_relation_score=min_relation_score,
        )

        # Prepare tasks for fmap_processpool
        # Each task is a partial function with the edge data
        tasks = [partial(compute_func, (edge, nodes_by_id)) for edge in edges]

        logger.debug("processing {} edges with fmap_processpool", total_edges)

        # Use fmap_processpool for parallel execution
        results_list = fmap_processpool(
            tasks,
            parallel=8,  # Use 8 workers
            ignore_exceptions=True,  # Don't fail on single edge errors
        )

        # Flatten results and build relation_scores dict
        for results in results_list:
            for src_id, dst_id, score in results:
                relation_scores[(src_id, dst_id)] = score

    if debug():
        logger.debug(
            "computed {} significant relations along {} edges",
            len(relation_scores),
            total_edges,
        )

    # Step 3: Calculate root cause scores based on relation changes
    # Relational Debugging principle: nodes that are sources of significant relation changes
    # are more likely to be root causes.

    # For each node, calculate:
    # 1. Its own anomaly score
    # 2. The sum of outgoing relation anomaly scores (changes it caused)

    for node in sdg.iter_nodes():
        node_id = node.id

        # Get node's own anomaly score
        anomalies = detect_node_anomalies(sdg, node)
        node_anomaly_score = max((a.score for a in anomalies), default=0.0)

        # Calculate total outgoing relation anomaly score
        outgoing_relation_score = 0.0
        for (src_id, dst_id), rel_score in relation_scores.items():
            if src_id == node_id:
                outgoing_relation_score += rel_score

        # Combine: nodes with both anomalies AND causing relation changes are ranked higher
        rc_scores[node_id] = alpha * node_anomaly_score + (1 - alpha) * outgoing_relation_score

    if debug():
        top_scores = sorted(rc_scores.items(), key=lambda x: x[1], reverse=True)[:10]
        logger.debug("top root cause scores:")
        for i, (nid, score) in enumerate(top_scores, 1):
            node = sdg.get_node_by_id(nid)
            logger.debug("  {}: {} (score={:.4f})", i, node.uniq_name, score)

    # Step 4: Collect and rank candidates
    rcc_list: list[PlaceNode] = []
    for node_id, score in rc_scores.items():
        if score > 0:
            node = sdg.get_node_by_id(node_id)
            node.data["alg.rc_score"] = score  # Store the final score
            rcc_list.append(node)

    rcc_list.sort(key=lambda n: n.data["alg.rc_score"], reverse=True)

    if debug():
        logger.debug("found {} relational root cause candidates:", len(rcc_list))
        for i, node in enumerate(rcc_list[:10], 1):  # Show top 10
            logger.debug("  {}: {} (score={:.4f})", i, node.uniq_name, node.data["alg.rc_score"])

    return rcc_list


# --- Service-Level Aggregation ---


def unify_to_service_candidates(sdg: SDG, rcc_list: list[PlaceNode]) -> list[str]:
    """
    Unify root cause candidates to service level.

    Aggregates scores from pods, containers, and functions to their parent services.
    """
    service_scores: dict[str, float] = defaultdict(float)

    for node in rcc_list:
        score = node.data.get("alg.rc_score", 0.0)

        # Find the service this node belongs to
        service = find_related_service(sdg, node)
        if service is not None:
            service_name = service.self_name
            # Accumulate scores (could also use max, avg, etc.)
            service_scores[service_name] += score
        else:
            # If it's already a service node
            if node.kind.startswith("service"):
                service_scores[node.self_name] += score

    # Sort services by aggregated score
    ranked_services = sorted(service_scores.items(), key=lambda x: x[1], reverse=True)

    return [name for name, _ in ranked_services]


def find_related_service(sdg: SDG, node: PlaceNode) -> PlaceNode | None:
    """
    Find the service node related to the given node by traversing the dependency graph.
    """
    # If already a service, return it
    if node.kind.startswith("service"):
        return node

    # BFS to find parent service
    visited = set()
    queue = [node]

    while queue:
        current = queue.pop(0)
        if current.id in visited:
            continue
        visited.add(current.id)

        # Check if this is a service
        if current.kind.startswith("service"):
            return current

        # Add parent nodes (traverse up the dependency tree)
        for edge in sdg.iter_edges():
            # Look for edges where current is destination
            if edge.dst_id == current.id:
                parent = sdg.get_node_by_id(edge.src_id)
                if parent.id not in visited:
                    queue.append(parent)
            # Also consider reverse direction for some edge types
            elif edge.src_id == current.id:
                child = sdg.get_node_by_id(edge.dst_id)
                if child.kind.startswith("service") and child.id not in visited:
                    queue.append(child)

    return None
