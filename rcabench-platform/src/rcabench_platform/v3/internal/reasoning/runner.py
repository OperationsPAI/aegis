"""Single-case reasoning runner orchestration."""

from __future__ import annotations

import logging
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any

import polars as pl

from rcabench_platform.v3.internal.reasoning.alarm_evidence import (
    _apply_terminal_alarm_confidence_caps,
    _build_alarm_accounting,
    _load_alarm_evidence_index,
)
from rcabench_platform.v3.internal.reasoning.algorithms.gates import (
    INJECT_TIME_TOLERANCE_SECONDS,
    manifest_aware_gates,
)
from rcabench_platform.v3.internal.reasoning.algorithms.label_classifier import classify
from rcabench_platform.v3.internal.reasoning.algorithms.propagator import FaultPropagator
from rcabench_platform.v3.internal.reasoning.algorithms.starting_point_resolver import StartingPointResolver
from rcabench_platform.v3.internal.reasoning.config.slo_surface import SLOSurface
from rcabench_platform.v3.internal.reasoning.export.result_writer import (
    _build_result,
    _process_successful_propagation,
    _save_case_result,
)
from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.inferred_edges import enrich_with_inferred_edges
from rcabench_platform.v3.internal.reasoning.ir.adapters.log_dependency import dispatch_log_adapters
from rcabench_platform.v3.internal.reasoning.ir.adapters.trace_db_binding import dispatch_trace_db_binding_adapters
from rcabench_platform.v3.internal.reasoning.ir.pipeline import run_reasoning_ir
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import ParquetDataLoader
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
from rcabench_platform.v3.internal.reasoning.manifests.extractors import extract_feature_samples
from rcabench_platform.v3.internal.reasoning.manifests.registry import get_default_registry
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph
from rcabench_platform.v3.internal.reasoning.models.injection import (
    InjectionNodeResolver,
    ResolvedInjection,
    ResolvedRootCandidate,
)
from rcabench_platform.v3.internal.reasoning.models.propagation import (
    FaultDecomposition,
    LabelT,
    LocalEffect,
    MechanismPath,
    PropagationResult,
    SLOImpact,
)
from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules

logger = logging.getLogger(__name__)

_LOCAL_EFFECT_BAD_STATES: frozenset[str] = frozenset(
    {"slow", "degraded", "restarting", "erroring", "silent", "unavailable", "missing"}
)


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


def _resolve_alarm_nodes(graph: HyperGraph, alarm_node_names: list[str]) -> set[int]:
    """Resolve alarm node names to node IDs."""
    alarm_nodes: set[int] = set()
    for node_name in alarm_node_names:
        full_name = f"span|{node_name}"
        node = graph.get_node_by_name(full_name)
        if node and node.id is not None:
            alarm_nodes.add(node.id)
    return alarm_nodes


@dataclass(frozen=True)
class _PropagationUnit:
    fault_type_name: str
    start_kind: str
    category: str
    fault_category: str
    resolution_method: str
    root_group_id: int | None
    root_candidate_indices: list[int]
    physical_node_names: list[str]
    physical_node_ids: list[int]
    starting_node_ids: list[int]
    manifest: Any


def _unique_ints(values: list[int]) -> list[int]:
    out: list[int] = []
    seen: set[int] = set()
    for value in values:
        if value in seen:
            continue
        out.append(value)
        seen.add(value)
    return out


def _unit_to_resolution_info(unit: _PropagationUnit, graph: HyperGraph) -> dict[str, Any]:
    return {
        "fault_type": unit.fault_type_name,
        "root_candidate_indices": unit.root_candidate_indices,
        "root_group_id": unit.root_group_id,
        "physical_nodes": unit.physical_node_names,
        "starting_points": [
            node.uniq_name for node_id in unit.starting_node_ids if (node := graph.get_node_by_id(node_id)) is not None
        ],
        "manifest_bound": unit.manifest is not None,
        "manifest_multi_v_root": bool(getattr(unit.manifest, "multi_v_root", False)),
    }


