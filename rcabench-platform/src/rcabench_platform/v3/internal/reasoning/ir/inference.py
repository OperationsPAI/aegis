"""Inference rule layer — rewrites UNKNOWN windows via neighbour evidence.

Phase 1 lands the protocol + fixpoint driver with an empty rule set;
concrete rules come in Phase 3. The contract the driver enforces:

- Inference rules may rewrite ``unknown`` windows **only**.
- They may not downgrade an ``observed`` or ``structural`` window.
- The driver iterates until a round produces no rewrites, bounded by
  ``max_rounds=3`` (issue #165 decision).
"""

from __future__ import annotations

from collections.abc import Iterable
from typing import Protocol, runtime_checkable

from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow


@runtime_checkable
class InferenceRule(Protocol):
    name: str

    def apply(
        self,
        timelines: dict[str, StateTimeline],
    ) -> Iterable[tuple[str, int, TimelineWindow]]:
        """Yield (node_key, window_index, new_window) triples.

        Driver validates: ``timelines[node_key].windows[window_index].state ==
        "unknown"`` before applying, and drops the rewrite otherwise.
        """
        ...


def _apply_rewrites(
    timelines: dict[str, StateTimeline],
    rewrites: Iterable[tuple[str, int, TimelineWindow]],
) -> tuple[dict[str, StateTimeline], int]:
    per_node: dict[str, dict[int, TimelineWindow]] = {}
    for node_key, idx, new_window in rewrites:
        tl = timelines.get(node_key)
        if tl is None or idx >= len(tl.windows):
            continue
        if tl.windows[idx].state != "unknown":
            continue
        if new_window.level == EvidenceLevel.observed:
            continue
        per_node.setdefault(node_key, {})[idx] = new_window

    if not per_node:
        return timelines, 0

    new_timelines = dict(timelines)
    count = 0
    for node_key, rewrites_for_node in per_node.items():
        tl = new_timelines[node_key]
        windows = list(tl.windows)
        for idx, new_win in rewrites_for_node.items():
            windows[idx] = new_win
            count += 1
        new_timelines[node_key] = StateTimeline(
            node_key=tl.node_key,
            kind=tl.kind,
            windows=tuple(windows),
        )
    return new_timelines, count


def run_fixpoint(
    timelines: dict[str, StateTimeline],
    rules: list[InferenceRule],
    *,
    max_rounds: int = 3,
) -> dict[str, StateTimeline]:
    """Iterate inference rules until a round is quiet or max_rounds hit."""
    if not rules:
        return timelines
    current = timelines
    for _ in range(max_rounds):
        all_rewrites: list[tuple[str, int, TimelineWindow]] = []
        for rule in rules:
            all_rewrites.extend(rule.apply(current))
        current, changed = _apply_rewrites(current, all_rewrites)
        if changed == 0:
            break
    return current
