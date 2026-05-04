"""Causal graph export conversion and metadata enrichment."""

from __future__ import annotations

import re
from collections import Counter
from typing import Any

from rcabench_platform.v3.internal.reasoning.alarm_evidence import _evidence_confidence_for_strength
from rcabench_platform.v3.internal.reasoning.ir.states import severity
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.propagation import PropagationResult
from rcabench_platform.v3.sdk.evaluation.causal_graph import CausalEdge, CausalGraph, CausalNode

_UNKNOWN_STATE = "unknown"


def _is_concrete_state(state: str) -> bool:
    return state.lower() not in {_UNKNOWN_STATE, "healthy", "unknown"}


def _primary_export_state(states: set[str] | frozenset[str] | list[str]) -> str | None:
    concrete = [str(state).lower() for state in states if _is_concrete_state(str(state))]
    if not concrete:
        return None
    return max(sorted(concrete), key=severity)


def _canonical_export_states(states: list[str] | frozenset[str] | set[str]) -> frozenset[str]:
    normalized = {str(state).lower() for state in states if str(state)}
    concrete = {state for state in normalized if _is_concrete_state(state)}
    if not concrete:
        return frozenset({_UNKNOWN_STATE}) if _UNKNOWN_STATE in normalized else frozenset(normalized)
    if "erroring" in concrete:
        return frozenset({"erroring"})
    return frozenset(concrete)


def _sync_injection_states_from_root_causes(result: PropagationResult, causal_graph: CausalGraph) -> None:
    """Make result.json use the same canonical root-state source as the graph export."""
    if not causal_graph.root_causes:
        return

    target_len = max(len(result.injection_node_ids), len(causal_graph.root_causes), len(result.injection_states))
    states = list(result.injection_states)
    if len(states) < target_len:
        states.extend([_UNKNOWN_STATE] * (target_len - len(states)))

    reasons: list[str | None] = [None] * target_len
    details: list[dict[str, Any]] = []
    for idx, root in enumerate(causal_graph.root_causes):
        primary = _primary_export_state(root.state)
        reason = root.state_resolution_reason
        if primary is not None:
            states[idx] = primary
            reason = None
        else:
            states[idx] = _UNKNOWN_STATE
            reason = reason or "no_canonical_root_state"
        reasons[idx] = reason
        details.append(
            {
                "injection_node_id": result.injection_node_ids[idx] if idx < len(result.injection_node_ids) else None,
                "component": root.component,
                "canonical_state": states[idx],
                "root_cause_states": sorted(root.state),
                "reason": reason,
            }
        )

    for idx, state in enumerate(states[:target_len]):
        if str(state).lower() == _UNKNOWN_STATE and reasons[idx] is None:
            reasons[idx] = "root_resolved_from_metadata_only"

    result.injection_states = states[:target_len]
    result.injection_state_reasons = reasons[:target_len]
    result.injection_state_details = details


def _build_confidence_breakdown(result: PropagationResult, alarm_accounting: dict[str, Any]) -> dict[str, float]:
    rule_confidence = max((p.confidence for p in result.paths), default=0.0)
    coverage = alarm_accounting.get("strong_alarm_coverage")
    alarm_coverage_confidence = float(coverage) if coverage is not None else 0.0
    evidence_confidence = alarm_coverage_confidence
    return {
        "rule_admission_confidence": rule_confidence,
        "evidence_confidence": evidence_confidence,
        "alarm_coverage_confidence": alarm_coverage_confidence,
        "final_confidence": min(rule_confidence, evidence_confidence),
    }


def _causal_graph_with_export_metadata(
    causal_graph: CausalGraph,
    *,
    case_name: str,
    result: PropagationResult,
    alarm_accounting: dict[str, Any],
    resolution_info: dict[str, Any] | None,
) -> CausalGraph:
    confidence_breakdown = _build_confidence_breakdown(result, alarm_accounting)
    update: dict[str, Any] = {
        "case_name": case_name,
        "alarm_nodes_scope": "path_terminal_alarm_nodes",
        "candidate_alarm_nodes": alarm_accounting.get("candidate_alarm_nodes", []),
        "explained_alarm_nodes": alarm_accounting.get("explained_alarm_nodes", []),
        "unexplained_alarm_nodes": alarm_accounting.get("unexplained_alarm_nodes", []),
        "candidate_alarm_count": alarm_accounting.get("candidate_alarm_count", 0),
        "explained_alarm_count": alarm_accounting.get("explained_alarm_count", 0),
        "unexplained_alarm_count": alarm_accounting.get("unexplained_alarm_count", 0),
        "candidate_strong_alarm_count": alarm_accounting.get("candidate_strong_alarm_count", 0),
        "explained_strong_alarm_count": alarm_accounting.get("explained_strong_alarm_count", 0),
        "unexplained_strong_alarm_count": alarm_accounting.get("unexplained_strong_alarm_count", 0),
        "strong_alarm_coverage": alarm_accounting.get("strong_alarm_coverage"),
        "strong_alarm_coverage_reason": alarm_accounting.get("strong_alarm_coverage_reason"),
        "confidence_breakdown": confidence_breakdown,
    }
    if resolution_info:
        update["fault_type"] = resolution_info.get("fault_type")
        update["root_resolution_method"] = resolution_info.get("resolution_method")
    return causal_graph.model_copy(update=update)


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
            states = _canonical_export_states(path.states[i]) if i < len(path.states) else frozenset()
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