def _build_propagation_units(
    *,
    graph: HyperGraph,
    resolved: ResolvedInjection,
    registry: Any,
    rules: list[Any],
    starting_resolver: StartingPointResolver,
) -> list[_PropagationUnit]:
    """Bind hybrid roots as independent single-manifest propagation legs."""
    raw_candidates = list(resolved.root_candidates)
    if not raw_candidates:
        raw_candidates = [
            ResolvedRootCandidate(
                node=node,
                start_kind=resolved.start_kind,
                category=resolved.category,
                fault_category=resolved.fault_category,
                fault_type_name=resolved.fault_type_name,
                resolution_method=resolved.resolution_method,
                root_group_id=None,
            )
            for node in resolved.injection_nodes
        ]

    provisional: list[_PropagationUnit] = []
    for idx, candidate in enumerate(raw_candidates):
        graph_node = graph.get_node_by_name(candidate.node)
        if graph_node is None or graph_node.id is None:
            logger.warning("Skipping unresolved root candidate %s", candidate.node)
            continue

        single = ResolvedInjection(
            injection_nodes=[candidate.node],
            start_kind=candidate.start_kind,
            category=candidate.category,
            fault_category=candidate.fault_category,
            fault_type_name=candidate.fault_type_name,
            resolution_method=candidate.resolution_method,
            root_candidates=[candidate],
        )
        starting_ids = starting_resolver.resolve(
            physical_node_ids=[graph_node.id],
            resolved_injection=single,
            rules=rules,
        )
        if not starting_ids:
            starting_ids = [graph_node.id]

        manifest = registry.get(candidate.fault_type_name)
        provisional.append(
            _PropagationUnit(
                fault_type_name=candidate.fault_type_name,
                start_kind=candidate.start_kind,
                category=candidate.category,
                fault_category=candidate.fault_category,
                resolution_method=candidate.resolution_method,
                root_group_id=candidate.root_group_id,
                root_candidate_indices=[idx],
                physical_node_names=[candidate.node],
                physical_node_ids=[graph_node.id],
                starting_node_ids=_unique_ints(starting_ids),
                manifest=manifest,
            )
        )

    grouped: dict[tuple[str, int | str | None], _PropagationUnit] = {}
    units: list[_PropagationUnit] = []
    for unit in provisional:
        if bool(getattr(unit.manifest, "multi_v_root", False)):
            group_key = (
                unit.fault_type_name,
                unit.root_group_id if unit.root_group_id is not None else unit.resolution_method,
            )
            previous = grouped.get(group_key)
            if previous is None:
                grouped[group_key] = unit
                units.append(unit)
                continue
            merged = _PropagationUnit(
                fault_type_name=previous.fault_type_name,
                start_kind=previous.start_kind,
                category=previous.category,
                fault_category=previous.fault_category,
                resolution_method=previous.resolution_method,
                root_group_id=previous.root_group_id,
                root_candidate_indices=previous.root_candidate_indices + unit.root_candidate_indices,
                physical_node_names=previous.physical_node_names + unit.physical_node_names,
                physical_node_ids=_unique_ints(previous.physical_node_ids + unit.physical_node_ids),
                starting_node_ids=_unique_ints(previous.starting_node_ids + unit.starting_node_ids),
                manifest=previous.manifest,
            )
            grouped[group_key] = merged
            units[units.index(previous)] = merged
            continue

        for starting_node_id in unit.starting_node_ids:
            units.append(
                _PropagationUnit(
                    fault_type_name=unit.fault_type_name,
                    start_kind=unit.start_kind,
                    category=unit.category,
                    fault_category=unit.fault_category,
                    resolution_method=unit.resolution_method,
                    root_group_id=unit.root_group_id,
                    root_candidate_indices=unit.root_candidate_indices,
                    physical_node_names=unit.physical_node_names,
                    physical_node_ids=unit.physical_node_ids,
                    starting_node_ids=[starting_node_id],
                    manifest=unit.manifest,
                )
            )

    return units


def _build_physical_fallback_units(
    *,
    graph: HyperGraph,
    resolved: ResolvedInjection,
    registry: Any,
    physical_node_ids: list[int],
) -> list[_PropagationUnit]:
    """Materialize the logged physical-node fallback as real propagation units."""
    manifest = registry.get(resolved.fault_type_name)
    node_ids = _unique_ints(physical_node_ids)
    node_names = [node.uniq_name for node_id in node_ids if (node := graph.get_node_by_id(node_id)) is not None]
    if not node_ids:
        return []
    if bool(getattr(manifest, "multi_v_root", False)):
        return [
            _PropagationUnit(
                fault_type_name=resolved.fault_type_name,
                start_kind=resolved.start_kind,
                category=resolved.category,
                fault_category=resolved.fault_category,
                resolution_method=f"{resolved.resolution_method}_physical_fallback",
                root_group_id=None,
                root_candidate_indices=[],
                physical_node_names=node_names,
                physical_node_ids=node_ids,
                starting_node_ids=node_ids,
                manifest=manifest,
            )
        ]
    return [
        _PropagationUnit(
            fault_type_name=resolved.fault_type_name,
            start_kind=resolved.start_kind,
            category=resolved.category,
            fault_category=resolved.fault_category,
            resolution_method=f"{resolved.resolution_method}_physical_fallback",
            root_group_id=None,
            root_candidate_indices=[],
            physical_node_names=[node_name],
            physical_node_ids=[node_id],
            starting_node_ids=[node_id],
            manifest=manifest,
        )
        for node_id, node_name in zip(node_ids, node_names, strict=False)
    ]


