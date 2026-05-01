"""TemporalGate: per-edge picked_onset[i+1] >= picked_onset[i] - clock_skew.

Phase 3 (FORGE rework, §3.3) simplifies the per-edge predicate:

* Forward propagation delay is governed by ``edge_epsilon_seconds(edge_kind)``
  alone (5s for synchronous channels, 10s for ``routes_to``, 60s for
  lifecycle channels) — onset-resolution noise compensation is now
  absorbed into the manifest's magnitude bands.
* Reversed-order tolerance is a fixed 1s clock-skew allowance
  (``REVERSED_ORDER_TOLERANCE_SECONDS``) — independent of edge kind.

The gate enforces the looser of the two: ``dst_onset >= src_onset - 1``.
That is, a downstream onset may slip up to 1s before its upstream onset
without rejection (NTP-bounded clock skew between control plane and
trace collectors). Forward delay is bounded by the topology + temporal
admission window in ``find_admissible_window``; the gate is the
canonical post-build assertion that the picked window respects causal
ordering.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import GateContext, GateResult
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.algorithms.policy import (
    REVERSED_ORDER_TOLERANCE_SECONDS,
    edge_epsilon_seconds,
)
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind


class TemporalGate:
    """Each edge respects ``dst_onset >= src_onset - 1`` (clock-skew tolerance).

    Forward delay is left to ``find_admissible_window``; the gate's job is
    to guard against picked-window selections that violate causal
    ordering by more than a clock-skew tick.
    """

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
            forward_budget = edge_epsilon_seconds(edge_kind)
            delay = dst_onset - src_onset
            ok = dst_onset >= src_onset - REVERSED_ORDER_TOLERANCE_SECONDS
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
                    "edge_epsilon": forward_budget,
                    "reversed_order_tolerance": REVERSED_ORDER_TOLERANCE_SECONDS,
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
