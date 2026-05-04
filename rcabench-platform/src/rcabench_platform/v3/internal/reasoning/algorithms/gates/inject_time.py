"""InjectTimeGate: every picked onset lies near the injection window."""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import GateContext, GateResult
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.algorithms.policy import (
    DOWNSTREAM_NODE_PRE_GRACE_SECONDS,
    INJECT_NODE_PRE_GRACE_SECONDS,
    INJECT_TIME_TOLERANCE_SECONDS,
)

# Re-exported here for backward-compatibility with callers (cli.py, gates
# package __init__) that imported these from the gate module before P0-A
# centralised the constants in policy.py.
__all__ = [
    "InjectTimeGate",
    "INJECT_TIME_TOLERANCE_SECONDS",
    "INJECT_NODE_PRE_GRACE_SECONDS",
    "DOWNSTREAM_NODE_PRE_GRACE_SECONDS",
]


class InjectTimeGate:
    """Picked onsets must lie within the injection window.

    Path nodes get a small pre-injection grace to absorb clock skew and
    timestamp bucket boundaries between the chaos control plane, traces, and
    metrics. The downstream grace is deliberately small; the upper-bound
    tolerance still covers late propagation.
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
            pre_grace = INJECT_NODE_PRE_GRACE_SECONDS if is_injection else DOWNSTREAM_NODE_PRE_GRACE_SECONDS
            lower = t0 - pre_grace
            in_window = lower <= onset <= t_end
            if not in_window:
                all_pass = False
            nodes_evidence.append(
                {
                    "node_index": i,
                    "node_id": nid,
                    "onset": onset,
                    "is_injection": is_injection,
                    "pre_grace_seconds": pre_grace,
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
