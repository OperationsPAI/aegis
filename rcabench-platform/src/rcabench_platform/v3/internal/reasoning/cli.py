import json
import logging
import re
import time
from datetime import datetime
from functools import partial
from pathlib import Path
from typing import Any

import polars as pl
import typer
from tqdm import tqdm

from rcabench_platform.v3.internal.reasoning._util import setup_logging
from rcabench_platform.v3.internal.reasoning.algorithms.gates import (
    INJECT_TIME_TOLERANCE_SECONDS,
    manifest_aware_gates,
)
from rcabench_platform.v3.internal.reasoning.algorithms.label_classifier import classify
from rcabench_platform.v3.internal.reasoning.algorithms.propagator import FaultPropagator
from rcabench_platform.v3.internal.reasoning.algorithms.starting_point_resolver import StartingPointResolver
from rcabench_platform.v3.internal.reasoning.config.slo_surface import SLOSurface
from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.inferred_edges import enrich_with_inferred_edges
from rcabench_platform.v3.internal.reasoning.ir.adapters.log_dependency import dispatch_log_adapters
from rcabench_platform.v3.internal.reasoning.ir.adapters.trace_db_binding import (
    dispatch_trace_db_binding_adapters,
)
from rcabench_platform.v3.internal.reasoning.ir.pipeline import run_reasoning_ir
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import ParquetDataLoader
from rcabench_platform.v3.internal.reasoning.loaders.utils import fmap_processpool
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
from rcabench_platform.v3.internal.reasoning.manifests.extractors import (
    extract_feature_samples,
)
from rcabench_platform.v3.internal.reasoning.manifests.registry import (
    ManifestRegistry,
    get_default_registry,
    set_default_registry,
)
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import InjectionNodeResolver
from rcabench_platform.v3.internal.reasoning.models.propagation import (
    FaultDecomposition,
    LabelT,
    LocalEffect,
    MechanismPath,
    PropagationResult,
    SLOImpact,
)
from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules
from rcabench_platform.v3.sdk.evaluation.causal_graph import CausalEdge, CausalGraph, CausalNode
from rcabench_platform.v3.sdk.utils.serde import save_json

logger = logging.getLogger(__name__)
app = typer.Typer(name="reason", help="Fault propagation reasoning engine CLI")


# Default manifest directory: package-relative ``manifests/fault_types/``.
# Phase 1 ships zero manifests (apart from the example referenced in tests),
# so the default is "registry empty → fall back to generic rules everywhere".
_DEFAULT_MANIFEST_DIR = Path(__file__).resolve().parent / "manifests" / "fault_types"


def _init_manifest_registry(manifest_dir: str | None) -> None:
    """Build and install the process-wide manifest registry.

    Phase 1 keeps the generic-rule path as the default: an empty registry
    or a missing directory both result in ``registry.get(name) is None``
    for every fault type, which is the documented "fall back" signal.
    """
    target = Path(manifest_dir) if manifest_dir else _DEFAULT_MANIFEST_DIR
    if not target.exists():
        logger.info(
            "manifest dir %s does not exist; using empty registry (generic rules everywhere)",
            target,
        )
        set_default_registry(ManifestRegistry({}))
        return
    registry = ManifestRegistry.from_directory(target, strict=True)
    logger.info(
        "loaded %d manifest(s) from %s: %s",
        len(registry),
        target,
        ", ".join(registry.names()) or "(none)",
    )
    set_default_registry(registry)


# =============================================================================
# Span to Service Mapping from Parquet
# =============================================================================


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


_UNKNOWN_STATE = "unknown"
_WEAK_ALARM_CONFIDENCE_CAP = 0.65
_NO_ISSUE_ALARM_CONFIDENCE_CAP = 0.45
_UNKNOWN_ALARM_CONFIDENCE_CAP = 0.80


def _parse_issues_payload(issues_raw: Any) -> dict[str, Any]:
    if issues_raw is None:
        return {}
    if isinstance(issues_raw, dict):
        return issues_raw
    if not isinstance(issues_raw, str):
        return {}
    payload = issues_raw.strip()
    if payload in {"", "{}", "null", "None"}:
        return {}
    try:
        parsed = json.loads(payload)
    except (TypeError, json.JSONDecodeError):
        return {}
    return parsed if isinstance(parsed, dict) else {}


def _safe_float(value: Any, default: float = 0.0) -> float:
    try:
        if value is None:
            return default
        return float(value)
    except (TypeError, ValueError):
        return default


def _ratio(after: float, before: float) -> float:
    if before <= 1e-9:
        return 0.0
    return after / before


def _normalize_conclusion_span_name(span_name: str) -> str:
    """Map conclusion.parquet span labels to graph span self_name when possible."""
    raw = span_name.strip()
    match = re.match(r"^HTTP\s+([A-Z]+)\s+https?://([^/:?\s]+)(?::\d+)?([^\s?]*)", raw)
    if match:
        method, host, path = match.groups()
        return f"{host}::{method} {path or '/'}"
    return raw


def _classify_conclusion_alarm(row: dict[str, Any]) -> dict[str, Any]:
    issues = _parse_issues_payload(row.get("Issues"))
    normal_success = _safe_float(row.get("NormalSuccRate"), 1.0)
    abnormal_success = _safe_float(row.get("AbnormalSuccRate"), normal_success)
    normal_avg = _safe_float(row.get("NormalAvgDuration"))
    abnormal_avg = _safe_float(row.get("AbnormalAvgDuration"))
    normal_p99 = _safe_float(row.get("NormalP99"))
    abnormal_p99 = _safe_float(row.get("AbnormalP99"))

    success_drop = max(0.0, normal_success - abnormal_success)
    avg_ratio = _ratio(abnormal_avg, normal_avg)
    p99_ratio = _ratio(abnormal_p99, normal_p99)
    avg_abs_change = max(0.0, abnormal_avg - normal_avg)
    p99_abs_change = max(0.0, abnormal_p99 - normal_p99)

    if issues:
        strength = "strong"
        reason = "conclusion_issues"
    elif success_drop >= 0.10:
        strength = "strong"
        reason = "success_rate_drop"
    elif (avg_ratio >= 2.0 and avg_abs_change >= 1.0) or (p99_ratio >= 5.0 and p99_abs_change >= 3.0):
        strength = "strong"
        reason = "material_latency_anomaly"
    elif avg_ratio >= 1.5 or p99_ratio >= 2.0 or avg_abs_change >= 0.5 or p99_abs_change >= 1.0:
        strength = "weak"
        reason = "weak_latency_signal"
    else:
        strength = "none"
        reason = "no_material_conclusion_signal"

    return {
        "issue_strength": strength,
        "issue_strength_reason": reason,
        "has_issues": bool(issues),
        "issues": issues,
        "normal_success_rate": normal_success,
        "abnormal_success_rate": abnormal_success,
        "success_rate_drop": success_drop,
        "normal_avg_duration": normal_avg,
        "abnormal_avg_duration": abnormal_avg,
        "avg_duration_ratio": avg_ratio,
        "normal_p99": normal_p99,
        "abnormal_p99": abnormal_p99,
        "p99_ratio": p99_ratio,
    }


