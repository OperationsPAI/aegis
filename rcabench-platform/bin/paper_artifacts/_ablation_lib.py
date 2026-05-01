"""Shared worker for FORGE ablation harness.

Provides ``run_one_case(case_dir, config_name, mode, ...)`` that runs the
v3 reasoning pipeline on one case under one of six configurations:

    baseline                — defaults (canonical pipeline)
    skip_topology           — replace rule set R with admit-all
    skip_screen             — drop DriftGate from the gate stack
    slo_x{0.5,1.5,2.0}      — scale alarm-detection thresholds

Two ``mode``s mirror ``sham_injection_fp.py`` (v2 fault-free) and the
``cli.run_single_case`` real-injection pipeline:

    real         — full pipeline against actual abnormal traces with
                   the resolved real injection node; returns
                   (label, n_paths).
    fault_free   — equivalent to ``sham_injection_fp.py --mode v2``:
                   split ``normal_traces`` in half (first = baseline,
                   second = fake-abnormal) AND swap the injection node
                   for a random sham target of matching PlaceKind not
                   in the case's ground truth. With no real fault and
                   an unrelated target, any ``attributed`` label is a
                   joint false positive of the (gate stack × rule set)
                   pipeline under this configuration.

This module is import-side-effect-free; it is intended to be invoked
inside ``ProcessPoolExecutor`` workers.
"""

from __future__ import annotations

import hashlib
import json
import random
from pathlib import Path
from typing import Any, Literal

CONFIG_NAMES = (
    "baseline",
    "skip_topology",
    "skip_screen",
    "slo_x0.5",
    "slo_x1.5",
    "slo_x2.0",
)

MODES = ("real", "fault_free")


def _slo_scale_for_config(config_name: str) -> float:
    if config_name.startswith("slo_x"):
        return float(config_name[len("slo_x") :])
    return 1.0


def _build_admit_all_rules() -> list[Any]:
    """Synthesize a permissive PropagationRule per (src_kind, edge_kind,
    direction, dst_kind) tuple.

    Empty ``src_states`` / ``possible_dst_states`` makes the rule kind-
    agnostic at the state level — combined with PathBuilder's "if
    rule_src_states is empty, accept" semantics, this admits any edge
    that happens to exist between two nodes of those kinds. The rule
    set therefore prunes nothing topologically: every connected edge in
    the graph passes."""
    from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, PlaceKind
    from rcabench_platform.v3.internal.reasoning.rules.schema import (
        FirstHopConfig,
        PropagationDirection,
        PropagationRule,
        RuleTier,
    )

    # Universe of canonical IR states (see ir/states.py + cli._LOCAL_EFFECT_BAD_STATES).
    all_states = [
        "healthy",
        "slow",
        "degraded",
        "restarting",
        "erroring",
        "silent",
        "unavailable",
        "missing",
        "unknown",
    ]

    # Lenient first-hop config: do not require source or destination
    # states to match the rule's own state lists. Combined with the
    # exhaustive ``possible_dst_states`` list below, this is the
    # strongest possible "admit-all" semantics that still respects the
    # PropagationRule contract enforced by RuleMatcher.
    lenient_first = FirstHopConfig(
        require_src_states=False,
        require_dst_states=False,
        lenient_dst_state_match=True,
    )

    rules: list[PropagationRule] = []
    for src_kind in PlaceKind:
        for dst_kind in PlaceKind:
            for edge_kind in DepKind:
                for direction in (PropagationDirection.FORWARD, PropagationDirection.BACKWARD):
                    rules.append(
                        PropagationRule(
                            rule_id=f"ADMIT_ALL_{src_kind.value}_{edge_kind.value}_{direction.value}_{dst_kind.value}",
                            description="Admit-all ablation rule (R skip-topology).",
                            tier=RuleTier.core,
                            src_kind=src_kind,
                            src_states=list(all_states),
                            edge_kind=edge_kind,
                            direction=direction,
                            dst_kind=dst_kind,
                            possible_dst_states=list(all_states),
                            first_hop_config=lenient_first,
                            confidence=0.5,
                            source="ablation_admit_all",
                        )
                    )
    return rules


def _build_skip_screen_gates() -> list[Any]:
    """Gate stack with DriftGate (the §11.2 statistical screen) removed."""
    from rcabench_platform.v3.internal.reasoning.algorithms.gates import (
        InjectTimeGate,
        TemporalGate,
        TopologyGate,
    )

    return [TopologyGate(), TemporalGate(), InjectTimeGate()]


def _patch_alarm_thresholds(scale: float) -> None:
    """Monkey-patch ``get_adaptive_threshold`` so its output is multiplied
    by ``scale``. Larger scale ⇒ looser detection (fewer alarms);
    smaller scale ⇒ tighter detection (more alarms). Applied per
    worker process before ``identify_alarm_nodes_v2`` runs."""
    from rcabench_platform.v3.internal.reasoning.algorithms import baseline_detector
    from rcabench_platform.v3.internal.reasoning.loaders import parquet_loader

    if getattr(parquet_loader, "_ABLATION_ALARM_PATCHED", False):
        return

    original = baseline_detector.get_adaptive_threshold

    def scaled(*args: Any, **kwargs: Any) -> float:
        return original(*args, **kwargs) * scale

    # ``parquet_loader`` imported the function by name, so patch *that*
    # symbol; the module-level reference is what ``identify_alarm_nodes_v2``
    # resolves.
    parquet_loader.get_adaptive_threshold = scaled  # type: ignore[attr-defined]
    parquet_loader._ABLATION_ALARM_PATCHED = True  # type: ignore[attr-defined]
    parquet_loader._ABLATION_ALARM_SCALE = scale  # type: ignore[attr-defined]


