"""DriftGate: every node must show at least one non-nominal timeline window."""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import GateContext, GateResult
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind

_NON_NOMINAL_STATES: frozenset[str] = frozenset(
    {"slow", "degraded", "restarting", "erroring", "silent", "unavailable", "missing"}
)

# Topological mediators have no per-entity timeline of their own; drift on
# them is asserted via inferred-level rollup from pods/containers, so we
# don't require a non-nominal observed state.
_STRUCTURAL_KINDS: frozenset[PlaceKind] = frozenset(
    {PlaceKind.pod, PlaceKind.replica_set, PlaceKind.deployment}
)


class DriftGate:
    """Every node along the path has >=1 timeline window in a non-nominal state."""

    name = "drift"

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult:
        nodes_evidence: list[dict[str, object]] = []
        all_pass = True
        for nid in path.node_ids:
            node = ctx.graph.get_node_by_id(nid)
            if node is not None and node.kind in _STRUCTURAL_KINDS:
                nodes_evidence.append(
                    {
                        "node_id": nid,
                        "has_drift": True,
                        "structural_exempt": True,
                        "observed_states": [],
                    }
                )
                continue
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
