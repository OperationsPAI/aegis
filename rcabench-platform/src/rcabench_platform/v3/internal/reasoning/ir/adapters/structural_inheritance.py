"""StructuralInheritanceAdapter — propagate infra availability down the containment hierarchy.

Closes a state-detection gap on the trace adapter. ``TraceStateAdapter`` only
rolls up service / span state from observed root-span anomalies, but in
hierarchical benchmarks (e.g. TrainTicket) the only true roots belong to the
load generator — non-loadgen services never get a service-level timeline, and
when their pods/containers are killed the trace adapter additionally lacks
baseline samples for those services so ``baseline_keys - seen_keys`` is empty
too. The result: ``container|svc`` is correctly classified ``unavailable`` by
``K8sMetricsAdapter`` while ``service|svc`` and ``span|svc::*`` have no
timeline at all, and downstream rules (``container_unavailable_to_span``,
``span_unavailable_to_caller``) cannot extract a path because their dst
states are empty.

This adapter expresses the structural invariant *if your container/pod/service
is unavailable (or silent at the request layer), the spans it would serve
cannot be served either* directly at the IR layer, before the propagator
runs. The cascade table itself lives in ``rules/structural_rules.json`` —
this adapter is a generic walker over those rules. It walks the containment
hierarchy implied by graph edges (``runs``, ``routes_to``, ``includes``) and
emits ``EvidenceLevel.inferred`` ``Transition`` events for the derived
nodes.

Each structural rule asserts a class-A topology axiom — violating one means
the topology graph itself is wrong, so the 4-gate does not falsify them.

The adapter is a regular ``StateAdapter`` but takes ``prior_timelines`` at
construction so it can avoid emitting transitions that would be redundant
(observed worst-or-equal already present at the cascade onset). The
pipeline driver runs the observation adapters first, synthesises a phase-1
timeline dict, then constructs this adapter and re-synthesises with the
combined transition stream.
"""

from __future__ import annotations

from collections.abc import Iterable

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.states import severity as state_severity
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_structural_rules
from rcabench_platform.v3.internal.reasoning.rules.schema import (
    PropagationDirection,
    StructuralRule,
)


def _is_weaker_or_equal(prior_state: str, candidate: str) -> bool:
    """Whether ``prior_state`` is at least as severe as ``candidate``.

    Severity ranks come from the canonical taxonomy table in
    :mod:`rcabench_platform.v3.internal.reasoning.ir.states` (§11.1).
    """
    return state_severity(prior_state) >= state_severity(candidate)


def _state_at(timeline: StateTimeline | None, at: int) -> str:
    """State of ``timeline`` at instant ``at`` (or ``healthy`` if uncovered).

    Used by the redundancy check in :meth:`_maybe_emit` so that a prior with
    a *late* worse-or-equal window does not suppress a structural claim at
    an *earlier* time when the prior is still healthy. The original
    ``last_state`` view (using ``windows[-1].state``) is too coarse for the
    erroring/slow cascades — pods typically reach ``degraded`` at +90s via
    k8s metric lag, which would otherwise mask our injection-time pod cascade.
    """
    if timeline is None:
        return "healthy"
    state = timeline.state_at(at)
    return state if state is not None else "healthy"


