"""Slim FaultPropagator coordinator over canonical-state IR timelines.

The propagator coordinates topology exploration (TopologyExplorer), rule
matching (RuleMatcher) and temporal validation (TemporalValidator) on top
of the per-node ``StateTimeline``s produced by ``run_reasoning_ir``.

Per-path admission is delegated to a 4-gate defense-in-depth pipeline
(``algorithms.gates``): TopologyGate, DriftGate, TemporalGate,
InjectTimeGate. ``PathBuilder`` is a pure data producer; gates own
all validity decisions.
"""

from __future__ import annotations

import hashlib
import json
import logging
from collections import Counter
from datetime import datetime
from pathlib import Path

from rcabench_platform.v3.internal.reasoning.algorithms.gates import (
    Gate,
    GateContext,
    default_gates,
    evaluate_path,
)
from rcabench_platform.v3.internal.reasoning.algorithms.gates.manifest_entry import (
    ManifestEntryGate,
)
from rcabench_platform.v3.internal.reasoning.algorithms.manifest_path_builder import (
    ManifestAwarePathBuilder,
)
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath, PathBuilder
from rcabench_platform.v3.internal.reasoning.algorithms.rule_matcher import RuleMatcher
from rcabench_platform.v3.internal.reasoning.algorithms.temporal_validator import TemporalValidator
from rcabench_platform.v3.internal.reasoning.algorithms.topology_explorer import TopologyExplorer
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph, is_structural_mediator
from rcabench_platform.v3.internal.reasoning.models.propagation import (
    PropagationPath,
    PropagationResult,
    RejectedPath,
)
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationRule

logger = logging.getLogger(__name__)


class RuleUsageStats:
    def __init__(self) -> None:
        self.rule_counts: Counter[str] = Counter()

    def record_rule_use(self, rule_id: str) -> None:
        self.rule_counts[rule_id] += 1

    def save_to_file(self, filepath: Path | str) -> None:
        filepath = Path(filepath)
        stats = {
            "total_applications": sum(self.rule_counts.values()),
            "unique_rules_used": len(self.rule_counts),
            "rule_usage": dict(self.rule_counts.most_common()),
        }
        filepath.write_text(json.dumps(stats, indent=2))

    def get_summary(self) -> str:
        lines = ["Rule Usage Statistics:"]
        for rule_id, count in self.rule_counts.most_common():
            lines.append(f"  {rule_id}: {count}")
        return "\n".join(lines)


