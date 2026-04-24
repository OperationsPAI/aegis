"""Export abnormal nodes and edges to Parquet files for LLM tools.

This module exports:
1. abnormal_nodes.parquet - All nodes with non-default states
2. abnormal_edges.parquet - All edges connecting abnormal nodes
3. propagation_patterns.parquet - (src_node, edge, dst_node, src_state, dst_state) tuples
"""

import logging
from pathlib import Path
from typing import TYPE_CHECKING

import pandas as pd

from rcabench_platform.v3.internal.reasoning.models.graph import CallsEdgeData, DepKind, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.state import get_default_state

logger = logging.getLogger(__name__)

if TYPE_CHECKING:
    pass


# Default states that are considered "normal" (not abnormal)
DEFAULT_STATES = {
    "HEALTHY",
    "ACTIVE",
    "AVAILABLE",
    "READY",
    "UNKNOWN",
    "unknown",
}


def is_abnormal_state(state: str, node_kind: PlaceKind) -> bool:
    """Check if a state is considered abnormal for the given node kind.

    Args:
        state: The detected state string
        node_kind: The kind of node

    Returns:
        True if the state is abnormal, False otherwise
    """
    default = get_default_state(node_kind).value
    return state.upper() != default.upper() and state.upper() not in DEFAULT_STATES


def export_abnormal_nodes(
    graph: HyperGraph,
    node_states: dict[int, str],
    output_path: Path,
) -> pd.DataFrame:
    """Export all abnormal nodes to a parquet file.

    Args:
        graph: The dependency graph
        node_states: Mapping of node_id -> detected state
        output_path: Path to save the parquet file

    Returns:
        DataFrame with abnormal nodes
    """
    records = []

    for node_id, state in node_states.items():
        node = graph.get_node_by_id(node_id)
        if node is None:
            continue

        if is_abnormal_state(state, node.kind):
            records.append(
                {
                    "node_id": node_id,
                    "node_kind": node.kind.value,
                    "node_name": node.self_name,
                    "uniq_name": node.uniq_name,
                    "state": state,
                }
            )

    df = pd.DataFrame(records)
    if not df.empty:
        df.to_parquet(output_path / "abnormal_nodes.parquet", index=False)
        logger.info(f"  ✓ Exported {len(df)} abnormal nodes to abnormal_nodes.parquet")
    else:
        logger.warning("  ⚠ No abnormal nodes found")

    return df


def export_abnormal_edges(
    graph: HyperGraph,
    node_states: dict[int, str],
    output_path: Path,
) -> pd.DataFrame:
    """Export edges connecting abnormal nodes to a parquet file.

    Args:
        graph: The dependency graph
        node_states: Mapping of node_id -> detected state
        output_path: Path to save the parquet file

    Returns:
        DataFrame with abnormal edges
    """
    # Collect abnormal node IDs
    abnormal_node_ids = set()
    for node_id, state in node_states.items():
        node = graph.get_node_by_id(node_id)
        if node is not None and is_abnormal_state(state, node.kind):
            abnormal_node_ids.add(node_id)

    records = []

    for edge in graph._edge_id_map.values():
        # Include edge if either source or target is abnormal
        if edge.src_id in abnormal_node_ids or edge.dst_id in abnormal_node_ids:
            src_node = graph.get_node_by_id(edge.src_id)
            dst_node = graph.get_node_by_id(edge.dst_id)

            if src_node is None or dst_node is None:
                continue

            if edge.src_id not in node_states:
                raise KeyError(f"node_states missing src_id={edge.src_id} for edge {edge.id}")
            if edge.dst_id not in node_states:
                raise KeyError(f"node_states missing dst_id={edge.dst_id} for edge {edge.id}")

            record: dict[str, str | int | float | None] = {
                "edge_id": edge.id,
                "edge_kind": edge.kind.value,
                "src_id": edge.src_id,
                "src_kind": src_node.kind.value,
                "src_name": src_node.self_name,
                "src_state": node_states[edge.src_id],
                "dst_id": edge.dst_id,
                "dst_kind": dst_node.kind.value,
                "dst_name": dst_node.self_name,
                "dst_state": node_states[edge.dst_id],
            }

            # Add calls edge statistics if available
            if edge.kind == DepKind.calls and isinstance(edge.data, CallsEdgeData):
                data = edge.data
                record.update(
                    {
                        "baseline_call_count": data.baseline_call_count,
                        "baseline_error_count": data.baseline_error_count,
                        "baseline_error_rate": data.baseline_error_rate,
                        "baseline_avg_latency": data.baseline_avg_latency,
                        "baseline_p99_latency": data.baseline_p99_latency,
                        "abnormal_call_count": data.abnormal_call_count,
                        "abnormal_error_count": data.abnormal_error_count,
                        "abnormal_error_rate": data.abnormal_error_rate,
                        "abnormal_avg_latency": data.abnormal_avg_latency,
                        "abnormal_p99_latency": data.abnormal_p99_latency,
                    }
                )

            records.append(record)

    df = pd.DataFrame(records)
    if not df.empty:
        df.to_parquet(output_path / "abnormal_edges.parquet", index=False)
        logger.info(f"  ✓ Exported {len(df)} edges connecting abnormal nodes to abnormal_edges.parquet")
    else:
        logger.warning("  ⚠ No edges connecting abnormal nodes found")

    return df