def _load_alarm_evidence_index(loader: ParquetDataLoader) -> dict[str, dict[str, Any]]:
    try:
        conclusion_df = loader.load_conclusion()
    except (AttributeError, FileNotFoundError):
        return {}

    evidence_by_name: dict[str, dict[str, Any]] = {}
    for row in conclusion_df.iter_rows(named=True):
        raw_name = str(row.get("SpanName") or "")
        if not raw_name:
            continue
        evidence = _classify_conclusion_alarm(row)
        evidence["conclusion_span_name"] = raw_name
        evidence_by_name[raw_name] = evidence
        evidence_by_name[_normalize_conclusion_span_name(raw_name)] = evidence
    return evidence_by_name


def _span_self_name_from_component(component: str) -> str:
    return component.split("|", 1)[1] if component.startswith("span|") else component


def _alarm_evidence_for_node(
    node_id: int,
    graph: HyperGraph,
    evidence_by_name: dict[str, dict[str, Any]],
) -> dict[str, Any]:
    node = graph.get_node_by_id(node_id)
    component = node.uniq_name
    span_self_name = _span_self_name_from_component(component)
    evidence = evidence_by_name.get(span_self_name)
    if evidence:
        return dict(evidence)
    return {
        "issue_strength": "unknown",
        "issue_strength_reason": "conclusion_row_unavailable",
        "has_issues": False,
    }


def _alarm_detail(
    node_id: int,
    graph: HyperGraph,
    evidence_by_name: dict[str, dict[str, Any]],
    *,
    reason: str | None = None,
    path_status: str | None = None,
) -> dict[str, Any]:
    node = graph.get_node_by_id(node_id)
    evidence = _alarm_evidence_for_node(node_id, graph, evidence_by_name)
    out: dict[str, Any] = {
        "node_id": node_id,
        "component": node.uniq_name,
        "issue_strength": evidence["issue_strength"],
        "issue_strength_reason": evidence["issue_strength_reason"],
    }
    if reason is not None:
        out["reason"] = reason
    if path_status is not None:
        out["path_status"] = path_status
    for key in (
        "has_issues",
        "normal_success_rate",
        "abnormal_success_rate",
        "success_rate_drop",
        "normal_avg_duration",
        "abnormal_avg_duration",
        "avg_duration_ratio",
        "normal_p99",
        "abnormal_p99",
        "p99_ratio",
        "conclusion_span_name",
    ):
        if key in evidence:
            out[key] = evidence[key]
    return out


def _path_terminal_alarm_ids(result: PropagationResult, alarm_nodes: set[int]) -> set[int]:
    return {path.nodes[-1] for path in result.paths if path.nodes and path.nodes[-1] in alarm_nodes}


def _confidence_cap_for_strength(strength: str) -> float | None:
    if strength == "weak":
        return _WEAK_ALARM_CONFIDENCE_CAP
    if strength == "none":
        return _NO_ISSUE_ALARM_CONFIDENCE_CAP
    if strength == "unknown":
        return _UNKNOWN_ALARM_CONFIDENCE_CAP
    return None


def _apply_terminal_alarm_confidence_caps(
    result: PropagationResult,
    graph: HyperGraph,
    alarm_nodes: set[int],
    evidence_by_name: dict[str, dict[str, Any]],
) -> None:
    for path in result.paths:
        if not path.nodes or path.nodes[-1] not in alarm_nodes:
            continue
        strength = _alarm_evidence_for_node(path.nodes[-1], graph, evidence_by_name)["issue_strength"]
        cap = _confidence_cap_for_strength(strength)
        if cap is not None and path.confidence > cap:
            path.confidence = cap


def _split_default_and_weak_paths(
    result: PropagationResult,
    graph: HyperGraph,
    alarm_nodes: set[int],
    evidence_by_name: dict[str, dict[str, Any]],
) -> tuple[list[Any], list[Any]]:
    default_paths = []
    weak_paths = []
    for path in result.paths:
        if path.nodes and path.nodes[-1] in alarm_nodes:
            strength = _alarm_evidence_for_node(path.nodes[-1], graph, evidence_by_name)["issue_strength"]
            if strength in {"weak", "none"}:
                weak_paths.append(path)
                continue
        default_paths.append(path)

    # Avoid producing an empty causal graph for datasets where conclusion rows
    # are unavailable or all alarm evidence is weak; weak_paths still makes the
    # isolation explicit in result.json.
    if not default_paths and weak_paths:
        return weak_paths, []
    return default_paths, weak_paths


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
    )


def _build_alarm_accounting(
    result: PropagationResult,
    graph: HyperGraph,
    alarm_nodes: set[int],
    evidence_by_name: dict[str, dict[str, Any]],
) -> dict[str, Any]:
    explained_ids = _path_terminal_alarm_ids(result, alarm_nodes)
    unexplained_ids = set(alarm_nodes) - explained_ids
    candidate_details = [_alarm_detail(nid, graph, evidence_by_name) for nid in sorted(alarm_nodes)]
    explained_details = [
        _alarm_detail(nid, graph, evidence_by_name, reason="path_terminal", path_status="explained")
        for nid in sorted(explained_ids)
    ]
    unexplained_details = []
    for nid in sorted(unexplained_ids):
        strength = _alarm_evidence_for_node(nid, graph, evidence_by_name)["issue_strength"]
        path_status = "strong_unexplained" if strength == "strong" else "unexplained"
        unexplained_details.append(
            _alarm_detail(nid, graph, evidence_by_name, reason="no_path_found", path_status=path_status)
        )

    candidate_strong_count = sum(1 for detail in candidate_details if detail["issue_strength"] == "strong")
    explained_strong_count = sum(1 for detail in explained_details if detail["issue_strength"] == "strong")
    strong_alarm_coverage = (
        1.0 if candidate_strong_count == 0 else explained_strong_count / candidate_strong_count
    )

    return {
        "candidate_alarm_nodes": candidate_details,
        "explained_alarm_nodes": explained_details,
        "unexplained_alarm_nodes": unexplained_details,
        "path_terminal_alarm_nodes": explained_details,
        "candidate_alarm_node_ids": sorted(alarm_nodes),
        "explained_alarm_node_ids": sorted(explained_ids),
        "unexplained_alarm_node_ids": sorted(unexplained_ids),
        "strong_alarm_coverage": strong_alarm_coverage,
        "candidate_strong_alarm_count": candidate_strong_count,
        "explained_strong_alarm_count": explained_strong_count,
        "unexplained_strong_alarm_count": candidate_strong_count - explained_strong_count,
    }


