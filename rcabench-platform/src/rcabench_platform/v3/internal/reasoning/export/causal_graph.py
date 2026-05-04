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

    target_len = len(causal_graph.root_causes)
    states = list(result.injection_states)
    if len(states) < target_len:
        states.extend([_UNKNOWN_STATE] * (target_len - len(states)))
    else:
        states = states[:target_len]

    aligned_injection_node_ids = [
        root.injection_node_id if root.injection_node_id is not None else -1 for root in causal_graph.root_causes
    ]

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
                "injection_node_id": aligned_injection_node_ids[idx] if idx < len(aligned_injection_node_ids) else None,
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
    result.injection_node_ids = aligned_injection_node_ids
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
        "candidate_alarm_node_ids": alarm_accounting.get("candidate_alarm_node_ids", []),
        "explained_alarm_node_ids": alarm_accounting.get("explained_alarm_node_ids", []),
        "unexplained_alarm_node_ids": alarm_accounting.get("unexplained_alarm_node_ids", []),
        "candidate_alarm_count": alarm_accounting.get("candidate_alarm_count", 0),
        "explained_alarm_count": alarm_accounting.get("explained_alarm_count", 0),
        "unexplained_alarm_count": alarm_accounting.get("unexplained_alarm_count", 0),
        "path_terminal_alarm_count": len(causal_graph.path_terminal_alarm_nodes),
        "path_terminal_alarm_node_ids": causal_graph.path_terminal_alarm_node_ids,
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
        if resolution_info.get("fault_types"):
            update["fault_types"] = list(resolution_info.get("fault_types") or [])
            if len(update["fault_types"]) > 1:
                update["fault_type"] = "hybrid"
        if resolution_info.get("root_candidates"):
            update["root_candidates"] = [
                dict(candidate)
                for candidate in resolution_info.get("root_candidates") or []
                if isinstance(candidate, dict) and candidate.get("node")
            ]
    return causal_graph.model_copy(update=update)


def _resolve_root_causal_node(
    component: str,
    graph_nodes: list[CausalNode],
    fallback_states: frozenset[str],
    graph: HyperGraph | None = None,
    *,
    fault_type: str | None = None,
    root_candidate_index: int | None = None,
    resolution_method: str | None = None,
    injection_node_id: int | None = None,
    prefer_fallback_state: bool = False,
) -> CausalNode:
    graph_node = graph.get_node_by_name(component) if graph is not None else None

    def _with_root_metadata(update: dict[str, Any]) -> CausalNode:
        return CausalNode(
            **update,
            fault_type=fault_type,
            root_candidate_index=root_candidate_index,
            root_resolution_method=resolution_method,
            injection_node_id=injection_node_id,
        )

    def _stateful_nodes_for(components: list[str]) -> list[CausalNode]:
        wanted = set(components)
        return [
            node
            for node in graph_nodes
            if node.component in wanted
            and node.state
            and node.state != frozenset({_UNKNOWN_STATE})
            and node.state != frozenset({"UNKNOWN"})
            and _primary_export_state(node.state) is not None
        ]

    def _causal_node_from_stateful(component_name: str, stateful_nodes: list[CausalNode]) -> CausalNode:
        unioned_state: set[str] = set()
        timestamps: list[int] = []
        for node in stateful_nodes:
            unioned_state.update(s for s in node.state if s.lower() != _UNKNOWN_STATE)
            if node.timestamp is not None:
                timestamps.append(node.timestamp)
        return _with_root_metadata(
            {
                "component": component_name,
                "timestamp": min(timestamps) if timestamps else None,
                "state": frozenset(unioned_state) if unioned_state else fallback_states,
            }
        )

    concrete_fallback = frozenset(s for s in fallback_states if s.lower() != _UNKNOWN_STATE)
    if prefer_fallback_state and concrete_fallback and graph_node is not None:
        return _with_root_metadata({"component": component, "state": concrete_fallback})

    matching_nodes = [node for node in graph_nodes if node.component == component]
    stateful_nodes = _stateful_nodes_for([component])
    if stateful_nodes:
        return _causal_node_from_stateful(component, stateful_nodes)

    if graph is not None and graph_node is not None:
        for mapped_components in _root_state_candidate_components(graph, graph_node):
            mapped_stateful_nodes = _stateful_nodes_for(mapped_components)
            if mapped_stateful_nodes:
                # Service roots are already the user-facing component. Keep
                # that component while borrowing pod/container evidence.
                target_component = (
                    component if graph_node.kind == PlaceKind.service else mapped_stateful_nodes[0].component
                )
                return _causal_node_from_stateful(target_component, mapped_stateful_nodes)

    if concrete_fallback and graph_node is not None:
        return _with_root_metadata({"component": component, "state": concrete_fallback})

    if graph_node is not None:
        reason = "no_mappable_root_state"
    elif matching_nodes:
        reason = "no_stateful_graph_node"
    else:
        reason = "root_component_not_in_causal_graph"
    return _with_root_metadata(
        {
            "component": component,
            "state": frozenset({_UNKNOWN_STATE}),
            "state_resolution_reason": reason,
        }
    )


