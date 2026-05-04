"""Single-case reasoning runner orchestration."""

from __future__ import annotations

import logging
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
