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
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath, PathBuilder
from rcabench_platform.v3.internal.reasoning.algorithms.rule_matcher import RuleMatcher
from rcabench_platform.v3.internal.reasoning.algorithms.temporal_validator import TemporalValidator
from rcabench_platform.v3.internal.reasoning.algorithms.topology_explorer import TopologyExplorer
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
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

        def edge_filter(src_id: int, dst_id: int, is_first_hop: bool) -> bool:
            src_states = self._states_for_node(src_id)
            dst_states = self._states_for_node(dst_id)
            src_labels = self._labels_for_node(src_id)
            return self.rule_matcher.edge_matches_any_rule(
                src_id, dst_id, self.graph, src_states, dst_states, is_first_hop, src_labels=src_labels
            )

        # §7.6 step 6 + §13.2 step 2.6 — bidirectional corridor + activity filter.
        # corridor       = Reach_forward(injection_set, max_hops_fwd)
        #                ∩ Reach_backward(alarm_set,    max_hops_bwd)
        # relevant_nodes = corridor ∩ (deviating_set ∪ injection_set)
        injection_set = set(injection_node_ids)

        # Reverse-orientation filter for the backward reach pass: an edge
        # (a, b) participates in backward reach from b iff (b → a) matches
        # some rule's forward propagation direction. ``is_first_hop`` is set
        # iff the neighbor we're stepping toward (which becomes the SRC of
        # the forward-propagation rule) is in the original injection_set —
        # see git blame on the previous revision for the asymmetry this fixes.
        def backward_edge_filter(src_id: int, dst_id: int, is_first_hop_unused: bool) -> bool:
            src_states = self._states_for_node(dst_id)
            dst_states = self._states_for_node(src_id)
            src_labels = self._labels_for_node(dst_id)
            is_first_hop = dst_id in injection_set
            return self.rule_matcher.edge_matches_any_rule(
                dst_id, src_id, self.graph, src_states, dst_states, is_first_hop, src_labels=src_labels
            )

        forward_edges = self.topology_explorer.find_reachable_subgraph(injection_node_ids, alarm_nodes, edge_filter)
        forward_visited: set[int] = set(injection_set)
        for s, d in forward_edges:
            forward_visited.add(s)
            forward_visited.add(d)

        backward_edges = self.topology_explorer.find_reachable_subgraph(
            list(alarm_nodes), set(injection_set), backward_edge_filter
        )
        backward_visited: set[int] = set(alarm_nodes)
        for s, d in backward_edges:
            backward_visited.add(s)
            backward_visited.add(d)

        corridor = forward_visited & backward_visited
        deviating_set = self._compute_deviating_set()
        relevant_nodes = corridor & (deviating_set | injection_set)

        subgraph_edges = [(s, d) for s, d in forward_edges if s in relevant_nodes and d in relevant_nodes]
        warnings: list[str] = []

        if not subgraph_edges:
            warning_msg = f"No reachable edges found from injection nodes {injection_node_ids}"
            warnings.append(warning_msg)
            logger.warning(f"  [WARNING] {warning_msg}")
            return PropagationResult(
                injection_node_ids=injection_node_ids,
                injection_states=["unknown"] * len(injection_node_ids),
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
                subgraph_edges=[],
                warnings=warnings,
            )

        all_topology_paths = self.topology_explorer.extract_paths(subgraph_edges, injection_node_ids, alarm_nodes)

        if not all_topology_paths:
            warning_msg = f"No paths extracted from reachable subgraph ({len(subgraph_edges)} edges available)"
            warnings.append(warning_msg)
            logger.warning(f"  [WARNING] {warning_msg}")
            return PropagationResult(
                injection_node_ids=injection_node_ids,
                injection_states=["unknown"] * len(injection_node_ids),
                paths=[],
                visited_nodes=set(),
                max_hops_reached=0,
                subgraph_edges=subgraph_edges,
                warnings=warnings,
            )

        ctx = GateContext(
            graph=self.graph,
            timelines=self.timelines,
            rules=self.rules,
            rule_matcher=self.rule_matcher,
            injection_window=self.injection_window,
            injection_node_ids=frozenset(injection_node_ids),
        )

        valid_paths: list[PropagationPath] = []
        rejected_paths: list[RejectedPath] = []
        visited_nodes: set[int] = set()
        max_hops = 0
        for node_ids in all_topology_paths:
            visited_nodes.update(node_ids)
            max_hops = max(max_hops, len(node_ids) - 1)
            admitted, gate_results, candidate = self._build_and_evaluate(node_ids, ctx)
            if admitted is not None:
                for rule_id in admitted.rules:
                    self.rule_stats.record_rule_use(rule_id)
                valid_paths.append(admitted)
            elif candidate is not None:
                rejected_paths.append(RejectedPath(node_ids=list(node_ids), gate_results=gate_results))

        if self.rule_stats.rule_counts:
            logger.info(self.rule_stats.get_summary())
            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            injection_hash = hashlib.md5(str(sorted(injection_node_ids)).encode()).hexdigest()[:8]
            stat_dir = Path("output/stat") / f"{timestamp}-{injection_hash}"
            stat_dir.mkdir(parents=True, exist_ok=True)
            stat_file = stat_dir / "rule_stats.json"
            self.rule_stats.save_to_file(stat_file)
            logger.info(f"Saved rule statistics to {stat_file}")

        return PropagationResult(
            injection_node_ids=injection_node_ids,
            injection_states=["unknown"] * len(injection_node_ids),
            paths=valid_paths,
            visited_nodes=visited_nodes,
            max_hops_reached=max_hops,
            subgraph_edges=subgraph_edges,
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