def _resolve_root_causal_node(
    component: str,
    graph_nodes: list[CausalNode],
    fallback_states: frozenset[str],
) -> CausalNode:
    matching_nodes = [node for node in graph_nodes if node.component == component]
    stateful_nodes = [
        node
        for node in matching_nodes
        if node.state and node.state != frozenset({_UNKNOWN_STATE}) and node.state != frozenset({"UNKNOWN"})
    ]
    if stateful_nodes:
        unioned_state: set[str] = set()
        timestamps: list[int] = []
        for node in stateful_nodes:
            unioned_state.update(s for s in node.state if s.lower() != _UNKNOWN_STATE)
            if node.timestamp is not None:
                timestamps.append(node.timestamp)
        return CausalNode(
            component=component,
            timestamp=min(timestamps) if timestamps else None,
            state=frozenset(unioned_state) if unioned_state else fallback_states,
        )

    concrete_fallback = frozenset(s for s in fallback_states if s.lower() != _UNKNOWN_STATE)
    if concrete_fallback:
        return CausalNode(component=component, state=concrete_fallback)

    reason = "no_stateful_graph_node" if matching_nodes else "root_component_not_in_causal_graph"
    return CausalNode(
        component=component,
        state=frozenset({_UNKNOWN_STATE}),
        state_resolution_reason=reason,
    )


# =============================================================================
# Conversion: PropagationResult -> CausalGraph
# =============================================================================


def propagation_result_to_causal_graph(
    result: PropagationResult,
    graph: HyperGraph,
    injection_node_name: str,
    alarm_node_ids: set[int],
    span_to_service_mapping: dict[str, list[str]] | None = None,
) -> CausalGraph:
    """Convert PropagationResult to CausalGraph format.

    This function generates both component-level and service-level edges.
    For service-level edges, it uses the path context and parquet-based
    span-to-service mapping to correctly assign spans to services when
    the same span_name belongs to multiple services.

    Args:
        result: The propagation result
        graph: The HyperGraph
        injection_node_name: Name of the injection node
        alarm_node_ids: Set of alarm node IDs
        span_to_service_mapping: Optional mapping from span_name to list of services
            (loaded from parquet files). If provided, this is used as ground truth.
    """
    from collections import Counter

    nodes_dict: dict[str, CausalNode] = {}
    edges_set: set[tuple[str, str]] = set()
    alarm_nodes_dict: dict[str, CausalNode] = {}
    # Track all service assignments for each component across all paths
    component_service_votes: dict[str, Counter[str]] = {}

    for path in result.paths:
        # Track the current service context as we traverse the path
        current_service_context: str | None = None
        # Track service assignment for each node position in this path
        path_services: list[str | None] = []

        # First pass: determine service for each node in this path
        for _i, node_id in enumerate(path.nodes):
            node = graph.get_node_by_id(node_id)
            if node is None:
                path_services.append(None)
                continue

            node_service: str | None = None
            if node.kind == PlaceKind.span:
                node_service = _extract_service_from_span(
                    graph, node_id, current_service_context, span_to_service_mapping
                )
                if node_service:
                    current_service_context = node_service
            elif node.kind == PlaceKind.service:
                node_service = node.self_name
                current_service_context = node_service
            elif node.kind == PlaceKind.pod:
                node_service = _extract_service_from_pod(node.self_name)
                current_service_context = node_service

            path_services.append(node_service)

        # Second pass: create nodes and edges, collect service votes
        for i, node_id in enumerate(path.nodes):
            node = graph.get_node_by_id(node_id)
            if node is None:
                continue

            component = node.uniq_name
            states = frozenset(path.states[i]) if i < len(path.states) else frozenset()
            timestamp = path.state_start_times[i] if i < len(path.state_start_times) else None

            node_key = f"{component}|{','.join(sorted(states))}"
            if node_key not in nodes_dict:
                causal_node = CausalNode(
                    timestamp=timestamp,
                    component=component,
                    state=states,
                )
                nodes_dict[node_key] = causal_node

                if node_id in alarm_node_ids:
                    alarm_nodes_dict[node_key] = causal_node

            # Collect service votes for this component
            svc = path_services[i]
            if svc:
                if component not in component_service_votes:
                    component_service_votes[component] = Counter()
                component_service_votes[component][svc] += 1

            # Create component-level edge
            if i < len(path.nodes) - 1:
                next_node_id = path.nodes[i + 1]
                next_node = graph.get_node_by_id(next_node_id)
                if next_node:
                    edges_set.add((component, next_node.uniq_name))

    # Build component_to_service using majority vote
    component_to_service: dict[str, str] = {}
    for component, votes in component_service_votes.items():
        # Use the most common service assignment
        most_common = votes.most_common(1)
        if most_common:
            component_to_service[component] = most_common[0][0]

    edges = [CausalEdge(source=src, target=tgt) for src, tgt in edges_set]

    graph_nodes = list(nodes_dict.values())
    injection_node_obj = graph.get_node_by_name(injection_node_name)
    root_causes = []
    if injection_node_obj:
        injection_states = frozenset(result.injection_states) if result.injection_states else frozenset()
        root_causes.append(_resolve_root_causal_node(injection_node_name, graph_nodes, injection_states))

    return CausalGraph(
        nodes=graph_nodes,
        edges=edges,
        root_causes=root_causes,
        alarm_nodes=list(alarm_nodes_dict.values()),
        path_terminal_alarm_nodes=list(alarm_nodes_dict.values()),
        component_to_service=component_to_service,
    )


def _extract_service_from_span(
    graph: HyperGraph,
    span_node_id: int,
    service_context: str | None = None,
    span_to_service_mapping: dict[str, list[str]] | None = None,
) -> str | None:
    """Extract service name from a span node.

    Span nodes now use the format "{service_name}::{span_name}", so we can
    directly extract the service name from the node's self_name.

    Falls back to HyperGraph includes edges if the format doesn't match.

    Args:
        graph: The HyperGraph
        span_node_id: The span node ID
        service_context: The current service context (unused with new format)
        span_to_service_mapping: Optional mapping (unused with new format)
    """
    node = graph.get_node_by_id(span_node_id)
    if node is None:
        return None

    span_name = node.self_name

    # New format: "{service_name}::{span_name}"
    if "::" in span_name:
        service_name = span_name.split("::", 1)[0]
        return service_name

    # Fallback for old format or HTTP client spans
    # Try parquet-based mapping
    if span_to_service_mapping and span_name in span_to_service_mapping:
        candidate_services = span_to_service_mapping[span_name]
        if candidate_services:
            if len(candidate_services) == 1:
                return candidate_services[0]
            if service_context and service_context in candidate_services:
                return service_context
            return candidate_services[0]

    # Fallback: use HyperGraph includes edges
    candidate_services: list[str] = []
    for edge in graph.get_edges_by_kind(DepKind.includes):
        if edge.dst_id == span_node_id:
            src_node = graph.get_node_by_id(edge.src_id)
            if src_node and src_node.kind == PlaceKind.service:
                candidate_services.append(src_node.self_name)

    if candidate_services:
        if len(candidate_services) == 1:
            return candidate_services[0]
        if service_context and service_context in candidate_services:
            return service_context
        return candidate_services[0]

    # For HTTP client spans without includes edges
    if span_name.startswith("HTTP "):
        # Try to extract service from URL host (e.g., "HTTP POST http://ts-ui-dashboard:8080/...")
        url_match = re.search(r"https?://([a-zA-Z0-9_-]+):", span_name)
        if url_match:
            return url_match.group(1)

        # Fallback: find the called span and use its service
        for edge in graph.get_edges_by_kind(DepKind.calls):
            if edge.src_id == span_node_id:
                called_service = _extract_service_from_span(
                    graph, edge.dst_id, service_context, span_to_service_mapping
                )
                if called_service:
                    return called_service

    return None


