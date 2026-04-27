"""Injection-anchored inferred-edge tests (§7.3).

Each test builds a minimal HyperGraph + StateTimeline fixture, anchors
the inference at an explicit ``injection_node_ids`` list, and asserts
the §7.3 Scenario A / Scenario E behaviour.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.ir.adapters.inferred_edges import (
    InferredEdgeMetadata,
    enrich_with_inferred_edges,
)
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.models.graph import (
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)


def _add_node(graph: HyperGraph, kind: PlaceKind, name: str) -> Node:
    return graph.add_node(Node(kind=kind, self_name=name))


def _add_edge(graph: HyperGraph, src: Node, dst: Node, kind: DepKind) -> None:
    assert src.id is not None and dst.id is not None
    graph.add_edge(
        Edge(
            src_id=src.id,
            dst_id=dst.id,
            src_name=src.uniq_name,
            dst_name=dst.uniq_name,
            kind=kind,
            data=None,
        )
    )


def _timeline(node_key: str, kind: PlaceKind, windows: list[tuple[int, int, str]]) -> StateTimeline:
    """Build a StateTimeline from ``[(start, end, state), ...]`` triples."""
    return StateTimeline(
        node_key=node_key,
        kind=kind,
        windows=tuple(
            TimelineWindow(
                start=start,
                end=end,
                state=state,
                level=EvidenceLevel.observed,
                trigger="fixture",
                evidence={},
            )
            for start, end, state in windows
        ),
    )


# --------------------------------------------------------------------------- #
# Scenario A
# --------------------------------------------------------------------------- #


def test_scenario_a_positive_dead_pod_emits_edge_to_consumer_anomalous_span() -> None:
    """Dead injection pod with a consumer service whose span is anomalous
    yields one inferred ``service|owner --includes--> span|consumer_anomalous``
    edge — bridging dead-infra state to its consumer alarms via the
    existing ``service_to_span`` rule (src_states includes ``unavailable``).
    """
    g = HyperGraph()
    # Owner side: service -> pod, service -> span (the dead svc's API).
    owner = _add_node(g, PlaceKind.service, "payment")
    pod = _add_node(g, PlaceKind.pod, "payment-0")
    owner_span = _add_node(g, PlaceKind.span, "payment::POST /pay")
    _add_edge(g, owner, pod, DepKind.routes_to)
    _add_edge(g, owner, owner_span, DepKind.includes)
    # Consumer side: caller_svc -> caller_span -> calls -> owner_span.
    caller_svc = _add_node(g, PlaceKind.service, "checkout")
    caller_span = _add_node(g, PlaceKind.span, "checkout::POST /buy")
    _add_edge(g, caller_svc, caller_span, DepKind.includes)
    _add_edge(g, caller_span, owner_span, DepKind.calls)

    timelines = {
        "pod|payment-0": _timeline("pod|payment-0", PlaceKind.pod, [(1000, 1050, "unavailable")]),
        # Caller span is anomalous in the abnormal window.
        "span|checkout::POST /buy": _timeline("span|checkout::POST /buy", PlaceKind.span, [(1000, 1050, "missing")]),
    }
    assert pod.id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [pod.id])

    assert n_added == 1
    assert owner.id is not None and caller_span.id is not None
    assert g._graph.has_edge(owner.id, caller_span.id, DepKind.includes)

    metadata_store = g.data["inferred_edges"]
    metadata = metadata_store[(owner.id, caller_span.id)]
    assert isinstance(metadata, InferredEdgeMetadata)
    assert metadata.inferred is True
    assert metadata.kind == "depends_on_dead_infra"


def test_scenario_a_negative_healthy_pod_emits_no_edge() -> None:
    """Healthy injection pod yields zero inferred edges."""
    g = HyperGraph()
    svc = _add_node(g, PlaceKind.service, "payment")
    pod = _add_node(g, PlaceKind.pod, "payment-0")
    _add_edge(g, svc, pod, DepKind.routes_to)

    timelines = {
        "pod|payment-0": _timeline("pod|payment-0", PlaceKind.pod, [(1000, 1050, "healthy")]),
    }
    assert pod.id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [pod.id])

    assert n_added == 0
    assert g.data.get("inferred_edges", {}) == {}


# --------------------------------------------------------------------------- #
# Scenario E
# --------------------------------------------------------------------------- #


def _build_three_service_chain() -> tuple[HyperGraph, dict[str, Node]]:
    """auth-svc --calls--> user-mgmt --calls--> notify (via spans).

    Each service has one span; spans are connected by ``calls`` edges so
    forward BFS over ``includes`` ∪ ``calls`` walks
    auth-svc → span_auth → span_user → user-mgmt → span_user → span_notify → notify.
    """
    g = HyperGraph()
    auth = _add_node(g, PlaceKind.service, "auth-svc")
    user = _add_node(g, PlaceKind.service, "user-mgmt")
    notify = _add_node(g, PlaceKind.service, "notify")
    span_auth = _add_node(g, PlaceKind.span, "auth-svc::POST /verify")
    span_user = _add_node(g, PlaceKind.span, "user-mgmt::GET /me")
    span_notify = _add_node(g, PlaceKind.span, "notify::POST /push")

    _add_edge(g, auth, span_auth, DepKind.includes)
    _add_edge(g, user, span_user, DepKind.includes)
    _add_edge(g, notify, span_notify, DepKind.includes)
    _add_edge(g, span_auth, span_user, DepKind.calls)
    _add_edge(g, span_user, span_notify, DepKind.calls)

    return g, {
        "auth": auth,
        "user": user,
        "notify": notify,
        "span_auth": span_auth,
        "span_user": span_user,
        "span_notify": span_notify,
    }


def test_scenario_e_positive_gating_service_silences_downstream() -> None:
    """auth-svc erroring + notify silent (user-mgmt healthy) → one edge."""
    g, nodes = _build_three_service_chain()
    timelines = {
        "service|auth-svc": _timeline("service|auth-svc", PlaceKind.service, [(1000, 1050, "erroring")]),
        "service|user-mgmt": _timeline("service|user-mgmt", PlaceKind.service, [(1000, 1050, "healthy")]),
        "service|notify": _timeline("service|notify", PlaceKind.service, [(1000, 1050, "silent")]),
    }
    assert nodes["auth"].id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [nodes["auth"].id])

    assert n_added == 1
    assert nodes["notify"].id is not None
    assert g._graph.has_edge(nodes["auth"].id, nodes["notify"].id, DepKind.includes)
    # user-mgmt is healthy, must NOT be a target.
    assert nodes["user"].id is not None
    assert not g._graph.has_edge(nodes["auth"].id, nodes["user"].id, DepKind.includes)

    metadata_store = g.data["inferred_edges"]
    metadata = metadata_store[(nodes["auth"].id, nodes["notify"].id)]
    assert isinstance(metadata, InferredEdgeMetadata)
    assert metadata.inferred is True
    assert metadata.kind == "gated_silenced"


def test_scenario_e_closer_erroring_ancestor_suppresses_edge() -> None:
    """When user-mgmt is erroring, notify's silence is explained by it,
    not by the gating auth-svc — so no inferred edge is emitted."""
    g, nodes = _build_three_service_chain()
    timelines = {
        "service|auth-svc": _timeline("service|auth-svc", PlaceKind.service, [(1000, 1050, "erroring")]),
        "service|user-mgmt": _timeline("service|user-mgmt", PlaceKind.service, [(1000, 1050, "erroring")]),
        "service|notify": _timeline("service|notify", PlaceKind.service, [(1000, 1050, "silent")]),
    }
    assert nodes["auth"].id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [nodes["auth"].id])

    assert n_added == 0
    assert g.data.get("inferred_edges", {}) == {}


def test_scenario_e_non_gating_service_emits_no_edge() -> None:
    """Injected service named 'payment' (no gating substring) yields zero
    edges even though notify is silent."""
    g = HyperGraph()
    payment = _add_node(g, PlaceKind.service, "payment")
    user = _add_node(g, PlaceKind.service, "user-mgmt")
    notify = _add_node(g, PlaceKind.service, "notify")
    span_payment = _add_node(g, PlaceKind.span, "payment::POST /charge")
    span_user = _add_node(g, PlaceKind.span, "user-mgmt::GET /me")
    span_notify = _add_node(g, PlaceKind.span, "notify::POST /push")
    _add_edge(g, payment, span_payment, DepKind.includes)
    _add_edge(g, user, span_user, DepKind.includes)
    _add_edge(g, notify, span_notify, DepKind.includes)
    _add_edge(g, span_payment, span_user, DepKind.calls)
    _add_edge(g, span_user, span_notify, DepKind.calls)

    timelines = {
        "service|payment": _timeline("service|payment", PlaceKind.service, [(1000, 1050, "erroring")]),
        "service|user-mgmt": _timeline("service|user-mgmt", PlaceKind.service, [(1000, 1050, "healthy")]),
        "service|notify": _timeline("service|notify", PlaceKind.service, [(1000, 1050, "silent")]),
    }
    assert payment.id is not None
    n_added = enrich_with_inferred_edges(g, timelines, [payment.id])

    assert n_added == 0
    assert g.data.get("inferred_edges", {}) == {}
