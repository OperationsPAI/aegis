"""Transition stream → per-node StateTimeline.

Merge policy (decided in #165):

- UNKNOWN seeded at ``t=-inf`` (represented as window start = earliest
  observed ``at`` in the stream; caller passes ``observation_start`` if a
  global t0 is known).
- Multiple transitions at the same ``at`` on the same node: pick by severity
  (see ``states.severity``); if two tied adapters both claim the top
  severity with different ``to_state``, earliest stream position wins (stable
  sort). Same ``to_state`` from multiple adapters merges evidence —
  ``specialization_labels`` are unioned, ``trigger`` / numeric fields are
  taken from the first.
- Between transitions on the same node the window holds the previous
  ``to_state``; the final window extends to ``observation_end`` if given,
  otherwise to the last transition's ``at`` (zero-length end marker —
  callers that need a bounded window should pass ``observation_end``).
"""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Iterable

from rcabench_platform.v3.internal.reasoning.ir.evidence import Evidence, EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.states import severity
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind


def _merge_evidence(primary: Evidence, extra: Evidence) -> Evidence:
    merged: Evidence = dict(primary)  # type: ignore[assignment]
    extra_labels = extra.get("specialization_labels")
    if extra_labels:
        existing = merged.get("specialization_labels", frozenset())
        merged["specialization_labels"] = frozenset(existing) | frozenset(extra_labels)
    for key in ("trigger_metric", "observed", "threshold"):
        if key not in merged and key in extra:
            merged[key] = extra[key]  # type: ignore[literal-required]
    return merged


def _collapse_simultaneous(group: list[Transition]) -> Transition:
    """Reduce same-(node, at) transitions to a single winning Transition."""
    winner = group[0]
    for t in group[1:]:
        if t.to_state == winner.to_state:
            winner = Transition(
                node_key=winner.node_key,
                kind=winner.kind,
                at=winner.at,
                from_state=winner.from_state,
                to_state=winner.to_state,
                trigger=winner.trigger,
                level=winner.level,
                evidence=_merge_evidence(winner.evidence, t.evidence),
            )
            continue
        if severity(t.to_state) > severity(winner.to_state):
            winner = t
    return winner


def synth_timelines(
    transitions: Iterable[Transition],
    *,
    observation_start: int | None = None,
    observation_end: int | None = None,
) -> dict[str, StateTimeline]:
    """Build per-node timelines from a stream of Transitions.

    Args:
        transitions: events emitted by one or more adapters; order not
            required (sorted internally by ``at``).
        observation_start: optional lower bound for the first UNKNOWN
            window. Defaults to the earliest transition's ``at``, which
            yields a zero-length pre-UNKNOWN window.
        observation_end: optional upper bound for the last window.
            Defaults to the last transition's ``at``.
    """
    by_node: dict[str, list[Transition]] = defaultdict(list)
    kinds: dict[str, PlaceKind] = {}
    for t in transitions:
        by_node[t.node_key].append(t)
        kinds.setdefault(t.node_key, t.kind)

    timelines: dict[str, StateTimeline] = {}
    for node_key, events in by_node.items():
        events.sort(key=lambda e: e.at)

        collapsed: list[Transition] = []
        i = 0
        while i < len(events):
            j = i
            while j < len(events) and events[j].at == events[i].at:
                j += 1
            collapsed.append(_collapse_simultaneous(events[i:j]))
            i = j

        first_at = collapsed[0].at
        last_at = collapsed[-1].at
        t0 = observation_start if observation_start is not None else first_at
        tN = observation_end if observation_end is not None else last_at

        windows: list[TimelineWindow] = []
        if t0 < first_at:
            windows.append(
                TimelineWindow(
                    start=t0,
                    end=first_at,
                    state="unknown",
                    level=EvidenceLevel.inferred,
                    trigger="init",
                    evidence={},
                )
            )

        for idx, tr in enumerate(collapsed):
            w_end = collapsed[idx + 1].at if idx + 1 < len(collapsed) else tN
            if w_end < tr.at:
                w_end = tr.at
            windows.append(
                TimelineWindow(
                    start=tr.at,
                    end=w_end,
                    state=tr.to_state,
                    level=tr.level,
                    trigger=tr.trigger,
                    evidence=tr.evidence,
                )
            )

        timelines[node_key] = StateTimeline(
            node_key=node_key,
            kind=kinds[node_key],
            windows=tuple(windows),
        )

    return timelines