def _extract_service_from_pod(pod_name: str) -> str:
    """Extract service name from pod name."""
    parts = pod_name.rsplit("-", 2)
    if len(parts) >= 3:
        return "-".join(parts[:-2])
    return pod_name


def _build_visualization_paths(
    result: PropagationResult,
    graph: HyperGraph,
    alarm_nodes: set[int],
) -> list[dict[str, Any]]:
    """Build path data with full node info for visualization."""
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

        viz_paths.append(
            {
                "confidence": path.confidence,
                "nodes": path_nodes,
            }
        )
    return viz_paths


# =============================================================================
# Single Case Processing
# =============================================================================


def _earliest_abnormal_seconds(abnormal_traces: pl.DataFrame) -> int:
    """Earliest abnormal-trace timestamp normalized to unix seconds.

    Mirrors ``ir/adapters/traces.py::_ts_seconds`` so the InjectionAdapter seed
    lands on the same time axis as trace adapter transitions regardless of how
    parquet stores ``time`` (Datetime[ns]/[us]/[ms], or int nanos/micros/secs).
    """
    if abnormal_traces.height == 0 or "time" not in abnormal_traces.columns:
        return 0
    raw = abnormal_traces["time"].min()
    if raw is None:
        return 0
    if isinstance(raw, datetime):
        return int(raw.timestamp())
    if isinstance(raw, int):
        if raw > 10**14:
            return raw // 1_000_000_000
        if raw > 10**11:
            return raw // 1_000
        return raw
    return int(raw)  # type: ignore[arg-type]


def _latest_abnormal_seconds(abnormal_traces: pl.DataFrame) -> int:
    """Latest abnormal-trace timestamp normalized to unix seconds.

    Mirrors ``ir/adapters/traces.py::_ts_seconds`` so the abnormal-window
    end used by ``TraceVolumeAdapter`` lands on the same time axis as the
    InjectionAdapter seed regardless of how parquet stores ``time``
    (Datetime[ns]/[us]/[ms], or int nanos/micros/secs).
    """
    if abnormal_traces.height == 0 or "time" not in abnormal_traces.columns:
        return 0
    raw = abnormal_traces["time"].max()
    if raw is None:
        return 0
    if isinstance(raw, datetime):
        return int(raw.timestamp())
    if isinstance(raw, int):
        if raw > 10**14:
            return raw // 1_000_000_000
        if raw > 10**11:
            return raw // 1_000
        return raw
    return int(raw)  # type: ignore[arg-type]


_LOCAL_EFFECT_BAD_STATES: frozenset[str] = frozenset(
    {"slow", "degraded", "restarting", "erroring", "silent", "unavailable", "missing"}
)


def _compute_local_effect(
    physical_node_ids: list[int],
    timelines: dict[str, StateTimeline],
    graph: HyperGraph,
) -> LocalEffect:
    """Probe injection-node timelines for any non-healthy state.

    L=1 iff ANY injection node has at least one timeline window in a state
    of severity >= 2 (slow/degraded/restarting/erroring/silent/unavailable/missing).
    """
    impacted: list[dict[str, Any]] = []
    for nid in physical_node_ids:
        node = graph.get_node_by_id(nid)
        if node is None:
            continue
        tl = timelines.get(node.uniq_name)
        if tl is None:
            continue
        bad_windows = [w for w in tl.windows if w.state in _LOCAL_EFFECT_BAD_STATES]
        if bad_windows:
            impacted.append(
                {
                    "node": node.uniq_name,
                    "states": sorted({w.state for w in bad_windows}),
                    "first_state_at": min(w.start for w in bad_windows),
                }
            )
    return LocalEffect(detected=bool(impacted), evidence={"impacted_nodes": impacted})


def _compute_slo_impact(
    alarm_nodes: set[int],
    graph: HyperGraph,
    slo_surface: SLOSurface,
) -> SLOImpact:
    names: list[str] = []
    for nid in alarm_nodes:
        n = graph.get_node_by_id(nid)
        if n is not None:
            names.append(n.uniq_name)
    return SLOImpact(
        detected=bool(alarm_nodes),
        impacted_nodes=names,
        evidence={
            "alarm_count": len(alarm_nodes),
            "slo_surface_source": slo_surface.source,
            "slo_surface_size": len(slo_surface.services),
        },
    )


def _filter_alarms_by_surface(
    alarm_node_names: list[str],
    graph: HyperGraph,
    slo_surface: SLOSurface,
) -> list[str]:
    """Restrict alarm spans to those owned by services in the explicit surface.

    For ``slo_surface.is_default()`` returns the input unchanged — the alarm
    detector's own loadgen/caller exclusion is the heuristic surface.
    """
    if slo_surface.is_default():
        return alarm_node_names
    kept: list[str] = []
    for span_name in alarm_node_names:
        node = graph.get_node_by_name(f"span|{span_name}")
        if node is None:
            continue
        owning_service = getattr(node, "service_name", None) or _extract_service_from_span_uniq(node.uniq_name)
        if owning_service in slo_surface.services:
            kept.append(span_name)
    return kept


def _extract_service_from_span_uniq(uniq_name: str) -> str | None:
    """Best-effort extraction: span uniq_name is ``span|<service>::<span_name>``.

    Returns ``None`` if the format doesn't match.
    """
    if not uniq_name.startswith("span|"):
        return None
    body = uniq_name[len("span|") :]
    if "::" not in body:
        return None
    return body.split("::", 1)[0]


def _label_to_legacy_status(label: LabelT, e_detected: bool) -> str:
    """Map new label to legacy ``status`` string for back-compat skip-logic.

    - ``attributed`` -> ``success``
    - ``ineffective`` / ``absorbed`` / ``unexplained_impact`` -> ``no_paths`` (legacy bucket)
    - When E=0 we still surface ``no_alarms`` to keep `_collect_batch_tasks`
      able to retire alarm-less cases via the existing marker.
    """
    if label == "attributed":
        return "success"
    if not e_detected:
        return "no_alarms"
    return "no_paths"