def export_propagation_patterns(
    graph: HyperGraph,
    node_states: dict[int, str],
    output_path: Path,
) -> pd.DataFrame:
    """Export propagation patterns (abnormal src -> edge -> dst with states) to parquet.

    This is the main file for LLM to read for rule generation.
    Each row represents a potential fault propagation pattern:
    (src_kind, src_state, edge_kind, dst_kind, dst_state, edge_statistics)

    Args:
        graph: The dependency graph
        node_states: Mapping of node_id -> detected state
        output_path: Path to save the parquet file

    Returns:
        DataFrame with propagation patterns
    """
    records = []

    for edge in graph._edge_id_map.values():
        src_node = graph.get_node_by_id(edge.src_id)
        dst_node = graph.get_node_by_id(edge.dst_id)

        if src_node is None or dst_node is None:
            continue

        if edge.src_id not in node_states:
            raise KeyError(f"node_states missing src_id={edge.src_id} for edge {edge.id}")
        if edge.dst_id not in node_states:
            raise KeyError(f"node_states missing dst_id={edge.dst_id} for edge {edge.id}")

        src_state = node_states[edge.src_id]
        dst_state = node_states[edge.dst_id]

        # Only include if at least one node is abnormal
        src_abnormal = is_abnormal_state(src_state, src_node.kind)
        dst_abnormal = is_abnormal_state(dst_state, dst_node.kind)

        if not (src_abnormal or dst_abnormal):
            continue

        record = {
            # Source node info
            "src_kind": src_node.kind.value,
            "src_name": src_node.self_name,
            "src_state": src_state,
            "src_is_abnormal": src_abnormal,
            # Edge info
            "edge_kind": edge.kind.value,
            # Destination node info
            "dst_kind": dst_node.kind.value,
            "dst_name": dst_node.self_name,
            "dst_state": dst_state,
            "dst_is_abnormal": dst_abnormal,
            # Pattern type
            "pattern_type": _classify_pattern(src_abnormal, dst_abnormal),
        }

        # Add calls edge statistics if available
        if edge.kind == DepKind.calls and isinstance(edge.data, CallsEdgeData):
            data = edge.data
            record.update(
                {
                    "baseline_call_count": data.baseline_call_count,
                    "baseline_error_rate": data.baseline_error_rate,
                    "baseline_avg_latency": data.baseline_avg_latency,
                    "baseline_p99_latency": data.baseline_p99_latency,
                    "abnormal_call_count": data.abnormal_call_count,
                    "abnormal_error_rate": data.abnormal_error_rate,
                    "abnormal_avg_latency": data.abnormal_avg_latency,
                    "abnormal_p99_latency": data.abnormal_p99_latency,
                    "latency_increase_ratio": (
                        data.abnormal_avg_latency / data.baseline_avg_latency if data.baseline_avg_latency > 0 else 0.0
                    ),
                    "error_rate_delta": data.abnormal_error_rate - data.baseline_error_rate,
                }
            )

        records.append(record)

    df = pd.DataFrame(records)
    if not df.empty:
        # Sort by pattern_type to group forward/backward propagations
        df = df.sort_values(["pattern_type", "src_kind", "edge_kind", "dst_kind"])
        df.to_parquet(output_path / "propagation_patterns.parquet", index=False)
        logger.info(f"  ✓ Exported {len(df)} propagation patterns to propagation_patterns.parquet")

        # Log summary
        pattern_summary = df.groupby("pattern_type").size()
        for ptype, count in pattern_summary.items():
            logger.info(f"    - {ptype}: {count} patterns")
    else:
        logger.warning("  ⚠ No propagation patterns found")

    return df


def _classify_pattern(src_abnormal: bool, dst_abnormal: bool) -> str:
    """Classify the propagation pattern type.

    Args:
        src_abnormal: Whether source node is abnormal
        dst_abnormal: Whether destination node is abnormal

    Returns:
        Pattern type string
    """
    if src_abnormal and dst_abnormal:
        return "both_abnormal"  # Likely propagation chain
    elif src_abnormal:
        return "forward_propagation"  # Fault propagating from src to dst
    else:
        return "backward_propagation"  # Looking for root cause upstream


def export_all_patterns(
    graph: HyperGraph,
    node_states: dict[int, str],
    output_dir: Path,
) -> dict[str, pd.DataFrame]:
    """Export all abnormal patterns to parquet files.

    Creates three files:
    - abnormal_nodes.parquet
    - abnormal_edges.parquet
    - propagation_patterns.parquet

    Args:
        graph: The dependency graph
        node_states: Mapping of node_id -> detected state
        output_dir: Directory to save the parquet files

    Returns:
        Dictionary mapping filename to DataFrame
    """
    output_dir.mkdir(parents=True, exist_ok=True)

    logger.info("\n[3.5/7] Exporting abnormal patterns for LLM analysis...")

    results = {}
    results["abnormal_nodes"] = export_abnormal_nodes(graph, node_states, output_dir)
    results["abnormal_edges"] = export_abnormal_edges(graph, node_states, output_dir)
    results["propagation_patterns"] = export_propagation_patterns(graph, node_states, output_dir)

    return results
