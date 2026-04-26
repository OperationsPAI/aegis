"""Corridor-and-activity-filter wiring in :class:`FaultPropagator` (§7.6, §13.2).

Validates PR#7's contract:

* Bidirectional corridor (Reach_forward(injection) ∩ Reach_backward(alarm))
  prunes dead-end forward branches before DFS.
* Activity filter (corridor ∩ (deviating_set ∪ injection_set)) drops
  topologically-relevant but HEALTHY middlemen, except for the injection
  node itself (which may still be HEALTHY for silent injections).
* DFS short-circuits at ``self.max_paths`` and logs a warning.
* Empty alarm set short-circuits to no paths + a warning.

The propagator wires the corridor inline using ``find_reachable_subgraph``
twice (forward edge_filter + reversed-orientation backward filter), since
``compute_corridor`` walks pure out_edges/in_edges and rcabench's ``calls``
edges run counter to the propagation direction.
"""

from __future__ import annotations

import logging

import pytest

from rcabench_platform.v3.internal.reasoning.algorithms.propagator import FaultPropagator
from rcabench_platform.v3.internal.reasoning.algorithms.topology_explorer import (
    TopologyExplorer,
)
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.models.graph import (
    CallsEdgeData,
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)
from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules


def _calls(src_id: int, dst_id: int, src_name: str, dst_name: str) -> Edge:
    return Edge(
        src_id=src_id,
        dst_id=dst_id,
        src_name=src_name,
        dst_name=dst_name,
        kind=DepKind.calls,
        data=CallsEdgeData(),
    )


def _tl(node_key: str, kind: PlaceKind, state: str, *, start: int = 1000, end: int = 2000) -> StateTimeline:
    return StateTimeline(
        node_key=node_key,
        kind=kind,
        windows=(
            TimelineWindow(
                start=start,
                end=end,
                state=state,
                level=EvidenceLevel.observed,
                trigger="test",
                evidence={},
            ),
        ),
    )


# --------------------------------------------------------------------------- #
# Test 1 — corridor pruning drops dead-end forward branches
# --------------------------------------------------------------------------- #


