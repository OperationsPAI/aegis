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
from rcabench_platform.v3.internal.reasoning.algorithms.propagator import FaultPropagator
from rcabench_platform.v3.internal.reasoning.algorithms.starting_point_resolver import StartingPointResolver
from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.inferred_edges import enrich_with_inferred_edges
from rcabench_platform.v3.internal.reasoning.ir.adapters.log_dependency import dispatch_log_adapters
from rcabench_platform.v3.internal.reasoning.ir.pipeline import run_reasoning_ir
from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import ParquetDataLoader
from rcabench_platform.v3.internal.reasoning.loaders.utils import fmap_processpool
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import InjectionNodeResolver
from rcabench_platform.v3.internal.reasoning.models.propagation import PropagationResult
from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules
from rcabench_platform.v3.sdk.evaluation.causal_graph import CausalEdge, CausalGraph, CausalNode
from rcabench_platform.v3.sdk.utils.serde import save_json

logger = logging.getLogger(__name__)
app = typer.Typer(name="reason", help="Fault propagation reasoning engine CLI")


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

    injection_node_obj = graph.get_node_by_name(injection_node_name)
    root_causes = []
    if injection_node_obj:
        injection_states = frozenset(result.injection_states) if result.injection_states else frozenset()
        root_causes.append(
            CausalNode(
                component=injection_node_name,
                state=injection_states,
            )
        )

    return CausalGraph(
        nodes=list(nodes_dict.values()),
        edges=edges,
        root_causes=root_causes,
        alarm_nodes=list(alarm_nodes_dict.values()),
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


def run_single_case(
    data_dir: Path,
    max_hops: int,
    return_graph: bool = False,
    injection_data: dict[str, Any] | None = None,
) -> dict[str, Any]:
    case_name = data_dir.name
    if case_name == "converted":
        case_name = data_dir.parent.name

    try:
        loader = ParquetDataLoader(data_dir, 2)
        graph = loader.build_graph_from_parquet()

        alarm_node_names = loader.identify_alarm_nodes_v2()
        alarm_nodes = _resolve_alarm_nodes(graph, list(alarm_node_names))

        if not alarm_nodes:
            _save_case_result(data_dir, case_name, "no_alarms")
            return _build_result(case_name, "no_alarms", graph if return_graph else None)

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

        propagator = FaultPropagator(
            graph=graph,
            rules=rules,
            timelines=timelines,
            max_hops=max_hops,
        )
        result = propagator.propagate_from_injection(
            injection_node_ids=injection_node_ids,
            alarm_nodes=alarm_nodes,
        )

        if result.paths:
            return _process_successful_propagation(
                case_name=case_name,
                result=result,
                graph=propagator.graph,
                injection_nodes=actual_injection_nodes,
                alarm_nodes=alarm_nodes,
                return_graph=return_graph,
                data_dir=data_dir,
                resolution_info=resolution_info,
            )
        else:
            _save_case_result(data_dir, case_name, "no_paths")
            return _build_result(case_name, "no_paths", graph if return_graph else None)

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
) -> dict[str, Any]:
    """Process case with successful propagation paths."""
    primary_injection_node = injection_nodes[0] if injection_nodes else ""

    # Load span-to-service mapping from parquet files for accurate service assignment
    span_to_service_mapping = load_span_to_service_mapping(data_dir)

    causal_graph = propagation_result_to_causal_graph(
        result=result,
        graph=graph,
        injection_node_name=primary_injection_node,
        alarm_node_ids=alarm_nodes,
        span_to_service_mapping=span_to_service_mapping,
    )

    viz_paths = _build_visualization_paths(result, graph, alarm_nodes)

    _save_case_result(
        data_dir=data_dir,
        case_name=case_name,
        status="success",
        causal_graph=causal_graph,
        injection_nodes=injection_nodes,
        alarm_nodes=alarm_nodes,
        result=result,
        viz_paths=viz_paths,
        resolution_info=resolution_info,
    )

    ret: dict[str, Any] = {
        "case": case_name,
        "status": "success",
        "paths": len(result.paths),
        "propagation_result": result,
    }
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
    resolution_info: dict[str, Any] | None = None,
) -> None:
    _clean_previous_results(data_dir)

    if status == "success" and causal_graph and result and injection_nodes is not None and alarm_nodes is not None:
        graph_data = causal_graph.model_dump()
        save_json(graph_data, path=data_dir / "causal_graph.json")

        result_data: dict[str, Any] = {
            "case_name": case_name,
            "injection_nodes": injection_nodes,
            "alarm_nodes": list(alarm_nodes),
            "propagation_result": result.to_dict(),
            "visualization_paths": viz_paths or [],
        }
        if resolution_info:
            result_data["resolution_info"] = resolution_info
        save_json(result_data, path=data_dir / "result.json")
        logger.info(f"[{case_name}] Saved causal_graph.json and result.json")

    elif status == "no_alarms":
        (data_dir / "no_alarms.marker").touch()
        logger.info(f"[{case_name}] No alarm nodes found, created marker")

    elif status == "no_paths":
        (data_dir / "no_paths.marker").touch()
        logger.info(f"[{case_name}] No propagation paths found, created marker")


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

        data_dir = case_folder / "converted"
        injection_file = data_dir / "injection.json"
        if not injection_file.exists():
            logger.debug(f"[{case_folder.name}] Skipping: injection.json not found")
            continue

        valid_marker = case_folder / ".valid"
        if not valid_marker.exists():
            logger.debug(f"[{case_folder.name}] Skipping: .valid marker not found")
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
            with open(injection_file, encoding="utf-8") as f:
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
    max_hops: int = typer.Option(20, help="Maximum propagation hops"),
) -> int:
    """Run fault propagation analysis for a single case."""
    setup_logging(verbose=True)
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
    max_hops: int = typer.Option(30, help="Maximum propagation hops"),
    force: bool = typer.Option(False, "--force", help="Force reprocess all cases"),
    retry_no_paths: bool = typer.Option(False, "--retry-no-paths", help="Only retry no_paths cases"),
) -> int:
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


if __name__ == "__main__":
    app()
