"""Result and causal graph file-writing helpers for reasoning runs."""

from __future__ import annotations

import logging
from pathlib import Path
from typing import Any

import polars as pl

from rcabench_platform.v3.internal.reasoning.alarm_evidence import (
    _alarm_evidence_for_node,
    _build_alarm_accounting,
    _build_leg_alarm_accounting,
    _evidence_confidence_for_strength,
    _split_default_and_weak_paths,
)
from rcabench_platform.v3.internal.reasoning.export.causal_graph import (
    _causal_graph_with_export_metadata,
    _sync_injection_states_from_root_causes,
    propagation_result_to_causal_graph,
)
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph
from rcabench_platform.v3.internal.reasoning.models.propagation import LabelT, PropagationResult
from rcabench_platform.v3.sdk.evaluation.causal_graph import CausalGraph
from rcabench_platform.v3.sdk.utils.serde import save_json

logger = logging.getLogger(__name__)


def load_span_to_service_mapping(data_dir: Path) -> dict[str, list[str]]:
    """Load span_name -> service_name mapping directly from parquet files.

    This provides the ground truth mapping of which services contain which spans,
    avoiding ambiguity when the same span_name appears in multiple services.

    Returns:
        Dict mapping span_name to list of service_names that contain it.
    """
    mapping: dict[str, list[str]] = {}

    abnormal_path = data_dir / "abnormal_traces.parquet"
    normal_path = data_dir / "normal_traces.parquet"

    dfs = []
    if abnormal_path.exists():
        dfs.append(pl.read_parquet(abnormal_path))
    if normal_path.exists():
        dfs.append(pl.read_parquet(normal_path))

    if not dfs:
        return mapping

    all_traces = pl.concat(dfs)

    # Group by span_name and collect unique service_names
    span_services = all_traces.group_by("span_name").agg(pl.col("service_name").unique().alias("services"))

    for row in span_services.iter_rows(named=True):
        span_name = row["span_name"]
        services = list(row["services"]) if row["services"] else []
        mapping[span_name] = services

    return mapping


def _result_with_paths(result: PropagationResult, paths: list[Any]) -> PropagationResult:
    return PropagationResult(
        injection_node_ids=list(result.injection_node_ids),
        injection_states=list(result.injection_states),
        paths=list(paths),
        visited_nodes=set(result.visited_nodes),
        max_hops_reached=result.max_hops_reached,
        subgraph_edges=list(result.subgraph_edges),
        warnings=list(result.warnings),
        label=result.label,
        label_reason=result.label_reason,
        decomposition=result.decomposition,
        rejected_paths=list(result.rejected_paths),
        injection_state_reasons=list(result.injection_state_reasons),
        injection_state_details=list(result.injection_state_details),
    )


def _build_visualization_paths(
    result: PropagationResult,
    graph: HyperGraph,
    alarm_nodes: set[int],
    evidence_by_name: dict[str, Any] | None = None,
) -> list[dict[str, Any]]:
    """Build path data with full node info for visualization."""
    evidence_by_name = evidence_by_name or {}
    viz_paths = []
    for path in result.paths:
        path_nodes = []
        for j, node_id in enumerate(path.nodes):
            node = graph.get_node_by_id(node_id)

            states_at_node = path.states[j] if j < len(path.states) else []
            if isinstance(states_at_node, list):
                state_str = ", ".join(states_at_node) if states_at_node else "UNKNOWN"
            else:
                state_str = str(states_at_node) if states_at_node else "UNKNOWN"

            state_start_time = None
            if path.state_start_times and j < len(path.state_start_times):
                state_start_time = path.state_start_times[j]

            # Edge, rule, and delay are for the hop FROM this node TO next node
            # So they exist for nodes 0 to n-2 (not for the last node)
            edge_kind = None
            rule_id = None
            propagation_delay = None
            if j < len(path.nodes) - 1:
                if path.edges and j < len(path.edges):
                    edge_kind = path.edges[j]
                if path.rules and j < len(path.rules):
                    rule_id = path.rules[j]
                if path.propagation_delays and j < len(path.propagation_delays):
                    propagation_delay = path.propagation_delays[j]

            path_nodes.append(
                {
                    "node_id": node_id,
                    "kind": node.kind.value if node else "unknown",
                    "name": node.self_name if node else f"Node_{node_id}",
                    "uniq_name": node.uniq_name if node else f"unknown|Node_{node_id}",
                    "state": state_str,
                    "state_start_time": state_start_time,
                    "is_alarm": node_id in alarm_nodes,
                    "edge_kind": edge_kind,
                    "rule_id": rule_id,
                    "propagation_delay": propagation_delay,
                }
            )

        terminal_strength = "unknown"
        if path.nodes and path.nodes[-1] in alarm_nodes:
            terminal_strength = _alarm_evidence_for_node(path.nodes[-1], graph, evidence_by_name)["issue_strength"]
        evidence_confidence = _evidence_confidence_for_strength(terminal_strength)
        viz_paths.append(
            {
                "confidence": min(path.confidence, evidence_confidence),
                "rule_admission_confidence": path.confidence,
                "evidence_confidence": evidence_confidence,
                "alarm_coverage_confidence": evidence_confidence,
                "final_confidence": min(path.confidence, evidence_confidence),
                "nodes": path_nodes,
            }
        )
    return viz_paths