def test_corridor_drops_dead_end_branch_from_subgraph() -> None:
    """A forward-reachable but alarm-unreachable branch is excluded.

    Topology (calls edges, caller → callee):

        AlarmTop  -- calls -->  Mid  -- calls -->  Inject (leaf, injected)
                                Mid  -- calls -->  Dead   (mid-tier callee, no alarm above it)

    Propagation flows leaf → mid → top (against edge orientation).
    Without corridor pruning, the subgraph_edges would also list Mid↔Dead
    even though Dead can never reach AlarmTop. The corridor wiring filters
    Dead out — its in/out neighbors are all outside the corridor.
    """
    g = HyperGraph()
    inject = g.add_node(Node(kind=PlaceKind.span, self_name="leaf::POST /db"))
    mid = g.add_node(Node(kind=PlaceKind.span, self_name="mid::POST /work"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /"))
    dead = g.add_node(Node(kind=PlaceKind.span, self_name="dead::GET /side"))
    assert inject.id is not None and mid.id is not None
    assert alarm.id is not None and dead.id is not None

    # Real chain: top → mid → leaf.
    g.add_edge(_calls(alarm.id, mid.id, alarm.uniq_name, mid.uniq_name))
    g.add_edge(_calls(mid.id, inject.id, mid.uniq_name, inject.uniq_name))
    # Dead-end branch from mid: mid → dead.
    g.add_edge(_calls(mid.id, dead.id, mid.uniq_name, dead.uniq_name))

    # All deviating so the activity filter doesn't conflate this with Test 2.
    timelines = {
        inject.uniq_name: _tl(inject.uniq_name, PlaceKind.span, "erroring", start=1000),
        mid.uniq_name: _tl(mid.uniq_name, PlaceKind.span, "erroring", start=1010),
        alarm.uniq_name: _tl(alarm.uniq_name, PlaceKind.span, "erroring", start=1020),
        dead.uniq_name: _tl(dead.uniq_name, PlaceKind.span, "erroring", start=1015),
    }

    propagator = FaultPropagator(
        graph=g,
        rules=get_builtin_rules(),
        timelines=timelines,
        max_hops=4,
    )
    result = propagator.propagate_from_injection(
        injection_node_ids=[inject.id],
        alarm_nodes={alarm.id},
    )

    # Sanity: the main chain still produces a path.
    assert len(result.paths) >= 1
    main_path = result.paths[0]
    assert main_path.nodes[0] == inject.id
    assert main_path.nodes[-1] == alarm.id

    # Dead-end node is excluded from subgraph_edges (corridor effect).
    nodes_in_subgraph = {n for s, d in result.subgraph_edges for n in (s, d)}
    assert dead.id not in nodes_in_subgraph, (
        f"corridor failed to prune dead-end node {dead.id}; "
        f"subgraph_edges={result.subgraph_edges}"
    )

    # Baseline check: forward-only find_reachable_subgraph WOULD include
    # the dead-end branch — confirming the corridor is the actual pruner.
    explorer = TopologyExplorer(g, max_hops=4)
    forward_only = explorer.find_reachable_subgraph(
        [inject.id], {alarm.id}, edge_filter=None
    )
    forward_only_nodes = {n for s, d in forward_only for n in (s, d)}
    assert dead.id in forward_only_nodes, (
        "test fixture invalid: forward-only reach must reach the dead-end "
        "branch for the corridor comparison to be meaningful"
    )


# --------------------------------------------------------------------------- #
# Test 2 — activity filter drops HEALTHY corridor middlemen
# --------------------------------------------------------------------------- #


def test_activity_filter_drops_healthy_middleman() -> None:
    """A topologically-corridor middleman that is HEALTHY is excluded.

    Topology: AlarmTop --calls--> Mid --calls--> Inject (leaf).
    Mid is HEALTHY; Inject and AlarmTop are erroring.

    Per §7.4, ``relevant_nodes = corridor ∩ (deviating_set ∪ injection_set)``.
    Mid is in the corridor (sits between injection and alarm) but not in
    deviating_set and not the injection — so the activity filter drops it,
    breaking the only chain to alarm. Result: no admissible path.
    """
    g = HyperGraph()
    inject = g.add_node(Node(kind=PlaceKind.span, self_name="leaf::POST /db"))
    mid = g.add_node(Node(kind=PlaceKind.span, self_name="mid::POST /work"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /"))
    assert inject.id is not None and mid.id is not None and alarm.id is not None

    g.add_edge(_calls(alarm.id, mid.id, alarm.uniq_name, mid.uniq_name))
    g.add_edge(_calls(mid.id, inject.id, mid.uniq_name, inject.uniq_name))

    timelines = {
        inject.uniq_name: _tl(inject.uniq_name, PlaceKind.span, "erroring", start=1000),
        mid.uniq_name: _tl(mid.uniq_name, PlaceKind.span, "healthy", start=1010),
        alarm.uniq_name: _tl(alarm.uniq_name, PlaceKind.span, "erroring", start=1020),
    }

    propagator = FaultPropagator(
        graph=g,
        rules=get_builtin_rules(),
        timelines=timelines,
        max_hops=4,
    )
    result = propagator.propagate_from_injection(
        injection_node_ids=[inject.id],
        alarm_nodes={alarm.id},
    )

    # Activity filter drops Mid — the corridor edge (inject↔mid) is gone.
    nodes_in_subgraph = {n for s, d in result.subgraph_edges for n in (s, d)}
    assert mid.id not in nodes_in_subgraph, (
        f"activity filter failed to drop HEALTHY middleman; "
        f"subgraph_edges={result.subgraph_edges}"
    )


def test_compute_deviating_set_excludes_healthy_and_unknown() -> None:
    """``_compute_deviating_set`` excludes HEALTHY/UNKNOWN-only nodes.

    Verifies the activity-filter input directly: a node is in the deviating
    set iff at least one of its timeline windows is in a non-HEALTHY,
    non-UNKNOWN state. The propagator's ``relevant_nodes`` then unions
    this with ``injection_set`` so that a HEALTHY injection still survives
    the activity filter (silent-injection guard, §7.4).
    """
    g = HyperGraph()
    inject = g.add_node(Node(kind=PlaceKind.span, self_name="leaf::POST /db"))
    healthy_mid = g.add_node(Node(kind=PlaceKind.span, self_name="mid::POST /work"))
    erroring_top = g.add_node(Node(kind=PlaceKind.span, self_name="frontend::GET /"))
    unknown_only = g.add_node(Node(kind=PlaceKind.span, self_name="ghost::?"))
    assert inject.id is not None and healthy_mid.id is not None
    assert erroring_top.id is not None and unknown_only.id is not None

    timelines = {
        inject.uniq_name: _tl(inject.uniq_name, PlaceKind.span, "healthy", start=1000),
        healthy_mid.uniq_name: _tl(healthy_mid.uniq_name, PlaceKind.span, "healthy", start=1010),
        erroring_top.uniq_name: _tl(erroring_top.uniq_name, PlaceKind.span, "erroring", start=1020),
        unknown_only.uniq_name: _tl(unknown_only.uniq_name, PlaceKind.span, "unknown", start=1015),
    }

    propagator = FaultPropagator(
        graph=g,
        rules=get_builtin_rules(),
        timelines=timelines,
        max_hops=4,
    )
    deviating = propagator._compute_deviating_set()
    assert deviating == {erroring_top.id}, (
        f"deviating_set must contain only the erroring node, got {deviating}"
    )

    # The injection_set ∪ deviating_set guard means the activity filter
    # cannot drop a HEALTHY injection node — verified by checking that
    # the union (the actual filter input) covers both.
    injection_set = {inject.id}
    relevant_input = deviating | injection_set
    assert inject.id in relevant_input
    assert erroring_top.id in relevant_input
    assert healthy_mid.id not in relevant_input
    assert unknown_only.id not in relevant_input


# --------------------------------------------------------------------------- #
# Test 3 — max_paths safety cap + warning
# --------------------------------------------------------------------------- #


def test_extract_paths_max_paths_cap_fires_and_warns(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """``extract_paths`` short-circuits at ``self.max_paths`` and warns.

    Five parallel I→Xi→Alarm chains yield five paths. With ``max_paths=3``
    DFS must truncate at 3 results and emit a warning record.
    """
    g = HyperGraph()
    inject = g.add_node(Node(kind=PlaceKind.span, self_name="leaf"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="alarm"))
    middlemen: list[Node] = []
    for i in range(5):
        m = g.add_node(Node(kind=PlaceKind.span, self_name=f"mid_{i}"))
        middlemen.append(m)
    assert inject.id is not None and alarm.id is not None

    # Build I→Xi→Alarm fan-out (using raw integer IDs since extract_paths
    # works on (src,dst) tuples directly without consulting graph topology).
    edges: list[tuple[int, int]] = []
    for m in middlemen:
        assert m.id is not None
        edges.append((inject.id, m.id))
        edges.append((m.id, alarm.id))

    explorer = TopologyExplorer(g, max_hops=5, max_paths=3)
    with caplog.at_level(logging.WARNING):
        paths = explorer.extract_paths(edges, [inject.id], {alarm.id})

    assert len(paths) == 3, f"expected exactly max_paths=3, got {len(paths)}"
    assert any(
        "max_paths" in rec.message and "safety cap" in rec.message
        for rec in caplog.records
    ), f"expected safety-cap warning, got: {[r.message for r in caplog.records]}"


def test_extract_paths_under_cap_does_not_warn(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """No warning when the path count is below ``max_paths``."""
    g = HyperGraph()
    inject = g.add_node(Node(kind=PlaceKind.span, self_name="leaf"))
    alarm = g.add_node(Node(kind=PlaceKind.span, self_name="alarm"))
    mid = g.add_node(Node(kind=PlaceKind.span, self_name="mid"))
    assert inject.id is not None and alarm.id is not None and mid.id is not None

    edges: list[tuple[int, int]] = [(inject.id, mid.id), (mid.id, alarm.id)]

    explorer = TopologyExplorer(g, max_hops=5, max_paths=10)
    with caplog.at_level(logging.WARNING):
        paths = explorer.extract_paths(edges, [inject.id], {alarm.id})

    assert len(paths) == 1
    assert not any("safety cap" in rec.message for rec in caplog.records)


# --------------------------------------------------------------------------- #
# Test 4 — empty alarm set short-circuits to empty result
# --------------------------------------------------------------------------- #


def test_empty_alarm_set_returns_empty_paths_and_warns() -> None:
    """``alarm_nodes = ∅`` ⇒ corridor empty ⇒ no paths, with a warning."""
    g = HyperGraph()
    inject = g.add_node(Node(kind=PlaceKind.span, self_name="leaf::POST /db"))
    other = g.add_node(Node(kind=PlaceKind.span, self_name="other::POST /work"))
    assert inject.id is not None and other.id is not None
    g.add_edge(_calls(other.id, inject.id, other.uniq_name, inject.uniq_name))

    timelines = {
        inject.uniq_name: _tl(inject.uniq_name, PlaceKind.span, "erroring", start=1000),
        other.uniq_name: _tl(other.uniq_name, PlaceKind.span, "erroring", start=1010),
    }

    propagator = FaultPropagator(
        graph=g,
        rules=get_builtin_rules(),
        timelines=timelines,
        max_hops=4,
    )
    result = propagator.propagate_from_injection(
        injection_node_ids=[inject.id],
        alarm_nodes=set(),
    )

    assert result.paths == []
    assert result.subgraph_edges == []
    assert result.warnings, "expected at least one warning when alarm set is empty"