class StructuralInheritanceAdapter:
    """Emit inferred transitions on derived nodes per ``structural_rules.json``.

    The cascade table is purely declarative — see
    ``rules/structural_rules.json`` for the
    ``(src_kind, src_state) -> [(via_edge, derived_kind, derived_state), ...]``
    mapping. This adapter is a generic walker.
    """

    name = "structural_inheritance"

    def __init__(
        self,
        *,
        graph: HyperGraph,
        prior_timelines: dict[str, StateTimeline],
        rules: list[StructuralRule] | None = None,
    ) -> None:
        self._graph = graph
        self._prior_timelines = prior_timelines
        self._rules = rules if rules is not None else get_builtin_structural_rules()
        self._rule_index: dict[tuple[PlaceKind, str], StructuralRule] = {
            (r.src_kind, r.src_state): r for r in self._rules
        }
        self._inherit_kinds: frozenset[PlaceKind] = frozenset(r.src_kind for r in self._rules)
        self._inherit_states: frozenset[str] = frozenset(r.src_state for r in self._rules)

    def emit(self, ctx: AdapterContext) -> Iterable[Transition]:
        return list(self._emit_all())

    def _emit_all(self) -> Iterable[Transition]:
        # Track derived-node last-emitted state so we collapse contiguous source
        # windows into a single transition (mirrors the other adapters' shape).
        derived_last: dict[str, str] = {}

        for source_node_key, timeline in self._prior_timelines.items():
            if timeline.kind not in self._inherit_kinds:
                continue
            for window in timeline.windows:
                if window.state not in self._inherit_states:
                    continue
                rule = self._rule_index.get((timeline.kind, window.state))
                if rule is None:
                    continue
                yield from self._walk_rule(
                    rule=rule,
                    source_node_key=source_node_key,
                    at=window.start,
                    derived_last=derived_last,
                )

    def _walk_rule(
        self,
        *,
        rule: StructuralRule,
        source_node_key: str,
        at: int,
        derived_last: dict[str, str],
    ) -> Iterable[Transition]:
        source_node = self._graph.get_node_by_name(source_node_key)
        if source_node is None or source_node.id is None:
            return
        current_node_ids: list[int] = [source_node.id]
        for step in rule.derivations:
            next_node_ids: list[int] = []
            for cur_id in current_node_ids:
                for next_id in self._traverse(cur_id, step.via.edge_kind, step.via.direction):
                    next_node = self._graph.get_node_by_id(next_id)
                    if next_node is None or next_node.kind != step.derived_kind:
                        continue
                    next_node_ids.append(next_id)
                    if step.derived_state is None:
                        continue
                    yield from self._maybe_emit(
                        derived_node_key=next_node.uniq_name,
                        derived_kind=next_node.kind,
                        derived_state=step.derived_state,
                        at=at,
                        source_node_key=source_node_key,
                        source_state=rule.src_state,
                        derived_last=derived_last,
                    )
            current_node_ids = next_node_ids
            if not current_node_ids:
                return

    def _traverse(self, node_id: int, edge_kind: DepKind, direction: PropagationDirection) -> Iterable[int]:
        if direction == PropagationDirection.FORWARD:
            for _src, dst, ek in self._graph._graph.out_edges(node_id, keys=True):  # type: ignore[call-arg]
                if ek == edge_kind:
                    yield dst
        else:
            for src, _dst, ek in self._graph._graph.in_edges(node_id, keys=True):  # type: ignore[call-arg]
                if ek == edge_kind:
                    yield src

    def _maybe_emit(
        self,
        *,
        derived_node_key: str,
        derived_kind: PlaceKind,
        derived_state: str,
        at: int,
        source_node_key: str,
        source_state: str,
        derived_last: dict[str, str],
    ) -> Iterable[Transition]:
        prior = self._prior_timelines.get(derived_node_key)
        # Compare at the source's onset time — a prior with a late worse-or-
        # equal window must not suppress a structural claim at an earlier
        # time when the prior is still healthy.
        prior_state_at_t = _state_at(prior, at)
        if prior is not None and _is_weaker_or_equal(prior_state_at_t, derived_state):
            return
        last_emitted = derived_last.get(derived_node_key)
        if last_emitted == derived_state:
            return
        from_state = last_emitted if last_emitted is not None else prior_state_at_t
        yield Transition(
            node_key=derived_node_key,
            kind=derived_kind,
            at=at,
            from_state=from_state,
            to_state=derived_state,
            trigger="structural_inheritance",
            level=EvidenceLevel.inferred,
            evidence={
                "trigger_metric": "structural_inheritance",
                "specialization_labels": frozenset(
                    {f"inherited_from:{source_node_key}", f"source_state:{source_state}"}
                ),
            },
        )
        derived_last[derived_node_key] = derived_state


__all__ = ["StructuralInheritanceAdapter"]
