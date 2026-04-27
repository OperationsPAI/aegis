"""DriftGate: every node must show at least one non-nominal timeline window."""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import GateContext, GateResult
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath

_NON_NOMINAL_STATES: frozenset[str] = frozenset(
    {"slow", "degraded", "restarting", "erroring", "silent", "unavailable", "missing"}
)


class DriftGate:
    """Every node along the path has >=1 timeline window in a non-nominal state."""

    name = "drift"

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult:
        nodes_evidence: list[dict[str, object]] = []
        all_pass = True
        for nid in path.node_ids:
            node = ctx.graph.get_node_by_id(nid)
            observed: set[str] = set()
            if node is not None:
                tl = ctx.timelines.get(node.uniq_name)
                if tl is not None:
                    observed = {w.state for w in tl.windows}
            has_drift = bool(observed & _NON_NOMINAL_STATES)
            if not has_drift:
                all_pass = False
            nodes_evidence.append(
                {
                    "node_id": nid,
                    "has_drift": has_drift,
                    "observed_states": sorted(observed),
                }
            )

        reason = "" if all_pass else f"{sum(1 for n in nodes_evidence if not n['has_drift'])} node(s) without drift"
        return GateResult(gate_name=self.name, passed=all_pass, evidence={"nodes": nodes_evidence}, reason=reason)