def _root_state_candidate_components(graph: HyperGraph, root_node: Any) -> list[list[str]]:
    """Return topology-near components whose concrete state can stand in for a root.

    Physical JVM fallbacks can resolve to a container even though the concrete
    evidence is on the service/span plane. PodFailure service fallbacks can have
    the inverse shape: the root is the service but unavailable evidence remains
    on pod/container nodes. Keep the groups ordered by semantic closeness.
    """
    if root_node.id is None:
        return []

    def _service_ids_for_pod(pod_id: int) -> list[int]:
        service_ids: list[int] = []
        for src_id, _dst_id, edge_key in graph._graph.in_edges(pod_id, keys=True):  # type: ignore[call-arg]
            if edge_key == DepKind.routes_to:
                src = graph.get_node_by_id(src_id)
                if src.kind == PlaceKind.service:
                    service_ids.append(int(src_id))
        return service_ids

    def _pod_ids_for_container(container_id: int) -> list[int]:
        pod_ids: list[int] = []
        for src_id, _dst_id, edge_key in graph._graph.in_edges(container_id, keys=True):  # type: ignore[call-arg]
            if edge_key == DepKind.runs:
                src = graph.get_node_by_id(src_id)
                if src.kind == PlaceKind.pod:
                    pod_ids.append(int(src_id))
        return pod_ids

    def _pod_ids_for_service(service_id: int) -> list[int]:
        pod_ids: list[int] = []
        for _src_id, dst_id, edge_key in graph._graph.out_edges(service_id, keys=True):  # type: ignore[call-arg]
            if edge_key == DepKind.routes_to:
                dst = graph.get_node_by_id(dst_id)
                if dst.kind == PlaceKind.pod:
                    pod_ids.append(int(dst_id))
        return pod_ids

    def _span_ids_for_service(service_id: int) -> list[int]:
        span_ids: list[int] = []
        for _src_id, dst_id, edge_key in graph._graph.out_edges(service_id, keys=True):  # type: ignore[call-arg]
            if edge_key == DepKind.includes:
                dst = graph.get_node_by_id(dst_id)
                if dst.kind == PlaceKind.span:
                    span_ids.append(int(dst_id))
        return span_ids

    def _container_ids_for_pods(pod_ids: list[int]) -> list[int]:
        container_ids: list[int] = []
        for pod_id in pod_ids:
            for _src_id, dst_id, edge_key in graph._graph.out_edges(pod_id, keys=True):  # type: ignore[call-arg]
                if edge_key == DepKind.runs:
                    dst = graph.get_node_by_id(dst_id)
                    if dst.kind == PlaceKind.container:
                        container_ids.append(int(dst_id))
        return container_ids

    def _uniq_names(node_ids: list[int]) -> list[str]:
        names: list[str] = []
        seen: set[str] = set()
        for node_id in node_ids:
            node = graph.get_node_by_id(node_id)
            if node.uniq_name not in seen:
                names.append(node.uniq_name)
                seen.add(node.uniq_name)
        return names

    if root_node.kind == PlaceKind.service:
        pod_ids = _pod_ids_for_service(root_node.id)
        return [
            _uniq_names(pod_ids + _container_ids_for_pods(pod_ids)),
        ]

    if root_node.kind == PlaceKind.container:
        pod_ids = _pod_ids_for_container(root_node.id)
        container_service_ids: list[int] = []
        for pod_id in pod_ids:
            container_service_ids.extend(_service_ids_for_pod(pod_id))
        container_span_ids: list[int] = []
        for service_id in container_service_ids:
            container_span_ids.extend(_span_ids_for_service(service_id))
        return [
            _uniq_names(container_service_ids),
            _uniq_names(container_span_ids),
            _uniq_names(pod_ids),
        ]

    if root_node.kind == PlaceKind.pod:
        pod_service_ids = _service_ids_for_pod(root_node.id)
        pod_span_ids: list[int] = []
        for service_id in pod_service_ids:
            pod_span_ids.extend(_span_ids_for_service(service_id))
        return [
            _uniq_names(pod_service_ids),
            _uniq_names(pod_span_ids),
        ]

    if root_node.kind == PlaceKind.span:
        span_service_ids: list[int] = []
        for src_id, _dst_id, edge_key in graph._graph.in_edges(root_node.id, keys=True):  # type: ignore[call-arg]
            if edge_key == DepKind.includes:
                src = graph.get_node_by_id(src_id)
                if src.kind == PlaceKind.service:
                    span_service_ids.append(int(src_id))
        return [_uniq_names(span_service_ids)]

    return []


