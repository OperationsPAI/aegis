"""Hand-off chain limit (FORGE rework §3.6).

A path may chain at most 2 hand-offs (≤ 3 fault types). The cap protects
against combinatorial blowup in cascade verification; cycles are broken
by tracking ``(node_id, fault_type_name)`` pairs.

Synthetic coverage:

* 2 hand-offs admit (depth 3).
* 3rd hand-off rejects with logged warning.
* Re-visiting a ``(node, fault_type)`` pair rejects (cycle).
"""

from __future__ import annotations

import logging

from rcabench_platform.v3.internal.reasoning.algorithms.handoff import (
    MAX_HAND_OFFS_PER_PATH,
    HandOffChain,
)


def test_two_handoffs_admit() -> None:
    chain = HandOffChain()
    chain.record_seed(1, "CPUStress")
    assert chain.record_handoff(2, "HTTPResponseAbort")
    assert chain.record_handoff(3, "MemoryStress")
    assert chain.depth == 3
    assert chain.handoff_count == 2
    assert chain.fault_types == ["CPUStress", "HTTPResponseAbort", "MemoryStress"]


def test_third_handoff_rejects_with_warning(caplog) -> None:
    chain = HandOffChain()
    chain.record_seed(1, "CPUStress")
    assert chain.record_handoff(2, "HTTPResponseAbort")
    assert chain.record_handoff(3, "MemoryStress")
    assert chain.handoff_count == MAX_HAND_OFFS_PER_PATH

    with caplog.at_level(logging.WARNING):
        admitted = chain.record_handoff(4, "PodKill")
    assert not admitted
    assert chain.depth == 3  # unchanged
    assert chain.rejected_handoffs == [(4, "PodKill")]
    assert any("hit cap" in record.message for record in caplog.records)


def test_can_take_handoff_predicate() -> None:
    chain = HandOffChain()
    chain.record_seed(1, "CPUStress")
    assert chain.can_take_handoff(2, "HTTPResponseAbort")
    chain.record_handoff(2, "HTTPResponseAbort")
    chain.record_handoff(3, "MemoryStress")
    # Cap hit.
    assert not chain.can_take_handoff(4, "PodKill")


def test_cycle_detection_breaks_handoff(caplog) -> None:
    chain = HandOffChain()
    chain.record_seed(1, "CPUStress")
    chain.record_handoff(2, "HTTPResponseAbort")

    # Trying to hand off back to (node=1, CPUStress) → cycle.
    with caplog.at_level(logging.WARNING):
        admitted = chain.record_handoff(1, "CPUStress")
    assert not admitted
    assert chain.cycle_attempts == [(1, "CPUStress")]
    assert any("cycle" in record.message for record in caplog.records)


def test_cycle_detection_does_not_consume_cap_slot() -> None:
    chain = HandOffChain()
    chain.record_seed(1, "CPUStress")
    chain.record_handoff(2, "HTTPResponseAbort")
    # First handoff used. Now attempt a cycle — should not consume the slot.
    chain.record_handoff(1, "CPUStress")  # cycle
    assert chain.handoff_count == 1
    # Still room for one more legitimate hand-off.
    assert chain.record_handoff(3, "MemoryStress")
    assert chain.handoff_count == 2


def test_max_handoffs_constant_is_two() -> None:
    """Acceptance criterion: cap is 2 hand-offs (3 fault types per path)."""
    assert MAX_HAND_OFFS_PER_PATH == 2