def run_single_case(
    data_dir: Path,
    max_hops: int,
    return_graph: bool = False,
    injection_data: dict[str, Any] | None = None,
    slo_surface: SLOSurface | None = None,
    inject_time_tolerance_seconds: int | None = None,
) -> dict[str, Any]:
    case_name = data_dir.name
    if case_name == "converted":
        case_name = data_dir.parent.name

    surface = slo_surface or SLOSurface.default()

    try:
        loader = ParquetDataLoader(data_dir, 2)
        graph = loader.build_graph_from_parquet()

        alarm_node_names = loader.identify_alarm_nodes_v2()
        alarm_node_names = _filter_alarms_by_surface(list(alarm_node_names), graph, surface)
        alarm_nodes = _resolve_alarm_nodes(graph, list(alarm_node_names))
        alarm_evidence_by_name = _load_alarm_evidence_index(loader)
        slo_impact = _compute_slo_impact(alarm_nodes, graph, surface)

        actual_injection_nodes = []
        resolution_info: dict[str, Any] = {}

        assert injection_data is not None
        resolver = InjectionNodeResolver(graph)
        resolved = resolver.resolve(injection_data)
        assert resolved.injection_nodes is not None
        actual_injection_nodes = resolved.injection_nodes
        resolution_info = {
            "resolved_nodes": resolved.injection_nodes,
            "start_kind": resolved.start_kind,
            "category": resolved.category,
            "fault_type": resolved.fault_type_name,
            "resolution_method": resolved.resolution_method,
        }
        logger.info(
            f"[{case_name}] Resolved injection: {resolved.fault_type_name} -> "
            f"{resolved.start_kind} ({resolved.resolution_method}): {resolved.injection_nodes}"
        )

        # Bind the active manifest (if any) for downstream Phase-3 gates.
        # The full ReasoningContext (with v_root_node_id, t0, and
        # feature_samples) is built below once the IR pipeline has run;
        # this early lookup just decides routing and logging.
        _registry = get_default_registry()
        _manifest = _registry.get(resolved.fault_type_name)
        if _manifest is None:
            logger.info("no manifest for %s, using generic rules", resolved.fault_type_name)
        else:
            logger.debug("manifest %s bound for case %s", resolved.fault_type_name, case_name)

        physical_node_ids: list[int] = []
        for injection_node in actual_injection_nodes:
            injection_node_obj = graph.get_node_by_name(injection_node)
            if injection_node_obj is None:
                logger.warning(f"[{case_name}] Injection node not found: {injection_node}")
                continue
            assert injection_node_obj.id is not None
            physical_node_ids.append(injection_node_obj.id)

        assert physical_node_ids != []

        if resolved.injection_point:
            ip = resolved.injection_point
            if resolved.category == "network":
                resolution_info["network_source"] = ip.source_service
                resolution_info["network_target"] = ip.target_service
                resolution_info["network_direction"] = ip.direction
            elif resolved.category == "dns":
                resolution_info["dns_app"] = ip.app_name
                resolution_info["dns_domain"] = ip.domain

        rules = get_builtin_rules()

        # Drive the canonical-state IR pipeline. Pick injection_at as the
        # earliest abnormal-trace timestamp (so InjectionAdapter seed lands
        # at the start of the abnormal window).
        baseline_traces = loader.load_traces("normal")
        abnormal_traces = loader.load_traces("abnormal")
        injection_at = _earliest_abnormal_seconds(abnormal_traces)
        abnormal_window_end = _latest_abnormal_seconds(abnormal_traces)

        # Per-system trace -> DB binding. Runs BEFORE the IR pipeline so that
        # the structural edges this adapter wires (service->pod routes_to,
        # stateful_set->pod manages) participate in StructuralInheritance's
        # ``container.unavailable`` -> ``service.unavailable`` cascade. Each
        # registered adapter gates itself on its system signature, so this
        # is a no-op on non-matching benchmarks.
        n_db_binding_edges = dispatch_trace_db_binding_adapters(graph, abnormal_traces, baseline_traces)
        logger.info(f"[{case_name}] trace-db-binding edges: {n_db_binding_edges}")

        ctx = AdapterContext(datapack_dir=data_dir, case_name=case_name)
        timelines = run_reasoning_ir(
            graph=graph,
            ctx=ctx,
            resolved=resolved,
            injection_at=injection_at,
            baseline_traces=baseline_traces,
            abnormal_traces=abnormal_traces,
            abnormal_window_end=abnormal_window_end,
        )
        logger.info(
            f"[{case_name}] IR pipeline: {len(timelines)} node timelines "
            f"(trace_volume window={injection_at}..{abnormal_window_end})"
        )

        # Add inferred call-graph edges for trace-blind dependencies (e.g.
        # Spring auth filters that fire before any controller span). This is
        # NOT a StateAdapter — it mutates graph topology after the IR
        # pipeline has settled, so the propagator sees the new edges
        # naturally on construction. See ir/adapters/inferred_edges.py.
        n_inferred = enrich_with_inferred_edges(graph, timelines, physical_node_ids)
        logger.info(f"[{case_name}] inferred edges: {n_inferred}")

        # Per-system log-evidence adapters: scan application logs for
        # backing-service failure patterns (HikariPool / SQLException for
        # Java/Spring, dial-tcp / EOF for Go, etc.) and add inferred
        # ``service|backing -[includes]→ span|caller_alarm`` edges that
        # the temporal-coincidence heuristic alone cannot reach (JDBC
        # traffic is not in OTel spans). See ir/adapters/log_dependency.py.
        try:
            abnormal_logs_for_deps = loader.load_logs("abnormal")
            normal_logs_for_deps = loader.load_logs("normal")
        except FileNotFoundError:
            logger.debug(f"[{case_name}] logs absent — skipping log-dependency adapters")
        else:
            n_log_inferred = dispatch_log_adapters(graph, timelines, abnormal_logs_for_deps, normal_logs_for_deps)
            logger.info(f"[{case_name}] log-inferred edges: {n_log_inferred}")

        # Resolve propagation starting points based on rule semantics
        # For HTTP response faults, propagation starts from caller service (not physical injection)
        starting_resolver = StartingPointResolver(graph)
        injection_node_ids = starting_resolver.resolve(
            physical_node_ids=physical_node_ids,
            resolved_injection=resolved,
            rules=rules,
        )
        if injection_node_ids != physical_node_ids:
            starting_node_names = [graph.get_node_by_id(nid).uniq_name for nid in injection_node_ids]
            resolution_info["starting_points"] = starting_node_names
            logger.info(
                f"[{case_name}] StartingPointResolver: propagation starts from "
                f"{starting_node_names} (physical: {actual_injection_nodes})"
            )

        local_effect = _compute_local_effect(physical_node_ids, timelines, graph)

        # Build the ReasoningContext for the manifest-aware gates. This
        # uses the IR products that have just been computed (graph,
        # timelines, traces) plus the resolved injection root.
        v_root_id: int | None = (
            injection_node_ids[0] if injection_node_ids else (physical_node_ids[0] if physical_node_ids else None)
        )
        feature_samples = extract_feature_samples(
            graph=graph,
            baseline_traces=baseline_traces,
            abnormal_traces=abnormal_traces,
            abnormal_window_start=injection_at,
            abnormal_window_end=abnormal_window_end,
            timelines=timelines,
        )
        reasoning_ctx = ReasoningContext(
            fault_type_name=resolved.fault_type_name,
            manifest=_manifest,
            v_root_node_id=v_root_id,
            t0=injection_at,
            feature_samples=feature_samples,
            registry=_registry,
            graph=graph,
        )
        if _manifest is not None:
            logger.info(
                f"[{case_name}] manifest gates active: "
                f"{len(feature_samples)} feature samples extracted "
                f"(v_root={v_root_id})"
            )

        propagator_graph = graph
        if slo_impact.detected:
            tau = (
                INJECT_TIME_TOLERANCE_SECONDS
                if inject_time_tolerance_seconds is None
                else inject_time_tolerance_seconds
            )
            delta_t = max(0, abnormal_window_end - injection_at)
            injection_window = (injection_at, injection_at + delta_t + tau)
            propagator = FaultPropagator(
                graph=graph,
                rules=rules,
                timelines=timelines,
                max_hops=max_hops,
                injection_window=injection_window,
                gates=manifest_aware_gates(reasoning_ctx),
                reasoning_ctx=reasoning_ctx,
            )
            result = propagator.propagate_from_injection(
                injection_node_ids=injection_node_ids,
                alarm_nodes=alarm_nodes,
            )
            propagator_graph = propagator.graph
        else:
            result = PropagationResult(
                injection_node_ids=injection_node_ids,
                injection_states=[],
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
            )

        _apply_terminal_alarm_confidence_caps(result, propagator_graph, alarm_nodes, alarm_evidence_by_name)
        has_path = bool(result.paths)
        label, label_reason = classify(local_effect, slo_impact, has_path)
        mechanism: MechanismPath | None = None
        if has_path:
            mechanism = MechanismPath(
                paths=list(result.paths),
                n_paths=len(result.paths),
                confidence=max((p.confidence for p in result.paths), default=0.0),
            )
        result.label = label
        result.label_reason = label_reason
        result.decomposition = FaultDecomposition(L=local_effect, E=slo_impact, M=mechanism)

        legacy_status = _label_to_legacy_status(label, slo_impact.detected)

        if has_path:
            return _process_successful_propagation(
                case_name=case_name,
                result=result,
                graph=propagator_graph,
                injection_nodes=actual_injection_nodes,
                alarm_nodes=alarm_nodes,
                return_graph=return_graph,
                data_dir=data_dir,
                resolution_info=resolution_info,
                label=label,
                label_reason=label_reason,
                alarm_evidence_by_name=alarm_evidence_by_name,
            )

        alarm_accounting = _build_alarm_accounting(result, propagator_graph, alarm_nodes, alarm_evidence_by_name)
        _save_case_result(
            data_dir=data_dir,
            case_name=case_name,
            status=legacy_status,
            result=result,
            label=label,
            label_reason=label_reason,
            alarm_accounting=alarm_accounting,
        )
        return _build_result(
            case_name,
            legacy_status,
            graph if return_graph else None,
            label=label,
            label_reason=label_reason,
        )

    except Exception as e:
        logger.exception(f"[{case_name}] Error during processing")
        return {"case": case_name, "status": "error", "error": str(e), "paths": 0}


