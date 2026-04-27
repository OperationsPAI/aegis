"""InjectTimeGate: every picked onset lies within [t0, t0 + Δt + τ]."""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import GateContext, GateResult
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath

INJECT_TIME_TOLERANCE_SECONDS: int = 60
INJECT_NODE_PRE_GRACE_SECONDS: int = 5


class InjectTimeGate:
    """Picked onsets must lie within the injection window.

    The injection node itself gets a small pre-injection grace
    (``INJECT_NODE_PRE_GRACE_SECONDS``) to absorb clock skew between the
    chaos control plane and trace timestamps. Downstream nodes must lie
    fully within ``[t0, t0 + Δt + τ]``.
    """

    name = "inject_time"

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult:
        t0, t_end = ctx.injection_window
        injection_ids = ctx.injection_node_ids
        nodes_evidence: list[dict[str, object]] = []
        all_pass = True
        for i, nid in enumerate(path.node_ids):
            onset = path.picked_state_start_times[i]
            is_injection = nid in injection_ids
            lower = t0 - INJECT_NODE_PRE_GRACE_SECONDS if is_injection else t0
            in_window = lower <= onset <= t_end
            if not in_window:
                all_pass = False
            nodes_evidence.append(
                {
                    "node_index": i,
                    "node_id": nid,
                    "onset": onset,
                    "is_injection": is_injection,
                    "lower_bound": lower,
                    "upper_bound": t_end,
                    "in_window": in_window,
                }
            )

        reason = "" if all_pass else f"{sum(1 for n in nodes_evidence if not n['in_window'])} node(s) outside window"
        return GateResult(
            gate_name=self.name,
            passed=all_pass,
            evidence={
                "window_start": t0,
                "window_end": t_end,
                "tolerance_seconds": INJECT_TIME_TOLERANCE_SECONDS,
                "nodes": nodes_evidence,
            },
            reason=reason,
        )