def _merge_propagation_results(results: list[PropagationResult], injection_node_ids: list[int]) -> PropagationResult:
    if not results:
        return PropagationResult(
            injection_node_ids=injection_node_ids,
            injection_states=["unknown"] * len(injection_node_ids),
            paths=[],
            visited_nodes=set(),
            max_hops_reached=0,
        )

    states_by_occurrence: list[tuple[int, str]] = []
    reasons_by_occurrence: list[tuple[int, str | None]] = []
    details_by_occurrence: list[tuple[int, dict[str, Any]]] = []
    for result in results:
        for idx, node_id in enumerate(result.injection_node_ids):
            if idx < len(result.injection_states):
                states_by_occurrence.append((node_id, result.injection_states[idx]))
            if idx < len(result.injection_state_reasons):
                reasons_by_occurrence.append((node_id, result.injection_state_reasons[idx]))
            if idx < len(result.injection_state_details):
                details_by_occurrence.append((node_id, dict(result.injection_state_details[idx])))

    injection_states: list[str] = []
    injection_state_reasons: list[str | None] = []
    injection_state_details: list[dict[str, Any]] = []
    used_state_occurrences: set[int] = set()
    used_reason_occurrences: set[int] = set()
    used_detail_occurrences: set[int] = set()
    for node_id in injection_node_ids:
        state_idx = next(
            (
                idx
                for idx, (occurrence_node_id, _state) in enumerate(states_by_occurrence)
                if occurrence_node_id == node_id and idx not in used_state_occurrences
            ),
            None,
        )
        if state_idx is None:
            injection_states.append("unknown")
        else:
            used_state_occurrences.add(state_idx)
            injection_states.append(states_by_occurrence[state_idx][1])

        reason_idx = next(
            (
                idx
                for idx, (occurrence_node_id, _reason) in enumerate(reasons_by_occurrence)
                if occurrence_node_id == node_id and idx not in used_reason_occurrences
            ),
            None,
        )
        if reason_idx is None:
            injection_state_reasons.append(None)
        else:
            used_reason_occurrences.add(reason_idx)
            injection_state_reasons.append(reasons_by_occurrence[reason_idx][1])

        detail_idx = next(
            (
                idx
                for idx, (occurrence_node_id, _detail) in enumerate(details_by_occurrence)
                if occurrence_node_id == node_id and idx not in used_detail_occurrences
            ),
            None,
        )
        if detail_idx is not None:
            used_detail_occurrences.add(detail_idx)
            injection_state_details.append(details_by_occurrence[detail_idx][1])

    return PropagationResult(
        injection_node_ids=injection_node_ids,
        injection_states=injection_states,
        paths=[path for result in results for path in result.paths],
        visited_nodes=set().union(*(result.visited_nodes for result in results)),
        max_hops_reached=max(result.max_hops_reached for result in results),
        subgraph_edges=[edge for result in results for edge in result.subgraph_edges],
        warnings=[warning for result in results for warning in result.warnings],
        rejected_paths=[path for result in results for path in result.rejected_paths],
        injection_state_reasons=injection_state_reasons,
        injection_state_details=injection_state_details,
    )


