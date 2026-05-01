"""Hand-off chain enforcement for manifest-driven verification.

Per SCHEMA.md "Hand-off semantics" and FORGE rework §3.6:

* A path may chain at most **2** hand-offs (≤ 3 fault types per path).
* Cycles are broken by tracking visited ``(node_id, fault_type_name)``
  pairs — the same node cannot re-enter a manifest it has already been
  verified against.
* Hitting the cap is *not an error* (the chain so far is valid); it
  simply rejects the would-be next hand-off and logs a warning so
  validation can flag pathological cascade configurations.

This module is a small bookkeeping helper consumed by manifest-aware
PathBuilder extensions and by ``test_handoff_chain`` (the synthetic
2-hand-off-admit / 3-hand-off-reject test). It is deliberately
PathBuilder-agnostic: callers feed it ``(node_id, fault_type_name)``
pairs as they decide to follow a hand-off, and it returns a hard yes /
no along with the warning text to log.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field

logger = logging.getLogger(__name__)

MAX_HAND_OFFS_PER_PATH: int = 2
"""Cap on the number of hand-offs in a single derivation chain.

Two hand-offs means at most three fault types per path (the seed Mτ + two
hand-off targets Mτ' and Mτ''). The cap exists to bound combinatorial
blowup in cascade verification — see SCHEMA.md "Hand-off semantics".
"""


@dataclass
class HandOffChain:
    """Track visited ``(node_id, fault_type_name)`` pairs along a path.

    The chain is constructed empty, populated by ``record_seed`` once at
    the start of a derivation, then by ``record_handoff`` each time the
    PathBuilder follows a hand-off. ``can_take_handoff`` answers the
    "may we follow one more hand-off?" question without mutating state.
    """

    visited: set[tuple[int, str]] = field(default_factory=set)
    fault_types: list[str] = field(default_factory=list)
    handoff_count: int = 0
    rejected_handoffs: list[tuple[int, str]] = field(default_factory=list)
    cycle_attempts: list[tuple[int, str]] = field(default_factory=list)

    def record_seed(self, root_node_id: int, fault_type_name: str) -> None:
        """Record the entry-point manifest as the chain's seed."""
        self.visited.add((root_node_id, fault_type_name))
        self.fault_types.append(fault_type_name)

    def can_take_handoff(self, node_id: int, fault_type_name: str) -> bool:
        """Return True iff a new hand-off to ``(node_id, fault_type_name)``
        is admissible without exceeding the cap or revisiting a node.
        """
        if (node_id, fault_type_name) in self.visited:
            return False
        return self.handoff_count < MAX_HAND_OFFS_PER_PATH

    def record_handoff(self, node_id: int, fault_type_name: str) -> bool:
        """Attempt to take a hand-off; return whether it was admitted.

        On rejection, logs a warning explaining whether the cap or a
        cycle-break was the reason, and records the attempt in
        ``rejected_handoffs`` / ``cycle_attempts`` so callers can audit.
        """
        if (node_id, fault_type_name) in self.visited:
            self.cycle_attempts.append((node_id, fault_type_name))
            logger.warning(
                "hand-off rejected: cycle on (node=%s, fault_type=%s)",
                node_id,
                fault_type_name,
            )
            return False
        if self.handoff_count >= MAX_HAND_OFFS_PER_PATH:
            self.rejected_handoffs.append((node_id, fault_type_name))
            logger.warning(
                "hand-off rejected: chain hit cap MAX_HAND_OFFS_PER_PATH=%d "
                "(visited=%s, attempted=%s)",
                MAX_HAND_OFFS_PER_PATH,
                sorted(self.fault_types),
                fault_type_name,
            )
            return False
        self.visited.add((node_id, fault_type_name))
        self.fault_types.append(fault_type_name)
        self.handoff_count += 1
        return True

    @property
    def depth(self) -> int:
        """Total number of fault types in the chain (seed + hand-offs)."""
        return len(self.fault_types)


__all__ = ["HandOffChain", "MAX_HAND_OFFS_PER_PATH"]
