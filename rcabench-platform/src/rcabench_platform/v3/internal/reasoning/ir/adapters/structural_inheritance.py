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
runs. It walks the containment hierarchy implied by graph edges (``runs``,
``routes_to``, ``includes``) and emits ``EvidenceLevel.inferred``
``Transition`` events for the derived nodes that are weaker (or equal) in
severity than what the upstream adapters already established. It inherits
the **availability** axis (``unavailable`` / ``degraded``) and — for
``service`` only — the Class E **silent** signal down to spans (mirror of
the unavailable→missing rule; pod/container do not carry SILENT per
taxonomy §11.1). Slow / erroring are causal claims that the propagator +
rules should derive, not structural inheritance.

The adapter is a regular ``StateAdapter`` but takes ``prior_timelines`` at
construction so it can avoid emitting transitions that would be redundant
(observed worst-or-equal already present) or contradictory (observed
healthy-after-recovery later). The pipeline driver runs the observation
adapters first, synthesises a phase-1 timeline dict, then constructs this
adapter and re-synthesises with the combined transition stream.
"""

from __future__ import annotations

from collections.abc import Iterable

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, HyperGraph, PlaceKind

# Infrastructure-level states that trigger structural inheritance. Erroring and
# slow on a container/pod manifest at the owning service the moment the fault
# starts (the service has no other backend to route to) — without this cascade
# the application-rooted rules ``container_erroring_to_span`` and
# ``container_slow_to_span`` cannot find an intermediate pod/service window at
# the fault onset and the path drops out at temporal admission. ``silent`` is
# included because a service with no observable request flow cannot carry
# observed traces on its spans either; per §11.1 only request-layer kinds
# (service, span) carry SILENT, so it only fires for ``source_kind == service``.
_INHERIT_SOURCE_STATES = frozenset({"unavailable", "degraded", "erroring", "slow", "silent"})

# Severity ranks for the availability axis only. Mirrors the canonical severity
# table in ir/states.py (silent ties with erroring at tier 4 per §11.1). Kept
# local so this adapter does not couple to states the propagator might reorder
# later. ``missing`` and ``unavailable`` tie at the top — both mean "the node
# is gone".
_AVAILABILITY_SEVERITY: dict[str, int] = {
    "unknown": 0,
    "healthy": 1,
    "slow": 2,
    "degraded": 3,
    "restarting": 3,
    "erroring": 4,
    "silent": 4,
    "unavailable": 5,
    "missing": 5,
}


def _is_weaker_or_equal(prior_state: str, candidate: str) -> bool:
    return _AVAILABILITY_SEVERITY.get(prior_state, 0) >= _AVAILABILITY_SEVERITY.get(candidate, 0)


def _last_state(timeline: StateTimeline | None) -> str:
    if timeline is None or not timeline.windows:
        return "healthy"
    return timeline.windows[-1].state


def _state_at(timeline: StateTimeline | None, at: int) -> str:
    """State of ``timeline`` at instant ``at`` (or ``healthy`` if uncovered).

    Used by the redundancy check in :meth:`_maybe_emit` so that a prior with
    a *late* worse-or-equal window does not suppress a structural claim at
    an *earlier* time when the prior is still healthy. The original
    ``_last_state`` view is too coarse for the erroring/slow cascades — pods
    typically reach ``degraded`` at +90s via k8s metric lag, which would
    otherwise mask our injection-time pod cascade.
    """
    if timeline is None:
        return "healthy"
    state = timeline.state_at(at)
    return state if state is not None else "healthy"


class StructuralInheritanceAdapter:
    """Emit inferred transitions on derived nodes when infra is unavailable/degraded.

    Inheritance map (derived state strictly within the containment edges'
    ``possible_dst_states`` so propagator-side rule shapes stay consistent):

    - ``container.unavailable`` -> parent ``pod.degraded``
                                -> ``service.unavailable``
                                -> every ``span|service::*``: ``missing``
    - ``container.degraded``    -> ``service.degraded``
                                (spans NOT inherited; degraded infra does not
                                imply spans degrade — that should come from
                                the trace adapter as an observable)
    - ``container.erroring``    -> parent ``pod.erroring``
                                -> ``service.erroring``
                                (spans NOT inherited; the application-rooted
                                rule ``container_erroring_to_span`` walks the
                                hop sequence itself, this cascade only ensures
                                pod and service have an intermediate window
                                at the fault onset.)
    - ``container.slow``        -> parent ``pod.degraded``
                                -> ``service.slow``
                                (pod has no SLOW state per taxonomy §11.1, so
                                latency at the app layer projects as DEGRADED
                                at pod level. Spans NOT inherited; same
                                reasoning as the erroring cascade.)
    - ``pod.unavailable``       -> ``service.unavailable``
                                -> every ``span|service::*``: ``missing``
    - ``pod.degraded``          -> ``service.degraded``
    - ``service.unavailable``   -> every ``span|service::*``: ``missing``
    - ``service.silent``        -> every ``span|service::*``: ``silent``
                                (Class E mirror of the unavailable→missing rule;
                                spans of a silent service cannot carry observed
                                traces in this case.)
    """

    name = "structural_inheritance"

    def __init__(
        self,
        *,
        graph: HyperGraph,
        prior_timelines: dict[str, StateTimeline],
    ) -> None:
        self._graph = graph
        self._prior_timelines = prior_timelines

    def emit(self, ctx: AdapterContext) -> Iterable[Transition]:
        return list(self._emit_all())

    def _emit_all(self) -> Iterable[Transition]:
        # Track derived-node last-emitted state so we collapse contiguous source
        # windows into a single transition (mirrors the other adapters' shape).
        derived_last: dict[str, str] = {}

        for source_node_key, timeline in self._prior_timelines.items():
            if timeline.kind not in (PlaceKind.container, PlaceKind.pod, PlaceKind.service):
                continue
            for window in timeline.windows:
                if window.state not in _INHERIT_SOURCE_STATES:
                    continue
                yield from self._emit_inheritance(
                    source_node_key=source_node_key,
                    source_kind=timeline.kind,
                    source_state=window.state,
                    at=window.start,
                    derived_last=derived_last,
                )

    def _emit_inheritance(
        self,
        *,
        source_node_key: str,
        source_kind: PlaceKind,
        source_state: str,
        at: int,
        derived_last: dict[str, str],
    ) -> Iterable[Transition]:
        if source_kind == PlaceKind.container:
            yield from self._from_container(source_node_key, source_state, at, derived_last)
        elif source_kind == PlaceKind.pod:
            yield from self._from_pod(source_node_key, source_state, at, derived_last)
        elif source_kind == PlaceKind.service:
            if source_state == "unavailable":
                yield from self._propagate_to_spans(source_node_key, source_state, at, derived_last)
            elif source_state == "silent":
                yield from self._propagate_to_spans(
                    source_node_key,
                    source_state,
                    at,
                    derived_last,
                    derived_span_state="silent",
                )

    def _from_container(
        self,
        container_key: str,
        source_state: str,
        at: int,
        derived_last: dict[str, str],
    ) -> Iterable[Transition]:
        container_node = self._graph.get_node_by_name(container_key)
        if container_node is None or container_node.id is None:
            return

        pod_ids = [
            src_id
            for src_id, _dst_id, edge_key in self._graph._graph.in_edges(container_node.id, keys=True)  # type: ignore[call-arg]
            if edge_key == DepKind.runs
        ]

        if source_state == "unavailable":
            for pod_id in pod_ids:
                pod_node = self._graph.get_node_by_id(pod_id)
                yield from self._maybe_emit(
                    derived_node_key=pod_node.uniq_name,
                    derived_kind=PlaceKind.pod,
                    derived_state="degraded",
                    at=at,
                    source_node_key=container_key,
                    source_state=source_state,
                    derived_last=derived_last,
                )
                yield from self._propagate_pod_to_service_and_spans(
                    pod_node_id=pod_id,
                    derived_service_state="unavailable",
                    propagate_spans=True,
                    at=at,
                    source_node_key=container_key,
                    source_state=source_state,
                    derived_last=derived_last,
                )
        elif source_state == "degraded":
            for pod_id in pod_ids:
                yield from self._propagate_pod_to_service_and_spans(
                    pod_node_id=pod_id,
                    derived_service_state="degraded",
                    propagate_spans=False,
                    at=at,
                    source_node_key=container_key,
                    source_state=source_state,
                    derived_last=derived_last,
                )
        elif source_state == "erroring":
            for pod_id in pod_ids:
                pod_node = self._graph.get_node_by_id(pod_id)
                yield from self._maybe_emit(
                    derived_node_key=pod_node.uniq_name,
                    derived_kind=PlaceKind.pod,
                    derived_state="erroring",
                    at=at,
                    source_node_key=container_key,
                    source_state=source_state,
                    derived_last=derived_last,
                )
                yield from self._propagate_pod_to_service_and_spans(
                    pod_node_id=pod_id,
                    derived_service_state="erroring",
                    propagate_spans=False,
                    at=at,
                    source_node_key=container_key,
                    source_state=source_state,
                    derived_last=derived_last,
                )
        elif source_state == "slow":
            # Pod has no SLOW state (§11.1), so app-layer slowness projects as
            # DEGRADED at the pod level. Service does carry SLOW.
            for pod_id in pod_ids:
                pod_node = self._graph.get_node_by_id(pod_id)
                yield from self._maybe_emit(
                    derived_node_key=pod_node.uniq_name,
                    derived_kind=PlaceKind.pod,
                    derived_state="degraded",
                    at=at,
                    source_node_key=container_key,
                    source_state=source_state,
                    derived_last=derived_last,
                )
                yield from self._propagate_pod_to_service_and_spans(
                    pod_node_id=pod_id,
                    derived_service_state="slow",
                    propagate_spans=False,
                    at=at,
                    source_node_key=container_key,
                    source_state=source_state,
                    derived_last=derived_last,
                )

    def _from_pod(
        self,
        pod_key: str,
        source_state: str,
        at: int,
        derived_last: dict[str, str],
    ) -> Iterable[Transition]:
        pod_node = self._graph.get_node_by_name(pod_key)
        if pod_node is None or pod_node.id is None:
            return
        if source_state == "unavailable":
            yield from self._propagate_pod_to_service_and_spans(
                pod_node_id=pod_node.id,
                derived_service_state="unavailable",
                propagate_spans=True,
                at=at,
                source_node_key=pod_key,
                source_state=source_state,
                derived_last=derived_last,
            )
        elif source_state == "degraded":
            yield from self._propagate_pod_to_service_and_spans(
                pod_node_id=pod_node.id,
                derived_service_state="degraded",
                propagate_spans=False,
                at=at,
                source_node_key=pod_key,
                source_state=source_state,
                derived_last=derived_last,
            )

    def _propagate_pod_to_service_and_spans(
        self,
        *,
        pod_node_id: int,
        derived_service_state: str,
        propagate_spans: bool,
        at: int,
        source_node_key: str,
        source_state: str,
        derived_last: dict[str, str],
    ) -> Iterable[Transition]:
        service_ids = [
            src_id
            for src_id, _dst_id, edge_key in self._graph._graph.in_edges(pod_node_id, keys=True)  # type: ignore[call-arg]
            if edge_key == DepKind.routes_to
        ]
        for service_id in service_ids:
            service_node = self._graph.get_node_by_id(service_id)
            yield from self._maybe_emit(
                derived_node_key=service_node.uniq_name,
                derived_kind=PlaceKind.service,
                derived_state=derived_service_state,
                at=at,
                source_node_key=source_node_key,
                source_state=source_state,
                derived_last=derived_last,
            )
            if propagate_spans:
                yield from self._propagate_to_spans(
                    service_node.uniq_name,
                    source_state,
                    at,
                    derived_last,
                    source_node_key_override=source_node_key,
                )

    def _propagate_to_spans(
        self,
        service_key: str,
        source_state: str,
        at: int,
        derived_last: dict[str, str],
        *,
        source_node_key_override: str | None = None,
        derived_span_state: str = "missing",
    ) -> Iterable[Transition]:
        service_node = self._graph.get_node_by_name(service_key)
        if service_node is None or service_node.id is None:
            return
        source_for_evidence = source_node_key_override or service_key
        for _src_id, dst_id, edge_key in self._graph._graph.out_edges(service_node.id, keys=True):  # type: ignore[call-arg]
            if edge_key != DepKind.includes:
                continue
            span_node = self._graph.get_node_by_id(dst_id)
            if span_node.kind != PlaceKind.span:
                continue
            yield from self._maybe_emit(
                derived_node_key=span_node.uniq_name,
                derived_kind=PlaceKind.span,
                derived_state=derived_span_state,
                at=at,
                source_node_key=source_for_evidence,
                source_state=source_state,
                derived_last=derived_last,
            )

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
