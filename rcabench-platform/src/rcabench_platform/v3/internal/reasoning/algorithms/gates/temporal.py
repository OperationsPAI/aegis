"""TemporalGate: per-edge picked_onset[i+1] >= picked_onset[i] - epsilon_eff."""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import GateContext, GateResult
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.algorithms.policy import epsilon_eff_seconds
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind


class TemporalGate:
    """Each edge respects the §7.5 tolerant lower bound on downstream onset."""

    name = "temporal"

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult:
        edges_evidence: list[dict[str, object]] = []
        all_pass = True
        n_edges = len(path.edge_descs)
        for i in range(n_edges):
            src_state = path.picked_states[i]
            dst_state = path.picked_states[i + 1]
            src_onset = path.picked_state_start_times[i]
            dst_onset = path.picked_state_start_times[i + 1]
            edge_kind = _parse_edge_kind(path.edge_descs[i])
            eps = epsilon_eff_seconds(src_state, dst_state, edge_kind)
            delay = dst_onset - src_onset
            ok = dst_onset >= src_onset - eps
            if not ok:
                all_pass = False
            edges_evidence.append(
                {
                    "edge_index": i,
                    "edge_kind": edge_kind.value,
                    "src_state": src_state,
                    "dst_state": dst_state,
                    "src_onset": src_onset,
                    "dst_onset": dst_onset,
                    "delay": delay,
                    "epsilon_eff": eps,
                    "ok": ok,
                }
            )

        reason = "" if all_pass else f"{sum(1 for e in edges_evidence if not e['ok'])} edge(s) violate temporal bound"
        return GateResult(gate_name=self.name, passed=all_pass, evidence={"edges": edges_evidence}, reason=reason)


def _parse_edge_kind(edge_desc: str) -> DepKind:
    # edge_desc format: "{edge_kind.value}_{direction.value}"; direction values
    # are "FORWARD" / "BACKWARD" so split on the last "_".
    head, _, _ = edge_desc.rpartition("_")
    try:
        return DepKind(head)
    except ValueError:
        return DepKind.calls
