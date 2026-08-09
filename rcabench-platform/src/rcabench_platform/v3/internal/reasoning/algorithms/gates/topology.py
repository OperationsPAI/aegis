"""TopologyGate: rule-admitted invariant + per-edge rule_id evidence channel."""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import GateContext, GateResult
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath


class TopologyGate:
    """Guards the topological-rule invariant of the §7.6 4-gate pipeline.

    Invariant: every edge ``(node_i, node_{i+1})`` in a candidate path has
    at least one rule in ``ctx.rules`` whose ``(src_kind, edge_kind,
    direction, dst_kind)`` admits the edge given the per-node state
    universe and any first-hop / required-label constraints.

    Why have a gate when PathBuilder already filters?

    * PathBuilder operates with a fixed rule set baked into its constructor;
      callers who want to *re-evaluate* an existing candidate against a
      different rule set (ablation runs, sensitivity studies) need a
      checker that doesn't rebuild.
    * The gate doubles as an executable assertion of the build-time
      invariant — a future PathBuilder bug that lets a non-admitted edge
      slip through is caught here rather than silently propagating.
    * The per-edge ``admitting_rule_ids`` evidence is the canonical channel
      consumers use to learn *which* rule fired on each edge, including
      alternate rules that were also admissible. The propagator records
      only the picked rule on the path itself.

    Pass criterion: every edge has at least one admitting rule.
    """

    name = "topology"

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult:
        edges_evidence: list[dict[str, object]] = []
        all_pass = True
        for i, rule_id in enumerate(path.rule_ids):
            src_id = path.node_ids[i]
            dst_id = path.node_ids[i + 1]
            src_states = self._states_for_node(src_id, ctx)
            dst_states = self._states_for_node(dst_id, ctx)
            src_labels = self._labels_for_node(src_id, ctx)
            matches = ctx.rule_matcher.matches_edge(
                src_id,
                dst_id,
                ctx.graph,
                src_states,
                dst_states,
                is_first_hop=(i == 0),
                src_labels=src_labels,
            )
            admitting_rule_ids = sorted({r.rule_id for r in matches})
            edge_passed = bool(admitting_rule_ids)
            if not edge_passed:
                all_pass = False
            edges_evidence.append(
                {
                    "edge_index": i,
                    "src_id": src_id,
                    "dst_id": dst_id,
                    "picked_rule_id": rule_id,
                    "admitting_rule_ids": admitting_rule_ids,
                    "passed": edge_passed,
                }
            )

        n_failed = sum(1 for e in edges_evidence if not e["passed"])
        reason = "" if all_pass else f"{n_failed} edge(s) without admitting rule"
        return GateResult(gate_name=self.name, passed=all_pass, evidence={"edges": edges_evidence}, reason=reason)

    @staticmethod
    def _states_for_node(node_id: int, ctx: GateContext) -> set[str]:
        node = ctx.graph.get_node_by_id(node_id)
        if node is None:
            return set()
        tl = ctx.timelines.get(node.uniq_name)
        if tl is None:
            return set()
        return {w.state for w in tl.windows}

    @staticmethod
    def _labels_for_node(node_id: int, ctx: GateContext) -> frozenset[str]:
        node = ctx.graph.get_node_by_id(node_id)
        if node is None:
            return frozenset()
        tl = ctx.timelines.get(node.uniq_name)
        if tl is None:
            return frozenset()
        labels: set[str] = set()
        for w in tl.windows:
            ws = w.evidence.get("specialization_labels")
            if ws:
                labels.update(ws)
        return frozenset(labels)