def _resolve_alarm_nodes(graph: HyperGraph, alarm_node_names: list[str]) -> set[int]:
    """Resolve alarm node names to node IDs."""
    alarm_nodes: set[int] = set()
    for node_name in alarm_node_names:
        full_name = f"span|{node_name}"
        node = graph.get_node_by_name(full_name)
        if node and node.id is not None:
            alarm_nodes.add(node.id)
    return alarm_nodes


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
    alarm_evidence_by_name: dict[str, dict[str, Any]] | None = None,
) -> dict[str, Any]:
    """Process case with successful propagation paths."""
    primary_injection_node = injection_nodes[0] if injection_nodes else ""

    # Load span-to-service mapping from parquet files for accurate service assignment
    span_to_service_mapping = load_span_to_service_mapping(data_dir)
    alarm_evidence_by_name = alarm_evidence_by_name or {}
    default_paths, weak_paths = _split_default_and_weak_paths(result, graph, alarm_nodes, alarm_evidence_by_name)
    graph_result = _result_with_paths(result, default_paths)

    causal_graph = propagation_result_to_causal_graph(
        result=graph_result,
        graph=graph,
        injection_node_name=primary_injection_node,
        alarm_node_ids=alarm_nodes,
        span_to_service_mapping=span_to_service_mapping,
    )

    viz_paths = _build_visualization_paths(graph_result, graph, alarm_nodes)
    weak_viz_paths = _build_visualization_paths(_result_with_paths(result, weak_paths), graph, alarm_nodes)
    alarm_accounting = _build_alarm_accounting(result, graph, alarm_nodes, alarm_evidence_by_name)

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
            "alarm_nodes": list(alarm_nodes),
            "propagation_result": result.to_dict(),
            "visualization_paths": viz_paths or [],
        }
        if weak_paths:
            result_data["weak_paths"] = weak_paths
        if alarm_accounting:
            result_data.update(alarm_accounting)
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


# =============================================================================
# Batch Processing Helpers
# =============================================================================


def _log_batch_header(base_path: Path, output_path: Path, max_workers: int, max_cases: int) -> None:
    """Log batch run header."""
    logger.info("=" * 60)
    logger.info("Batch RCA Label Runner")
    logger.info("=" * 60)
    logger.info(f"Data directory: {base_path}")
    logger.info(f"Output directory: {output_path}")
    logger.info(f"Max workers: {max_workers}")
    logger.info(f"Max cases: {max_cases if max_cases > 0 else 'all'}")
    logger.info("=" * 60)


