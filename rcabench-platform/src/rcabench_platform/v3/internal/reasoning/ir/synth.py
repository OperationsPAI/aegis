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
from rcabench_platform.v3.internal.reasoning.ir.states import intra_tier_precedence, severity
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


def _shadow_loser(winner_evidence: Evidence, loser: Transition) -> Evidence:
    """Demote ``loser`` into the winner's ``shadowed`` list; carry its labels.

    Per methodology §7.1: specialization labels (Class F) live on a separate
    axis from canonical states and survive precedence loss — the labels of a
    demoted state are unioned into the winner's ``specialization_labels``.
    The rest of the loser's evidence is appended to ``shadowed`` so rules can
    still see the lower-precedence signal was observed.
    """
    merged: Evidence = dict(winner_evidence)  # type: ignore[assignment]
    loser_labels = loser.evidence.get("specialization_labels")
    if loser_labels:
        existing = merged.get("specialization_labels", frozenset())
        merged["specialization_labels"] = frozenset(existing) | frozenset(loser_labels)
    existing_shadow = merged.get("shadowed", ())
    merged["shadowed"] = tuple(existing_shadow) + ((loser.to_state, loser.evidence),)
    return merged


def _rank(state: str) -> tuple[int, int]:
    return (severity(state), intra_tier_precedence(state))


def _collapse_simultaneous(group: list[Transition]) -> Transition:
    """Reduce same-(node, at) transitions to a single winning Transition.

    Tie-breaking lexicographically on ``(severity, intra_tier_precedence)``:
    higher severity tier wins outright; intra-tier ties resolve by direct-
    observation precedence (see ``states.intra_tier_precedence`` and
    ``docs/reasoning-feature-taxonomy.md`` §7.1). Demoted states are
    recorded under ``evidence['shadowed']`` so the lower-precedence signal
    is not silently lost.
    """
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
        if _rank(t.to_state) > _rank(winner.to_state):
            new_evidence = _shadow_loser(t.evidence, winner)
            winner = Transition(
                node_key=t.node_key,
                kind=t.kind,
                at=t.at,
                from_state=t.from_state,
                to_state=t.to_state,
                trigger=t.trigger,
                level=t.level,
                evidence=new_evidence,
            )
        else:
            winner = Transition(
                node_key=winner.node_key,
                kind=winner.kind,
                at=winner.at,
                from_state=winner.from_state,
                to_state=winner.to_state,
                trigger=winner.trigger,
                level=winner.level,
                evidence=_shadow_loser(winner.evidence, t),
            )
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