def _build_result(
    case_name: str,
    status: str,
    graph: HyperGraph | None = None,
    **kwargs: Any,
) -> dict[str, Any]:
    """Build a result dictionary."""
    ret: dict[str, Any] = {"case": case_name, "status": status, "paths": 0}
    ret.update(kwargs)
    if graph is not None:
        ret["graph"] = graph
    return ret


def _process_successful_propagation(
    case_name: str,
    result: PropagationResult,
    graph: HyperGraph,
    injection_nodes: list[str],
    alarm_nodes: set[int],
    return_graph: bool,
    data_dir: Path,
    resolution_info: dict[str, Any] | None = None,
    label: LabelT | None = None,
    label_reason: str = "",
    alarm_evidence_by_name: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Process case with successful propagation paths."""
    primary_injection_node = injection_nodes[0] if injection_nodes else ""

    # Load span-to-service mapping from parquet files for accurate service assignment
    span_to_service_mapping = load_span_to_service_mapping(data_dir)
    alarm_evidence_by_name = alarm_evidence_by_name or {}
    default_paths, weak_paths = _split_default_and_weak_paths(result, graph, alarm_nodes, alarm_evidence_by_name)
    graph_result = _result_with_paths(result, default_paths)
    root_fallback_states: dict[str, str | list[str] | set[str] | frozenset[str]] = {}
    if resolution_info:
        for candidate in resolution_info.get("root_candidates", []) or []:
            if not isinstance(candidate, dict):
                continue
            node = candidate.get("node")
            expected_state = candidate.get("expected_state")
            if node and expected_state:
                root_fallback_states[str(node)] = str(expected_state)

    causal_graph = propagation_result_to_causal_graph(
        result=graph_result,
        graph=graph,
        injection_node_name=primary_injection_node,
        alarm_node_ids=alarm_nodes,
        span_to_service_mapping=span_to_service_mapping,
        injection_node_names=injection_nodes,
        root_fallback_states=root_fallback_states,
        root_candidates=resolution_info.get("root_candidates") if resolution_info else None,
    )

    alarm_accounting = _build_alarm_accounting(result, graph, alarm_nodes, alarm_evidence_by_name)
    leg_accounting = _build_leg_alarm_accounting(
        result,
        graph,
        alarm_nodes,
        alarm_evidence_by_name,
        resolution_info,
    )
    if leg_accounting:
        alarm_accounting["leg_alarm_accounting"] = leg_accounting
    causal_graph = _causal_graph_with_export_metadata(
        causal_graph,
        case_name=case_name,
        result=result,
        alarm_accounting=alarm_accounting,
        resolution_info=resolution_info,
    )
    _sync_injection_states_from_root_causes(result, causal_graph)
    viz_paths = _build_visualization_paths(graph_result, graph, alarm_nodes, alarm_evidence_by_name)
    weak_viz_paths = _build_visualization_paths(
        _result_with_paths(result, weak_paths),
        graph,
        alarm_nodes,
        alarm_evidence_by_name,
    )

    _save_case_result(
        data_dir=data_dir,
        case_name=case_name,
        status="success",
        causal_graph=causal_graph,
        injection_nodes=injection_nodes,
        alarm_nodes=alarm_nodes,
        result=result,
        viz_paths=viz_paths,
        weak_paths=weak_viz_paths,
        alarm_accounting=alarm_accounting,
        resolution_info=resolution_info,
        label=label,
        label_reason=label_reason,
    )

    ret: dict[str, Any] = {
        "case": case_name,
        "status": "success",
        "paths": len(result.paths),
        "propagation_result": result,
    }
    if label is not None:
        ret["label"] = label
        ret["label_reason"] = label_reason
    if resolution_info:
        ret["resolution_info"] = resolution_info
    if return_graph:
        ret["graph"] = graph
    return ret


def _clean_previous_results(data_dir: Path) -> None:
    files_to_clean = [
        data_dir / "result.json",
        data_dir / "causal_graph.json",
        data_dir / "no_alarms.marker",
        data_dir / "no_paths.marker",
        data_dir / "label.txt",
    ]
    for file in files_to_clean:
        if file.exists():
            file.unlink()


def _save_case_result(
    data_dir: Path,
    case_name: str,
    status: str,
    causal_graph: CausalGraph | None = None,
    injection_nodes: list[str] | None = None,
    alarm_nodes: set[int] | None = None,
    result: PropagationResult | None = None,
    viz_paths: list[dict[str, Any]] | None = None,
    weak_paths: list[dict[str, Any]] | None = None,
    alarm_accounting: dict[str, Any] | None = None,
    resolution_info: dict[str, Any] | None = None,
    label: LabelT | None = None,
    label_reason: str = "",
) -> None:
    _clean_previous_results(data_dir)

    if status == "success" and causal_graph and result and injection_nodes is not None and alarm_nodes is not None:
        graph_data = causal_graph.model_dump(exclude_none=True)
        save_json(graph_data, path=data_dir / "causal_graph.json")

        result_data: dict[str, Any] = {
            "case_name": case_name,
            "injection_nodes": injection_nodes,
            "alarm_nodes_scope": "candidate_alarm_nodes",
            "alarm_nodes": list(alarm_nodes),
            "propagation_result": result.to_dict(),
            "visualization_paths": viz_paths or [],
        }
        if weak_paths:
            result_data["weak_paths"] = weak_paths
        if alarm_accounting:
            result_data.update(alarm_accounting)
            terminal_components = {node.component for node in causal_graph.path_terminal_alarm_nodes}
            terminal_details = [
                detail
                for detail in alarm_accounting.get("explained_alarm_nodes", [])
                if detail.get("component") in terminal_components
            ]
            result_data["path_terminal_alarm_nodes"] = terminal_details
            result_data["path_terminal_alarm_node_ids"] = sorted(
                detail["node_id"] for detail in terminal_details if "node_id" in detail
            )
            result_data["path_terminal_alarm_count"] = len(terminal_details)
        if resolution_info:
            result_data["resolution_info"] = resolution_info
        if label is not None:
            result_data["label"] = label
            result_data["label_reason"] = label_reason
        save_json(result_data, path=data_dir / "result.json")
        logger.info(f"[{case_name}] Saved causal_graph.json and result.json")

    elif status == "no_alarms":
        (data_dir / "no_alarms.marker").touch()
        if result is not None:
            result_data = {
                "case_name": case_name,
                "propagation_result": result.to_dict(),
            }
            if alarm_accounting:
                result_data.update(alarm_accounting)
            if label is not None:
                result_data["label"] = label
                result_data["label_reason"] = label_reason
            save_json(result_data, path=data_dir / "result.json")
        logger.info(f"[{case_name}] No alarm nodes found, created marker")

    elif status == "no_paths":
        (data_dir / "no_paths.marker").touch()
        if result is not None:
            result_data = {
                "case_name": case_name,
                "propagation_result": result.to_dict(),
            }
            if alarm_accounting:
                result_data.update(alarm_accounting)
            if label is not None:
                result_data["label"] = label
                result_data["label_reason"] = label_reason
            save_json(result_data, path=data_dir / "result.json")
        logger.info(f"[{case_name}] No propagation paths found, created marker")

    if label is not None:
        (data_dir / "label.txt").write_text(label + "\n", encoding="utf-8")