def propagation_result_to_causal_graph(
    result: PropagationResult,
    graph: HyperGraph,
    injection_node_name: str,
    alarm_node_ids: set[int],
    span_to_service_mapping: dict[str, list[str]] | None = None,
    injection_node_names: list[str] | None = None,
    root_fallback_states: dict[str, str | list[str] | set[str] | frozenset[str]] | None = None,
    root_candidates: list[dict[str, Any]] | None = None,
) -> CausalGraph:
    """Convert PropagationResult to CausalGraph format.

    This function generates both component-level and service-level edges.
    For service-level edges, it uses the path context and parquet-based
    span-to-service mapping to correctly assign spans to services when
    the same span_name belongs to multiple services.

    Args:
        result: The propagation result
        graph: The HyperGraph
        injection_node_name: Name of the primary injection node. Kept for
            backward compatibility when ``injection_node_names`` is omitted.
        alarm_node_ids: Set of alarm node IDs
        span_to_service_mapping: Optional mapping from span_name to list of services
            (loaded from parquet files). If provided, this is used as ground truth.
        injection_node_names: Ordered root node names to export. Supplying
            all resolved roots preserves network and hybrid root sets.
        root_fallback_states: Optional per-root fallback states from metadata
            for roots that have no admitted propagation path.
        root_candidates: Ordered root metadata from the resolver. When set,
            this is the authoritative export source so same-component hybrid
            legs are not silently collapsed.
    """
    from collections import Counter

    nodes_dict: dict[str, CausalNode] = {}
    edges_set: set[tuple[str, str]] = set()
    alarm_nodes_dict: dict[int, CausalNode] = {}
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

                if node_id in alarm_node_ids and i == len(path.nodes) - 1:
                    previous = alarm_nodes_dict.get(node_id)
                    if previous is None:
                        alarm_nodes_dict[node_id] = causal_node
                    else:
                        merged_state = frozenset(set(previous.state) | set(causal_node.state))
                        timestamps = [ts for ts in (previous.timestamp, causal_node.timestamp) if ts is not None]
                        alarm_nodes_dict[node_id] = CausalNode(
                            component=component,
                            state=merged_state,
                            timestamp=min(timestamps) if timestamps else None,
                        )
            elif node_id in alarm_node_ids and i == len(path.nodes) - 1:
                causal_node = nodes_dict[node_key]
                previous = alarm_nodes_dict.get(node_id)
                if previous is None:
                    alarm_nodes_dict[node_id] = causal_node
                    continue
                merged_state = frozenset(set(previous.state) | set(states))
                timestamps = [ts for ts in (previous.timestamp, timestamp) if ts is not None]
                alarm_nodes_dict[node_id] = CausalNode(
                    component=component,
                    state=merged_state,
                    timestamp=min(timestamps) if timestamps else None,
                )

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
    root_causes = []
    root_fallback_states = root_fallback_states or {}
    if root_candidates:
        root_specs: list[dict[str, Any]] = []
        for candidate in root_candidates:
            if not isinstance(candidate, dict) or not candidate.get("node"):
                continue
            spec = dict(candidate)
            spec["_root_candidate_index"] = len(root_specs)
            root_specs.append(spec)
        if not root_specs:
            root_specs = [{"node": root_name} for root_name in (injection_node_names or [injection_node_name])]
    else:
        root_names = injection_node_names or [injection_node_name]
        seen_roots: set[str] = set()
        root_specs = []
        for root_name in root_names:
            if root_name in seen_roots:
                continue
            seen_roots.add(root_name)
            root_specs.append({"node": root_name})

    root_name_counts = Counter(str(spec.get("node") or "") for spec in root_specs)
    for idx, root_spec in enumerate(root_specs):
        root_name = str(root_spec.get("node") or "")
        if not root_name:
            continue
        root_node = graph.get_node_by_name(root_name)

        fallback_raw = root_spec.get("expected_state") or root_fallback_states.get(root_name)
        if isinstance(fallback_raw, str):
            fallback_states = frozenset({fallback_raw})
        elif fallback_raw:
            fallback_states = frozenset(str(state) for state in fallback_raw)
        elif idx < len(result.injection_states):
            fallback_states = frozenset({result.injection_states[idx]})
        else:
            fallback_states = frozenset()
        root_causes.append(
            _resolve_root_causal_node(
                root_name,
                graph_nodes,
                fallback_states,
                graph,
                fault_type=str(root_spec["fault_type_name"]) if root_spec.get("fault_type_name") else None,
                root_candidate_index=int(root_spec["_root_candidate_index"])
                if "_root_candidate_index" in root_spec
                else None,
                resolution_method=str(root_spec["resolution_method"]) if root_spec.get("resolution_method") else None,
                injection_node_id=root_node.id if root_node is not None else -1,
                prefer_fallback_state=root_name_counts[root_name] > 1 and root_spec.get("expected_state") is not None,
            )
        )

    return CausalGraph(
        nodes=graph_nodes,
        edges=edges,
        root_causes=root_causes,
        alarm_nodes=list(alarm_nodes_dict.values()),
        path_terminal_alarm_nodes=list(alarm_nodes_dict.values()),
        path_terminal_alarm_count=len(alarm_nodes_dict),
        path_terminal_alarm_node_ids=sorted(alarm_nodes_dict),
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
