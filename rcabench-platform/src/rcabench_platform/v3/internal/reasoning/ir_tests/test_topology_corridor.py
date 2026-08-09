"""Unit tests for TopologyExplorer.compute_corridor (§7.4).

Validates that bidirectional BFS prunes forward-only over-inclusion: nodes
reachable from the injection but unable to reach any alarm are excluded.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.topology_explorer import (
    TopologyExplorer,
)
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


def _build_corridor_fixture() -> tuple[HyperGraph, dict[str, Node]]:
    """Two forward branches from an injection node:

    Branch 1 (dead-end, forward-only over-includes): I -> A -> B
        A and B are forward-reachable but cannot reach any alarm.

    Branch 2 (true corridor): I -> C -> AlarmNode
        C sits on the injection→alarm path.

    A noise node X is wired so that X -> AlarmNode but is unreachable
    from I — it must NOT appear in the corridor either.
    """
    g = HyperGraph()
    inj = _add_node(g, PlaceKind.service, "I")
    a = _add_node(g, PlaceKind.service, "A")
    b = _add_node(g, PlaceKind.service, "B")
    c = _add_node(g, PlaceKind.service, "C")
    alarm = _add_node(g, PlaceKind.span, "AlarmNode")
    noise = _add_node(g, PlaceKind.service, "X")

    # Dead-end branch.
    _add_edge(g, inj, a, DepKind.depends_on)
    _add_edge(g, a, b, DepKind.depends_on)

    # Corridor branch.
    _add_edge(g, inj, c, DepKind.depends_on)
    _add_edge(g, c, alarm, DepKind.includes)

    # Backward-reachable from alarm but not forward-reachable from I.
    _add_edge(g, noise, alarm, DepKind.includes)

    return g, {"I": inj, "A": a, "B": b, "C": c, "alarm": alarm, "X": noise}


def test_corridor_excludes_dead_end_forward_branch() -> None:
    """Forward-only reach would include {I, A, B, C, alarm}.

    The corridor must drop A and B because they cannot reach AlarmNode,
    and must drop X because X is not forward-reachable from I.
    """
    g, nodes = _build_corridor_fixture()
    explorer = TopologyExplorer(g, max_hops=5)

    inj_id = nodes["I"].id
    alarm_id = nodes["alarm"].id
    assert inj_id is not None and alarm_id is not None

    corridor = explorer.compute_corridor(
        injection_node_ids=[inj_id],
        alarm_node_ids={alarm_id},
    )

    expected_in = {inj_id, nodes["C"].id, alarm_id}
    expected_out = {nodes["A"].id, nodes["B"].id, nodes["X"].id}

    assert expected_in.issubset(corridor)
    assert corridor.isdisjoint(expected_out)
    assert corridor == expected_in


def test_corridor_empty_alarm_set_returns_empty() -> None:
    """Degenerate case: no alarms → no corridor (documented behavior)."""
    g, nodes = _build_corridor_fixture()
    explorer = TopologyExplorer(g, max_hops=5)

    inj_id = nodes["I"].id
    assert inj_id is not None

    corridor = explorer.compute_corridor(
        injection_node_ids=[inj_id],
        alarm_node_ids=set(),
    )
    assert corridor == set()


def test_corridor_empty_injection_set_returns_empty() -> None:
    """Symmetric degenerate case: no injection → no corridor."""
    g, nodes = _build_corridor_fixture()
    explorer = TopologyExplorer(g, max_hops=5)

    alarm_id = nodes["alarm"].id
    assert alarm_id is not None

    corridor = explorer.compute_corridor(
        injection_node_ids=[],
        alarm_node_ids={alarm_id},
    )
    assert corridor == set()


def test_corridor_max_hops_fwd_caps_reach() -> None:
    """Deeper alarm-reaching nodes are excluded when max_hops_fwd shrinks.

    Build a longer chain I -> C1 -> C2 -> Alarm. With max_hops_fwd=1,
    the forward reach from I covers only {I, C1}; intersecting with the
    backward reach from Alarm therefore drops C2 and Alarm even though
    they sit on the true corridor.
    """
    g = HyperGraph()
    inj = _add_node(g, PlaceKind.service, "I")
    c1 = _add_node(g, PlaceKind.service, "C1")
    c2 = _add_node(g, PlaceKind.service, "C2")
    alarm = _add_node(g, PlaceKind.span, "Alarm")
    _add_edge(g, inj, c1, DepKind.depends_on)
    _add_edge(g, c1, c2, DepKind.depends_on)
    _add_edge(g, c2, alarm, DepKind.includes)

    explorer = TopologyExplorer(g, max_hops=5)

    inj_id = inj.id
    alarm_id = alarm.id
    c1_id = c1.id
    c2_id = c2.id
    assert inj_id is not None and alarm_id is not None
    assert c1_id is not None and c2_id is not None

    # Sanity: with the default budget, the full chain is in the corridor.
    full_corridor = explorer.compute_corridor(
        injection_node_ids=[inj_id],
        alarm_node_ids={alarm_id},
    )
    assert full_corridor == {inj_id, c1_id, c2_id, alarm_id}

    # max_hops_fwd=1: forward visits at most 1 hop from I → {I, C1}.
    # Backward from Alarm reaches {Alarm, C2, C1, I}. Intersection drops
    # C2 and Alarm because they are >1 forward hop from I.
    capped_corridor = explorer.compute_corridor(
        injection_node_ids=[inj_id],
        alarm_node_ids={alarm_id},
        max_hops_fwd=1,
    )
    assert capped_corridor == {inj_id, c1_id}
    assert c2_id not in capped_corridor
    assert alarm_id not in capped_corridor


def test_corridor_max_hops_bwd_caps_reach() -> None:
    """Symmetric cap: shrinking max_hops_bwd drops nodes too far from alarm."""
    g = HyperGraph()
    inj = _add_node(g, PlaceKind.service, "I")
    c1 = _add_node(g, PlaceKind.service, "C1")
    c2 = _add_node(g, PlaceKind.service, "C2")
    alarm = _add_node(g, PlaceKind.span, "Alarm")
    _add_edge(g, inj, c1, DepKind.depends_on)
    _add_edge(g, c1, c2, DepKind.depends_on)
    _add_edge(g, c2, alarm, DepKind.includes)

    explorer = TopologyExplorer(g, max_hops=5)

    inj_id = inj.id
    alarm_id = alarm.id
    c1_id = c1.id
    c2_id = c2.id
    assert inj_id is not None and alarm_id is not None
    assert c1_id is not None and c2_id is not None

    # max_hops_bwd=1: backward visits at most 1 predecessor hop from Alarm
    # → {Alarm, C2}. Forward from I reaches the entire chain. Intersection
    # excludes I and C1 because they are >1 backward hop from Alarm.
    capped_corridor = explorer.compute_corridor(
        injection_node_ids=[inj_id],
        alarm_node_ids={alarm_id},
        max_hops_bwd=1,
    )
    assert capped_corridor == {c2_id, alarm_id}
    assert inj_id not in capped_corridor
    assert c1_id not in capped_corridor


def test_corridor_edge_filter_is_applied_to_both_passes() -> None:
    """An edge_filter that drops a specific edge breaks the corridor on
    that branch in both forward and backward traversal."""
    g, nodes = _build_corridor_fixture()
    explorer = TopologyExplorer(g, max_hops=5)

    inj_id = nodes["I"].id
    c_id = nodes["C"].id
    alarm_id = nodes["alarm"].id
    assert inj_id is not None and c_id is not None and alarm_id is not None

    # Filter out the C -> alarm edge entirely.
    def edge_filter(src: int, dst: int, _is_first_hop: bool) -> bool:
        return not (src == c_id and dst == alarm_id)

    corridor = explorer.compute_corridor(
        injection_node_ids=[inj_id],
        alarm_node_ids={alarm_id},
        edge_filter=edge_filter,
    )

    # Forward from I cannot cross C -> alarm; backward from alarm cannot
    # cross alarm <- C either. Intersection collapses (apart from the
    # endpoints themselves, which are not connected through any path
    # within the filter).
    assert alarm_id not in corridor or inj_id not in corridor
    assert c_id not in corridor