def _collect_batch_tasks(
    base_path: Path,
    max_cases: int,
    skip_existing: bool = True,
    retry_no_paths_only: bool = False,
) -> tuple[list[tuple[Path, list[str], dict[str, Any]]], int]:
    """Collect all tasks to run from case folders."""
    tasks: list[tuple[Path, list[str], dict[str, Any]]] = []
    skipped = 0

    for case_folder in sorted(base_path.iterdir()):
        if not case_folder.is_dir():
            continue
        if max_cases > 0 and len(tasks) >= max_cases:
            break

        # Two layouts in the wild:
        #   legacy:  case_folder/converted/{injection.json, parquet, ...}
        #   aegis:   case_folder/{injection.json, parquet, ...}
        # Pick whichever has injection.json so callers don't have to flag it.
        legacy_dir = case_folder / "converted"
        if (legacy_dir / "injection.json").exists():
            data_dir = legacy_dir
        elif (case_folder / "injection.json").exists():
            data_dir = case_folder
        else:
            logger.debug(f"[{case_folder.name}] Skipping: injection.json not found")
            continue

        # Validity marker: `.valid` (legacy) or any of the AegisLab markers.
        # Empty marker files; their presence is the only signal.
        if not any((case_folder / m).exists() or (data_dir / m).exists() for m in (".valid", ".done", ".finished")):
            logger.debug(f"[{case_folder.name}] Skipping: no .valid/.done/.finished marker")
            continue

        case_output_folder = data_dir

        if retry_no_paths_only:
            no_paths_marker = case_output_folder / "no_paths.marker"
            if not no_paths_marker.exists():
                skipped += 1
                continue
            no_paths_marker.unlink()

        if skip_existing and not retry_no_paths_only:
            if (case_output_folder / "result.json").exists():
                skipped += 1
                continue
            if (case_output_folder / "no_alarms.marker").exists():
                skipped += 1
                continue

        try:
            with open(data_dir / "injection.json", encoding="utf-8") as f:
                injection_data = json.load(f)

            services = _extract_services_from_injection(injection_data)
            if not services:
                logger.debug(f"[{case_folder.name}] Skipping: No services in ground_truth")
                continue

            # Keep legacy injection_nodes as fallback
            injection_nodes = [f"service|{service}" for service in services if service != "mysql"]

            if injection_nodes:
                # Pass both injection_nodes (fallback) and injection_data (for smart resolution)
                tasks.append((data_dir, injection_nodes, injection_data))

        except Exception as e:
            logger.warning(f"[{case_folder.name}] Error reading injection.json: {e}")
            continue

    return tasks, skipped


def _extract_services_from_injection(injection_data: dict[str, Any]) -> list[str]:
    """Extract service names from injection.json ground_truth field."""
    ground_truth = injection_data.get("ground_truth", {})

    if isinstance(ground_truth, dict):
        services: list[str] = ground_truth.get("service", [])
        return services
    elif isinstance(ground_truth, list):
        services = []
        for gt_item in ground_truth:
            if isinstance(gt_item, dict):
                services.extend(gt_item.get("service", []))
        return services
    return []


def _run_batch_tasks(
    tasks: list[tuple[Path, list[str], dict[str, Any]]],
    max_hops: int,
    output_path: Path,
    max_workers: int,
    log_path: Path,
) -> dict[str, int]:
    """Run batch tasks in parallel and collect statistics."""
    stats = {
        "total": len(tasks),
        "success": 0,
        "failed": 0,
        "no_alarms": 0,
        "no_paths": 0,
    }
    no_paths_records: list[dict[str, Any]] = []

    task_callables = [
        partial(
            run_single_case,
            data_dir,
            max_hops,
            False,
            injection_data,  # Pass injection_data for smart resolution
        )
        for data_dir, injection_nodes, injection_data in tasks
    ]

    results = fmap_processpool(
        task_callables,
        parallel=max_workers,
        ignore_exceptions=True,
        cpu_limit_each=2,
        log_level=logging.DEBUG,
        log_file=str(log_path),
    )

    for i, result in enumerate(tqdm(results, desc="Processing", total=len(results))):
        if result is None:
            continue

        _, injection_nodes, _ = tasks[i]
        status = result["status"]

        if status == "success":
            stats["success"] += 1
        elif status == "no_alarms":
            stats["no_alarms"] += 1
        elif status == "no_paths":
            stats["no_paths"] += 1
            no_paths_records.append({"case": result["case"], "injection_nodes": injection_nodes})
        elif status == "injection_node_not_found":
            stats["failed"] += 1
        else:
            stats["failed"] += 1

    if no_paths_records:
        no_paths_file = output_path / "no_paths_records.json"
        save_json(no_paths_records, path=no_paths_file)
        logger.info(f"Exported {len(no_paths_records)} no-paths records to: {no_paths_file}")

    return stats


def _log_batch_summary(stats: dict[str, int], total_time: float) -> None:
    """Log batch run summary."""
    logger.info("\n" + "=" * 60)
    logger.info("Batch Run Complete")
    logger.info("=" * 60)
    logger.info(f"Total tasks: {stats['total']}")
    logger.info(f"Success: {stats['success']}")
    logger.info(f"Failed: {stats['failed']}")
    logger.info(f"No alarms: {stats['no_alarms']}")
    logger.info(f"No paths: {stats['no_paths']}")
    logger.info(f"Total time: {total_time:.2f}s")
    logger.info("=" * 60)


# =============================================================================
# CLI Commands
# =============================================================================


@app.command("run")
def run(
    data_dir: str = typer.Option(..., help="Directory containing parquet data files"),
    max_hops: int = typer.Option(15, help="Maximum propagation hops"),
    manifest_dir: str | None = typer.Option(
        None,
        "--manifest-dir",
        help=(
            "Directory of fault manifest YAMLs. Defaults to the package-shipped "
            "``manifests/fault_types/``. An empty / missing directory keeps the "
            "generic-rule fallback for every fault type."
        ),
    ),
) -> int:
    """Run fault propagation analysis for a single case."""
    setup_logging(verbose=True)
    _init_manifest_registry(manifest_dir)
    total_start = time.time()

    data_path = Path(data_dir)
    output_path = Path("output")
    output_path.mkdir(parents=True, exist_ok=True)

    injection_file = data_path / "injection.json"
    if not injection_file.exists():
        logger.error(f"injection.json not found in {data_path}")
        return 1

    with open(injection_file, encoding="utf-8") as f:
        injection_data = json.load(f)

    services = _extract_services_from_injection(injection_data)
    if not services:
        logger.error("No services found in injection.json ground_truth")
        return 1

    result = run_single_case(
        data_path,
        max_hops,
        return_graph=False,
        injection_data=injection_data,
    )

    status = result["status"]
    exit_code = 0

    if status == "success":
        resolution_info = result.get("resolution_info", {})
        if resolution_info:
            logger.info(f"\n[OK] Success: {result['paths']} paths")
            logger.info(f"  Fault type: {resolution_info.get('fault_type', 'unknown')}")
            logger.info(f"  Resolved to: {resolution_info.get('start_kind', 'service')}")
            logger.info(f"  Method: {resolution_info.get('resolution_method', 'unknown')}")
        else:
            logger.info(f"\n[OK] Success: {result['paths']} paths")
    elif status == "error":
        logger.error(f"\n[ERR] Error: {result.get('error', 'Unknown error')}")
        exit_code = 1
    else:
        logger.warning(f"\n[WARN] Status: {status}")

    total_time = time.time() - total_start
    logger.info(f"\n{'=' * 60}")
    logger.info(f"Total execution time: {total_time:.2f}s")
    logger.info(f"{'=' * 60}\n")

    return exit_code