def _sync_no_path_root_states_from_candidates(
    result: PropagationResult,
    *,
    resolved: ResolvedInjection,
    graph: HyperGraph,
) -> None:
    """Populate root-scoped state arrays when no causal graph is exported."""
    candidates = list(resolved.root_candidates)
    if not candidates:
        candidates = [
            ResolvedRootCandidate(
                node=node,
                start_kind=resolved.start_kind,
                category=resolved.category,
                fault_category=resolved.fault_category,
                fault_type_name=resolved.fault_type_name,
                resolution_method=resolved.resolution_method,
            )
            for node in resolved.injection_nodes
        ]
    if not candidates:
        return

    node_ids: list[int] = []
    states: list[str] = []
    reasons: list[str | None] = []
    details: list[dict[str, Any]] = []
    for candidate in candidates:
        graph_node = graph.get_node_by_name(candidate.node)
        node_id = graph_node.id if graph_node is not None and graph_node.id is not None else -1
        if node_id == -1:
            state = "unknown"
            reason = "root_component_not_in_causal_graph"
        elif candidate.expected_state:
            state = candidate.expected_state
            reason = None
        else:
            state = "unknown"
            reason = "root_resolved_from_metadata_only"
        node_ids.append(node_id)
        states.append(state)
        reasons.append(reason)
        details.append(
            {
                "injection_node_id": node_id,
                "component": candidate.node,
                "canonical_state": state,
                "root_cause_states": [state],
                "reason": reason,
            }
        )

    result.injection_node_ids = node_ids
    result.injection_states = states
    result.injection_state_reasons = reasons
    result.injection_state_details = details


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
        if resolved.root_candidates:
            root_candidates = [candidate.model_dump(exclude_none=True) for candidate in resolved.root_candidates]
            resolution_info["root_candidates"] = root_candidates
            resolution_info["fault_types"] = sorted(
                {str(candidate["fault_type_name"]) for candidate in root_candidates if candidate.get("fault_type_name")}
            )
        logger.info(
            f"[{case_name}] Resolved injection: {resolved.fault_type_name} -> "
            f"{resolved.start_kind} ({resolved.resolution_method}): {resolved.injection_nodes}"
        )

        _registry = get_default_registry()

        physical_node_ids: list[int] = []
        for injection_node in actual_injection_nodes:
            injection_node_obj = graph.get_node_by_name(injection_node)
            if injection_node_obj is None:
                logger.warning(f"[{case_name}] Injection node not found: {injection_node}")
                continue
            assert injection_node_obj.id is not None
            physical_node_ids.append(injection_node_obj.id)

        if not physical_node_ids:
            logger.warning(
                f"[{case_name}] no resolved injection node exists in graph; "
                "continuing with metadata root candidates for export"
            )

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
        n_inferred = enrich_with_inferred_edges(graph, timelines, physical_node_ids) if physical_node_ids else 0
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

        # Resolve propagation starting points per root candidate. Hybrid
        # injections are multiple independent manifest legs, not one primary
        # manifest with a long root list.
        starting_resolver = StartingPointResolver(graph)
        propagation_units = _build_propagation_units(
            graph=graph,
            resolved=resolved,
            registry=_registry,
            rules=rules,
            starting_resolver=starting_resolver,
        )
        if not propagation_units:
            logger.warning(f"[{case_name}] no propagation units resolved; falling back to physical nodes")
            propagation_units = _build_physical_fallback_units(
                graph=graph,
                resolved=resolved,
                registry=_registry,
                physical_node_ids=physical_node_ids,
            )
        resolution_info["propagation_units"] = [_unit_to_resolution_info(unit, graph) for unit in propagation_units]
        injection_node_ids = [node_id for unit in propagation_units for node_id in unit.starting_node_ids] or list(
            physical_node_ids
        )
        if injection_node_ids != physical_node_ids:
            starting_node_names = [graph.get_node_by_id(nid).uniq_name for nid in injection_node_ids]
            resolution_info["starting_points"] = starting_node_names
            logger.info(
                f"[{case_name}] StartingPointResolver: propagation starts from "
                f"{starting_node_names} (physical: {actual_injection_nodes})"
            )

        local_effect = _compute_local_effect(physical_node_ids, timelines, graph)

        feature_samples = extract_feature_samples(
            graph=graph,
            baseline_traces=baseline_traces,
            abnormal_traces=abnormal_traces,
            abnormal_window_start=injection_at,
            abnormal_window_end=abnormal_window_end,
            timelines=timelines,
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
            unit_results: list[PropagationResult] = []
            for unit in propagation_units:
                v_root_id = unit.starting_node_ids[0] if unit.starting_node_ids else None
                reasoning_ctx = ReasoningContext(
                    fault_type_name=unit.fault_type_name,
                    manifest=unit.manifest,
                    v_root_node_id=v_root_id,
                    t0=injection_at,
                    feature_samples=feature_samples,
                    registry=_registry,
                    graph=graph,
                )
                if unit.manifest is None:
                    logger.info("no manifest for %s, using manifest-only empty result", unit.fault_type_name)
                else:
                    logger.info(
                        f"[{case_name}] manifest gates active: "
                        f"{unit.fault_type_name} roots={unit.starting_node_ids} "
                        f"features={len(feature_samples)}"
                    )
                propagator = FaultPropagator(
                    graph=graph,
                    rules=rules,
                    timelines=timelines,
                    max_hops=max_hops,
                    injection_window=injection_window,
                    gates=manifest_aware_gates(reasoning_ctx),
                    reasoning_ctx=reasoning_ctx,
                )
                unit_result = propagator.propagate_from_injection(
                    injection_node_ids=unit.starting_node_ids,
                    alarm_nodes=alarm_nodes,
                )
                unit_result.warnings = [
                    f"{unit.fault_type_name}@{unit.physical_node_names}: {warning}" for warning in unit_result.warnings
                ]
                unit_results.append(unit_result)
                propagator_graph = propagator.graph
            result = _merge_propagation_results(unit_results, injection_node_ids)
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
        _sync_no_path_root_states_from_candidates(result, resolved=resolved, graph=propagator_graph)
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