def _build_excluded_uniq_names(injection_data: dict[str, Any]) -> set[str]:
    """uniq_names that must NOT be picked as a sham target.

    Mirrors the same exclusion logic as ``sham_injection_fp.py``.
    """
    excluded: set[str] = set()
    gt = injection_data.get("ground_truth") or {}
    for kind in ("container", "pod", "service", "span", "function"):
        for name in gt.get(kind) or []:
            if not name:
                continue
            excluded.add(f"{kind}|{name}")
            if kind == "span":
                excluded.add(f"span|{name}")
    return excluded


def run_one_case(
    case_dir_str: str,
    config_name: str,
    mode: Literal["real", "fault_free"],
    max_hops: int = 15,
) -> dict[str, Any]:
    """Run pipeline on one case under ``config_name`` × ``mode``.

    Returns a dict with at least ``case``, ``label``, ``n_paths``, and
    ``error``. ``label`` is ``None`` if the case errored before
    classification."""
    from rcabench_platform.v3.internal.reasoning.algorithms.gates import (
        INJECT_TIME_TOLERANCE_SECONDS,
        manifest_aware_gates,
    )
    from rcabench_platform.v3.internal.reasoning.algorithms.label_classifier import classify
    from rcabench_platform.v3.internal.reasoning.algorithms.propagator import FaultPropagator
    from rcabench_platform.v3.internal.reasoning.algorithms.starting_point_resolver import (
        StartingPointResolver,
    )
    from rcabench_platform.v3.internal.reasoning.cli import (
        _compute_local_effect,
        _compute_slo_impact,
        _earliest_abnormal_seconds,
        _filter_alarms_by_surface,
        _latest_abnormal_seconds,
        _resolve_alarm_nodes,
    )
    from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
    from rcabench_platform.v3.internal.reasoning.manifests.extractors.feature_extractor import (
        extract_feature_samples,
    )
    from rcabench_platform.v3.internal.reasoning.manifests.registry import (
        get_default_registry,
    )
    from rcabench_platform.v3.internal.reasoning.config.slo_surface import SLOSurface
    from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
    from rcabench_platform.v3.internal.reasoning.ir.adapters.inferred_edges import (
        enrich_with_inferred_edges,
    )
    from rcabench_platform.v3.internal.reasoning.ir.adapters.log_dependency import (
        dispatch_log_adapters,
    )
    from rcabench_platform.v3.internal.reasoning.ir.adapters.trace_db_binding import (
        dispatch_trace_db_binding_adapters,
    )
    from rcabench_platform.v3.internal.reasoning.ir.pipeline import run_reasoning_ir
    from rcabench_platform.v3.internal.reasoning.loaders.parquet_loader import ParquetDataLoader
    from rcabench_platform.v3.internal.reasoning.models.injection import InjectionNodeResolver
    from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules

    case_dir = Path(case_dir_str)
    case_name = case_dir.name
    out: dict[str, Any] = {
        "case": case_name,
        "config": config_name,
        "mode": mode,
        "label": None,
        "n_paths": 0,
        "error": None,
    }

    inj_path = case_dir / "injection.json"
    if not inj_path.exists():
        out["error"] = "missing-injection.json"
        return out
    try:
        injection_data = json.loads(inj_path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        out["error"] = f"injection-read: {exc}"
        return out

    # Apply SLO threshold scaling (process-global; safe across cases since
    # each worker stays on one config for the whole batch).
    _patch_alarm_thresholds(_slo_scale_for_config(config_name))

    try:
        loader = ParquetDataLoader(case_dir, 2)
        graph = loader.build_graph_from_parquet()
        alarm_node_names = loader.identify_alarm_nodes_v2()
        surface = SLOSurface.default()
        alarm_node_names = _filter_alarms_by_surface(list(alarm_node_names), graph, surface)
        alarm_nodes = _resolve_alarm_nodes(graph, list(alarm_node_names))
        slo_impact = _compute_slo_impact(alarm_nodes, graph, surface)

        resolver = InjectionNodeResolver(graph)
        try:
            resolved = resolver.resolve(injection_data)
        except Exception as exc:
            out["error"] = f"resolve: {exc}"
            return out

        if mode == "fault_free":
            # Sham target: random non-GT node of matching PlaceKind.
            # Deterministic seed from case_name so reruns reproduce.
            seed = int(hashlib.md5(case_name.encode()).hexdigest()[:8], 16)
            rng = random.Random(seed)
            sham_kind = resolved.start_kind
            excluded = _build_excluded_uniq_names(injection_data)
            candidates: list[int] = []
            for nid in graph._graph.nodes:  # noqa: SLF001
                node = graph.get_node_by_id(nid)
                if node is None or node.kind.value != sham_kind:
                    continue
                if node.uniq_name in excluded:
                    continue
                if graph._graph.degree(nid) == 0:  # noqa: SLF001
                    continue
                candidates.append(nid)
            if not candidates:
                out["error"] = "no-sham-candidate"
                return out
            physical_node_ids = [rng.choice(candidates)]
        else:
            physical_node_ids = []
            for inj_name in resolved.injection_nodes or []:
                n = graph.get_node_by_name(inj_name)
                if n is not None and n.id is not None:
                    physical_node_ids.append(n.id)
            if not physical_node_ids:
                out["error"] = "no-physical-nodes"
                return out

        # Traces: real or split-baseline.
        if mode == "fault_free":
            normal_full = loader.load_traces("normal")
            if normal_full.height < 2 or "time" not in normal_full.columns:
                out["error"] = "no-baseline-data"
                return out
            sorted_normal = normal_full.sort("time")
            mid = sorted_normal.height // 2
            baseline_traces = sorted_normal.slice(0, mid)
            abnormal_traces = sorted_normal.slice(mid)
            if baseline_traces.height == 0 or abnormal_traces.height == 0:
                out["error"] = "no-baseline-data"
                return out
        else:
            baseline_traces = loader.load_traces("normal")
            abnormal_traces = loader.load_traces("abnormal")

        injection_at = _earliest_abnormal_seconds(abnormal_traces)
        abnormal_window_end = _latest_abnormal_seconds(abnormal_traces)
        dispatch_trace_db_binding_adapters(graph, abnormal_traces, baseline_traces)

        ctx = AdapterContext(datapack_dir=case_dir, case_name=case_name)
        timelines = run_reasoning_ir(
            graph=graph,
            ctx=ctx,
            resolved=resolved,
            injection_at=injection_at,
            baseline_traces=baseline_traces,
            abnormal_traces=abnormal_traces,
            abnormal_window_end=abnormal_window_end,
        )
        enrich_with_inferred_edges(graph, timelines, physical_node_ids)
        try:
            ab_logs = loader.load_logs("abnormal")
            no_logs = loader.load_logs("normal")
            dispatch_log_adapters(graph, timelines, ab_logs, no_logs)
        except FileNotFoundError:
            pass

        # Rule set: builtin or admit-all.
        if config_name == "skip_topology":
            rules = _build_admit_all_rules()
        else:
            rules = get_builtin_rules()

        starting_resolver = StartingPointResolver(graph)
        injection_node_ids = starting_resolver.resolve(
            physical_node_ids=physical_node_ids,
            resolved_injection=resolved,
            rules=rules,
        )

        local_effect = _compute_local_effect(physical_node_ids, timelines, graph)

        if not slo_impact.detected:
            label, _reason = classify(local_effect, slo_impact, has_path=False)
            out["label"] = label
            return out

        delta_t = max(0, abnormal_window_end - injection_at)
        injection_window = (injection_at, injection_at + delta_t + INJECT_TIME_TOLERANCE_SECONDS)

        # Build ReasoningContext so the manifest-driven gates and
        # PathBuilder activate. Mirrors cli.run_single_case (cli.py:728-767).
        # Without this, the harness silently bypasses the entire forge-rework
        # pipeline and measures the generic 4-gate baseline.
        v_root_id: int | None = (
            injection_node_ids[0]
            if injection_node_ids
            else (physical_node_ids[0] if physical_node_ids else None)
        )
        feature_samples = extract_feature_samples(
            graph=graph,
            baseline_traces=baseline_traces,
            abnormal_traces=abnormal_traces,
            abnormal_window_start=injection_at,
            abnormal_window_end=abnormal_window_end,
            timelines=timelines,
        )
        registry = get_default_registry()
        manifest = registry.get(resolved.fault_type_name)
        reasoning_ctx = ReasoningContext(
            fault_type_name=resolved.fault_type_name,
            manifest=manifest,
            v_root_node_id=v_root_id,
            t0=injection_at,
            feature_samples=feature_samples,
            registry=registry,
            graph=graph,
        )

        if config_name == "skip_screen":
            gates = _build_skip_screen_gates()
            ctx_for_propagator = None
        else:
            gates = manifest_aware_gates(reasoning_ctx)
            ctx_for_propagator = reasoning_ctx

        propagator = FaultPropagator(
            graph=graph,
            rules=rules,
            timelines=timelines,
            max_hops=max_hops,
            injection_window=injection_window,
            gates=gates,
            reasoning_ctx=ctx_for_propagator,
        )
        result = propagator.propagate_from_injection(
            injection_node_ids=injection_node_ids,
            alarm_nodes=alarm_nodes,
        )
        has_path = bool(result.paths)
        label, _reason = classify(local_effect, slo_impact, has_path)
        out["label"] = label
        out["n_paths"] = len(result.paths)
        return out
    except Exception as exc:  # noqa: BLE001
        out["error"] = f"{type(exc).__name__}: {exc}"
        return out