@app.command()
def batch(
    data_base_dir: str = typer.Option(
        "data/jfs/rcabench_dataset",
        help="Base directory containing case folders",
    ),
    max_cases: int = typer.Option(0, help="Maximum number of cases to run (0 = all)"),
    max_workers: int = typer.Option(12, help="Maximum number of parallel workers"),
    max_hops: int = typer.Option(15, help="Maximum propagation hops"),
    force: bool = typer.Option(False, "--force", help="Force reprocess all cases"),
    retry_no_paths: bool = typer.Option(False, "--retry-no-paths", help="Only retry no_paths cases"),
    manifest_dir: str | None = typer.Option(
        None,
        "--manifest-dir",
        help=(
            "Directory of fault manifest YAMLs. Defaults to the package-shipped "
            "``manifests/fault_types/``. An empty / missing directory keeps the "
            "generic-rule fallback for every fault type."
        ),
    ),
) -> int:
    _init_manifest_registry(manifest_dir)
    output_path = Path("output/batch_runs")
    output_path.mkdir(parents=True, exist_ok=True)
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    log_path = output_path / f"batch_{timestamp}.log"

    setup_logging(verbose=True, log_file=log_path)
    logging.getLogger("rcabench_platform.v3.internal.reasoning").setLevel(logging.WARNING)

    total_start = time.time()
    base_path = Path(data_base_dir)

    _log_batch_header(base_path, output_path, max_workers, max_cases)

    tasks, skipped = _collect_batch_tasks(
        base_path,
        max_cases,
        skip_existing=not force,
        retry_no_paths_only=retry_no_paths,
    )
    logger.info(f"Collected {len(tasks)} tasks to run")
    if skipped > 0:
        logger.info(f"Skipped {skipped} already processed cases\n")

    stats = _run_batch_tasks(tasks, max_hops, output_path, max_workers, log_path)

    total_time = time.time() - total_start
    _log_batch_summary(stats, total_time)

    return 0


_FILTER_REQUIRED_PARQUETS = (
    "abnormal_traces.parquet",
    "normal_traces.parquet",
    "abnormal_metrics.parquet",
    "abnormal_metrics_histogram.parquet",
    "abnormal_metrics_sum.parquet",
)
_FILTER_EXTERNAL_SERVICES = {"mysql", "redis", "postgres", "mongodb", "kafka", "rabbitmq"}


def _classify_case(
    case_dir: Path,
    min_services: int,
    max_gap_seconds: float,
) -> tuple[str, str]:
    """Return (verdict, detail). verdict ∈ {clean, missing_parquet, no_engine_config,
    loadgen_only, gt_no_spans, large_gap, read_error}."""
    inj_path = case_dir / "injection.json"
    if not inj_path.exists():
        return ("missing_parquet", "injection.json")
    missing = [f for f in _FILTER_REQUIRED_PARQUETS if not (case_dir / f).exists()]
    if missing:
        return ("missing_parquet", missing[0])

    import json as _json

    import polars as pl

    try:
        inj = _json.loads(inj_path.read_text())
    except Exception as exc:
        return ("read_error", f"injection.json: {type(exc).__name__}")

    eng = inj.get("engine_config") or inj.get("engine_config_summary") or []
    if not eng:
        return ("no_engine_config", "")

    try:
        ab = pl.read_parquet(case_dir / "abnormal_traces.parquet")
        nm = pl.read_parquet(case_dir / "normal_traces.parquet")
    except Exception as exc:
        return ("read_error", f"traces: {type(exc).__name__}")

    ab_svcs = set(ab["service_name"].unique().to_list()) if len(ab) else set()
    if len(ab_svcs) < min_services:
        return ("loadgen_only", f"{len(ab_svcs)} services")

    gt: set[str] = set()
    for entry in inj.get("ground_truth", []) or []:
        for s in entry.get("service", []) or []:
            gt.add(s)
    gt_internal = (gt - _FILTER_EXTERNAL_SERVICES) or gt
    missing_gt = [s for s in gt_internal if s not in ab_svcs]
    if missing_gt:
        return ("gt_no_spans", ",".join(missing_gt))

    if len(nm) and len(ab):
        from datetime import datetime as _dt

        ab_min = ab["time"].min()
        nm_max = nm["time"].max()
        if isinstance(ab_min, _dt) and isinstance(nm_max, _dt):
            gap = (ab_min - nm_max).total_seconds()
            if gap > max_gap_seconds:
                return ("large_gap", f"{gap:.0f}s")

    return ("clean", "")


@app.command()
def filter_clean(
    data_base_dir: str = typer.Option(..., help="Base directory containing case folders"),
    min_services: int = typer.Option(3, help="Minimum distinct services in abnormal_traces"),
    max_gap_seconds: float = typer.Option(30.0, help="Max normal_end → abnormal_start gap (seconds)"),
    output: str = typer.Option("-", help="Output path for clean case names ('-' = stdout)"),
    summary: bool = typer.Option(True, help="Print dirty-reason breakdown to stderr"),
) -> int:
    """Filter datapacks by data quality. Prints clean case names (one per line).

    Reject criteria: missing required parquet, no engine_config, fewer than
    `min_services` distinct services in abnormal_traces, any GT internal
    service with zero spans in abnormal_traces, or normal-to-abnormal time
    gap > max_gap_seconds.
    """
    import sys
    from collections import Counter

    base_path = Path(data_base_dir)
    if not base_path.is_dir():
        typer.echo(f"error: {base_path} is not a directory", err=True)
        raise typer.Exit(2)

    clean: list[str] = []
    reasons: Counter[str] = Counter()
    details: list[tuple[str, str, str]] = []

    for case_dir in sorted(base_path.iterdir()):
        if not case_dir.is_dir():
            continue
        verdict, detail = _classify_case(case_dir, min_services, max_gap_seconds)
        if verdict == "clean":
            clean.append(case_dir.name)
        else:
            reasons[verdict] += 1
            details.append((case_dir.name, verdict, detail))

    out_stream = sys.stdout if output == "-" else open(output, "w")
    try:
        for name in clean:
            out_stream.write(name + "\n")
    finally:
        if out_stream is not sys.stdout:
            out_stream.close()

    if summary:
        total = len(clean) + sum(reasons.values())
        print(f"clean: {len(clean)}/{total}", file=sys.stderr)
        for reason, n in reasons.most_common():
            print(f"  {n:4} {reason}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    app()