class FaultPropagator:
    """Bidirectional fault propagation analyzer over canonical-state timelines."""

    def __init__(
        self,
        graph: HyperGraph,
        rules: list[PropagationRule],
        timelines: dict[str, StateTimeline],
        max_hops: int = 5,
        injection_window: tuple[int, int] | None = None,
        gates: list[Gate] | None = None,
        reasoning_ctx: ReasoningContext | None = None,
    ) -> None:
        """Initialize the fault propagator.

        Args:
            graph: HyperGraph topology (nodes + edges only; no state).
            rules: Propagation rules to evaluate paths against.
            timelines: Per-node ``StateTimeline`` from ``run_reasoning_ir``,
                keyed by ``node.uniq_name``.
            max_hops: Maximum propagation hops (default 5).
            injection_window: ``(t0, t0 + Δt + τ)`` window for InjectTimeGate.
                When ``None`` the gate is given an open-ended window so it
                effectively passes — used for legacy callers that have not
                been migrated yet.
            gates: Override the gate set; default is the standard 4-gate set.
        """
        self.graph = graph
        self.rules = rules
        self.timelines = timelines
        self.max_hops = max_hops
        self.injection_window = injection_window if injection_window is not None else (0, 2**62)
        self.gates: list[Gate] = gates if gates is not None else default_gates()
        self.reasoning_ctx = reasoning_ctx

        self.rule_matcher = RuleMatcher(rules)
        self.rule_index = self.rule_matcher.rule_index
        self.topology_explorer = TopologyExplorer(graph, max_hops)
        self.temporal_validator = TemporalValidator(timelines)
        self.path_builder = PathBuilder(
            graph=graph,
            rules=rules,
            timelines=timelines,
            rule_matcher=self.rule_matcher,
            temporal_validator=self.temporal_validator,
        )
        self.rule_stats = RuleUsageStats()

    def propagate_from_injection(
        self,
        injection_node_ids: list[int],
        alarm_nodes: set[int],
    ) -> PropagationResult:
        for injection_node_id in injection_node_ids:
            if self.graph.get_node_by_id(injection_node_id) is None:
                raise ValueError(f"Injection node {injection_node_id} not found in graph")

        # Manifest-only: every chaos-tool fault type in the catalog has a
        # registered manifest (see ``models/fault_seed.FAULT_TYPE_TO_SEED_TIER``
        # vs ``manifests/fault_types/*.yaml``), so we always route through
        # the manifest-driven path builder. Cases lacking a manifest /
        # v_root return an empty result with a clear warning instead of
        # silently producing garbage from rule-only propagation.
        if (
            self.reasoning_ctx is None
            or self.reasoning_ctx.manifest is None
            or self.reasoning_ctx.v_root_node_id is None
        ):
            ft = self.reasoning_ctx.fault_type_name if self.reasoning_ctx is not None else "<no_ctx>"
            warning_msg = (
                f"manifest-only propagator: skipping fault_type={ft} — no manifest registered or no v_root resolved"
            )
            logger.warning(f"  [WARNING] {warning_msg}")
            return PropagationResult(
                injection_node_ids=injection_node_ids,
                injection_states=["unknown"] * len(injection_node_ids),
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
                subgraph_edges=[],
                warnings=[warning_msg],
                rejected_paths=[],
            )

        return self._propagate_manifest_driven(
            injection_node_ids=injection_node_ids,
            alarm_nodes=alarm_nodes,
        )

    def _propagate_manifest_driven(
        self,
        injection_node_ids: list[int],
        alarm_nodes: set[int],
    ) -> PropagationResult:
        """Manifest-driven expansion path (Phase 5).

        Replaces the generic ``corridor → extract_paths → PathBuilder
        → gate post-filter`` pipeline with layer-by-layer expansion
        rooted at ``v_root``. The ``ManifestEntryGate`` runs once on
        ``v_root`` before any expansion; if it fails, no paths are
        produced (the entry signature short-circuits the whole case).
        Otherwise the per-edge gates (TemporalGate, InjectTimeGate,
        and a defensive ManifestLayerGate when present) run on each
        materialised :class:`CandidatePath`.
        """
        rctx = self.reasoning_ctx
        assert rctx is not None and rctx.manifest is not None  # caller guarded
        primary_v_root = rctx.v_root_node_id
        assert primary_v_root is not None  # caller guarded

        warnings: list[str] = []

        # Candidate seed set. Manifests carrying ``multi_v_root: true``
        # (network fault types: chaos-mesh networkchaos affects the
        # edge BETWEEN two services, so the resolver returns both
        # endpoints) cascade from every injection node independently;
        # this lets the path-builder discover the affected call set
        # even when the captured trace graph is missing the direct
        # src→tgt edge (e.g., partition cut all relevant calls before
        # the abnormal trace window started). Single-root manifests
        # (everything else) cascade from ``primary_v_root`` only — if
        # the resolver returned a list the extra entries are
        # disambiguation candidates, not parallel cascade roots.
        if rctx.manifest.multi_v_root:
            candidate_v_roots = list(injection_node_ids) or [primary_v_root]
        else:
            candidate_v_roots = [primary_v_root]

        # Entry-signature short-circuit. The gate is per-injection,
        # ignores the path. Evaluate once per v_root candidate; pass
        # if ANY satisfies the signature — for network the partition
        # signal may be observable on either source or target side.
        ctx = GateContext(
            graph=self.graph,
            timelines=self.timelines,
            rules=self.rules,
            rule_matcher=self.rule_matcher,
            injection_window=self.injection_window,
            injection_node_ids=frozenset(injection_node_ids),
        )
        entry_passed = False
        entry_reasons: list[str] = []
        winning_v_root = primary_v_root
        # ReasoningContext is frozen; iterate by building a per-v_root
        # variant via dataclasses.replace and re-binding the entry gate.
        from dataclasses import replace as _dc_replace

        for v_root in candidate_v_roots:
            v_rctx = _dc_replace(rctx, v_root_node_id=v_root)
            v_gate = ManifestEntryGate(v_rctx)
            empty_path = CandidatePath(
                node_ids=[v_root],
                all_states=[["unknown"]],
                picked_states=["unknown"],
                picked_state_start_times=[rctx.t0 if rctx.t0 is not None else 0],
                edge_descs=[],
                rule_ids=[],
                rule_confidences=[],
                propagation_delays=[],
            )
            r = v_gate.evaluate(empty_path, ctx)
            if r.passed:
                entry_passed = True
                winning_v_root = v_root
                break
            entry_reasons.append(f"v_root={v_root}: {r.reason}")
        if not entry_passed:
            joined = "; ".join(entry_reasons)
            warnings.append(f"manifest entry signature failed: {joined}")
            logger.info(
                "[manifest-driven] entry signature failed for fault_type=%s candidates=%s: %s",
                rctx.manifest.fault_type_name,
                candidate_v_roots,
                joined,
            )
            return PropagationResult(
                injection_node_ids=injection_node_ids,
                injection_states=["unknown"] * len(injection_node_ids),
                paths=[],
                visited_nodes=set(candidate_v_roots),
                max_hops_reached=0,
                subgraph_edges=[],
                warnings=warnings,
                rejected_paths=[],
            )

        # Build paths via the manifest-driven expander, seeded from
        # every candidate v_root.
        builder = ManifestAwarePathBuilder(
            graph=self.graph,
            timelines=self.timelines,
            temporal_validator=self.temporal_validator,
            reasoning_ctx=rctx,
        )
        build = builder.build_all(candidate_v_roots)

        if build.rejected_handoffs:
            for nid, ft in build.rejected_handoffs:
                warnings.append(f"hand-off cap/cycle hit at node={nid} target={ft}")

        valid_paths: list[PropagationPath] = []
        rejected_paths: list[RejectedPath] = []

        # Per-path gates: TemporalGate / InjectTimeGate / defensive
        # ManifestLayerGate (entry gate already ran above; skip it
        # here so we don't pay for it once per path). We honour any
        # gate the caller injected via ``self.gates`` other than the
        # entry gate.
        per_path_gates = [g for g in self.gates if not isinstance(g, ManifestEntryGate)]

        # Mirror the generic-branch contract: ``PropagationResult.paths``
        # are paths from the injection seed to an alarm node. The
        # ``ManifestAwarePathBuilder`` emits a candidate at every layer
        # admit (so layer-1 / layer-2 / layer-3 leaves all surface), but
        # those intermediate emissions are not "the propagation chain"
        # — only the ones whose terminal lands on a node in
        # ``alarm_nodes`` are. The generic branch enforces this through
        # ``TopologyExplorer.extract_paths(..., alarm_nodes)`` (paths
        # "terminate strictly at alarm nodes"); the manifest-driven
        # branch must enforce the same contract or downstream consumers
        # (causal_graph, hop-distance metrics, evaluators) read garbage.
        for candidate in build.paths:
            if alarm_nodes and (not candidate.node_ids or candidate.node_ids[-1] not in alarm_nodes):
                continue
            gate_results = evaluate_path(candidate, ctx, per_path_gates)
            if all(g.passed for g in gate_results):
                confidence = 1.0
                for c in candidate.rule_confidences:
                    confidence *= c
                valid_paths.append(
                    PropagationPath(
                        nodes=list(candidate.node_ids),
                        states=list(candidate.all_states),
                        edges=list(candidate.edge_descs),
                        rules=list(candidate.rule_ids),
                        confidence=confidence if candidate.rule_confidences else 1.0,
                        state_start_times=list(candidate.picked_state_start_times),
                        propagation_delays=list(candidate.propagation_delays),
                        gate_results=gate_results,
                    )
                )
                for rule_id in candidate.rule_ids:
                    self.rule_stats.record_rule_use(rule_id)
            else:
                rejected_paths.append(RejectedPath(node_ids=list(candidate.node_ids), gate_results=gate_results))

        logger.debug(
            "[manifest-driven] fault_type=%s v_root=%d: built=%d valid=%d rejected=%d max_hops=%d",
            rctx.manifest.fault_type_name,
            winning_v_root,
            len(build.paths),
            len(valid_paths),
            len(rejected_paths),
            build.max_hops_reached,
        )

        return PropagationResult(
            injection_node_ids=injection_node_ids,
            injection_states=["unknown"] * len(injection_node_ids),
            paths=valid_paths,
            visited_nodes=build.visited_nodes,
            max_hops_reached=build.max_hops_reached,
            subgraph_edges=[],
            warnings=warnings,
            rejected_paths=rejected_paths,
        )

    def _build_and_evaluate(
        self, node_ids: list[int], ctx: GateContext
    ) -> tuple[PropagationPath | None, list, CandidatePath | None]:
        candidate = self.path_builder.build(node_ids)
        if candidate is None:
            return None, [], None
        gate_results = evaluate_path(candidate, ctx, self.gates)
        if all(g.passed for g in gate_results):
            confidence = 1.0
            for c in candidate.rule_confidences:
                confidence *= c
            path = PropagationPath(
                nodes=list(candidate.node_ids),
                states=list(candidate.all_states),
                edges=list(candidate.edge_descs),
                rules=list(candidate.rule_ids),
                confidence=confidence if candidate.rule_confidences else 1.0,
                state_start_times=[t for t in candidate.picked_state_start_times],
                propagation_delays=list(candidate.propagation_delays),
                gate_results=gate_results,
            )
            return path, gate_results, candidate
        return None, gate_results, candidate

    def _timeline_for_node(self, node_id: int) -> StateTimeline | None:
        node = self.graph.get_node_by_id(node_id)
        if node is None:
            return None
        return self.timelines.get(node.uniq_name)

    def _states_for_node(self, node_id: int) -> set[str]:
        tl = self._timeline_for_node(node_id)
        if tl is None:
            return set()
        return {w.state for w in tl.windows}

    def _compute_deviating_set(self) -> set[int]:
        """Nodes whose timeline has ever been in a non-{HEALTHY, UNKNOWN}
        state during the abnormal window, OR whose kind is a structural
        mediator (pod / replica_set / deployment, see
        :func:`models.graph.is_structural_mediator`).

        Per §7.4, used by the activity filter in
        :meth:`propagate_from_injection` to restrict path search to nodes
        that actually exhibit anomalous behavior. Treating structural
        mediators as always-relevant matches the §7.4 invariant 1 spirit
        — they exist in the graph because they are part of the cascade
        structure, not because traces saw them.
        """
        deviating: set[int] = set()
        nominal = {"healthy", "unknown"}
        for node_id in self.graph._graph.nodes:
            node = self.graph.get_node_by_id(node_id)
            if node is not None and is_structural_mediator(node.kind):
                deviating.add(node_id)
                continue
            tl = self._timeline_for_node(node_id)
            if tl is None:
                continue
            for window in tl.windows:
                if window.state not in nominal:
                    deviating.add(node_id)
                    break
        return deviating

    def _labels_for_node(self, node_id: int) -> frozenset[str]:
        """Aggregate every specialization label ever observed on the node.

        Phase 4 of #163: rules with non-empty ``required_labels`` gate on
        these. Aggregating across the whole timeline (rather than picking
        a specific window) mirrors :meth:`StateTimeline.ever_carries`.
        """
        tl = self._timeline_for_node(node_id)
        if tl is None:
            return frozenset()
        labels: set[str] = set()
        for w in tl.windows:
            ws = w.evidence.get("specialization_labels")
            if ws:
                labels.update(ws)
        return frozenset(labels)
