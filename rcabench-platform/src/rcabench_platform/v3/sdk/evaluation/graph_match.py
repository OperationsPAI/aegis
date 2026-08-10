"""Graph-match layer: compare agent's propagation graph vs GT causal_graph.

Service-level node_f1 / edge_f1 plus a connectivity check
(``compute_path_reachability``) anchored on HIT root_causes.

Loadgen-style synthetic traffic sources are stripped from both sides — they
are never GT root causes and the "user-visible" boundary sits one hop above
them — so mentioning loadgen or not should not move node_f1 / edge_f1 /
path_reachability.
"""

from __future__ import annotations

from collections import deque

from pydantic import BaseModel, Field

from ._normalize import LOADGEN_NORM, norm
from .agent_output import AgentRCAOutput
from .causal_graph import CausalGraph
from .set_match import MatchStatus, OutcomeResult


class GraphMetrics(BaseModel):
    """Service-level graph comparison vs ground-truth causal_graph.json."""

    node_precision: float = 0.0
    node_recall: float = 0.0
    node_f1: float = 0.0
    edge_precision: float = 0.0
    edge_recall: float = 0.0
    edge_f1: float = 0.0

    matched_services: list[str] = Field(default_factory=list)
    missed_services: list[str] = Field(default_factory=list)
    hallucinated_services: list[str] = Field(default_factory=list)

    matched_edges: list[tuple[str, str]] = Field(default_factory=list)
    missed_edges: list[tuple[str, str]] = Field(default_factory=list)
    hallucinated_edges: list[tuple[str, str]] = Field(default_factory=list)

    applicable: bool = True


def _agent_service_set(agent: AgentRCAOutput) -> set[str]:
    out: set[str] = set()
    for rc in agent.root_causes:
        out.add(norm(rc.service))
        if rc.direction:
            out.add(norm(rc.direction.src))
            out.add(norm(rc.direction.dst))
    for prop in agent.propagation:
        out.add(norm(prop.from_))
        out.add(norm(prop.to))
    out.discard("")
    out -= LOADGEN_NORM
    return out


def _agent_edge_set(agent: AgentRCAOutput) -> set[tuple[str, str]]:
    out: set[tuple[str, str]] = set()
    for prop in agent.propagation:
        s, t = norm(prop.from_), norm(prop.to)
        if s and t and s != t and s not in LOADGEN_NORM and t not in LOADGEN_NORM:
            out.add((s, t))
    return out


def _prf(agent: set, gt: set) -> tuple[float, float, float]:
    if not agent and not gt:
        return 1.0, 1.0, 1.0
    matched = agent & gt
    p = len(matched) / len(agent) if agent else 0.0
    r = len(matched) / len(gt) if gt else 0.0
    f1 = (2 * p * r / (p + r)) if (p + r) else 0.0
    return p, r, f1


def compute_graph_metrics(agent: AgentRCAOutput, gt_graph: CausalGraph | None) -> GraphMetrics:
    if gt_graph is None:
        return GraphMetrics(applicable=False)

    agent_nodes = _agent_service_set(agent)
    agent_edges = _agent_edge_set(agent)

    gt_nodes_raw = gt_graph.get_service_nodes()
    gt_edges_raw = gt_graph.get_service_edges()
    gt_nodes = {norm(s) for s in gt_nodes_raw}
    gt_nodes.discard("")
    gt_nodes -= LOADGEN_NORM
    gt_edges = {(norm(s), norm(t)) for s, t in gt_edges_raw}
    gt_edges = {(s, t) for s, t in gt_edges if s and t and s != t and s not in LOADGEN_NORM and t not in LOADGEN_NORM}

    node_p, node_r, node_f1 = _prf(agent_nodes, gt_nodes)
    edge_p, edge_r, edge_f1 = _prf(agent_edges, gt_edges)

    return GraphMetrics(
        node_precision=node_p,
        node_recall=node_r,
        node_f1=node_f1,
        edge_precision=edge_p,
        edge_recall=edge_r,
        edge_f1=edge_f1,
        matched_services=sorted(agent_nodes & gt_nodes),
        missed_services=sorted(gt_nodes - agent_nodes),
        hallucinated_services=sorted(agent_nodes - gt_nodes),
        matched_edges=sorted(agent_edges & gt_edges),
        missed_edges=sorted(gt_edges - agent_edges),
        hallucinated_edges=sorted(agent_edges - gt_edges),
        applicable=True,
    )


def compute_path_reachability(
    agent: AgentRCAOutput,
    outcome: OutcomeResult,
    gt_graph: CausalGraph | None,
) -> bool | None:
    """Does the agent's propagation graph contain at least one path from a
    correctly-identified root cause to a GT alarm service?

    Returns ``None`` when the metric is not applicable (no GT graph, or GT
    graph has no alarm services). Otherwise returns ``True`` iff there exists
    some HIT agent root_cause whose service can reach some alarm service via
    the agent's own ``propagation`` edges, traversed as **undirected** (both
    ``from -> to`` and ``to -> from`` admit traversal). Path length 0 counts.

    Direction is intentionally collapsed: the agent contract uses ``from`` /
    ``to`` for the fault-impact direction (failing service → user-visible
    alarm), but some models silently re-interpret it as the request-call
    direction (caller → callee), which inverts every edge. This metric grades
    the CONNECTIVITY of the agent's chain claim, not the arrow direction;
    ``edge_f1`` is the strict-direction counterpart.

    Anchoring on HIT root causes prevents trivial passes — an agent that
    emits a generic ``loadgen -> frontend -> cart`` chain without identifying
    any GT fault scores 0 here even though its edges might overlap real ones.
    """
    if gt_graph is None:
        return None
    alarm_services = {norm(s) for s in gt_graph.get_alarm_services()}
    alarm_services.discard("")
    if not alarm_services:
        return None

    hit_rc_indices = {
        m.matched_root_cause_index
        for m in outcome.per_fault
        if m.status == MatchStatus.HIT and m.matched_root_cause_index is not None
    }
    if not hit_rc_indices:
        return False

    adj: dict[str, set[str]] = {}
    for s, t in _agent_edge_set(agent):
        adj.setdefault(s, set()).add(t)
        adj.setdefault(t, set()).add(s)

    for idx in hit_rc_indices:
        start = norm(agent.root_causes[idx].service)
        if not start:
            continue
        seen = {start}
        queue: deque[str] = deque([start])
        while queue:
            node = queue.popleft()
            if node in alarm_services:
                return True
            for nxt in adj.get(node, ()):
                if nxt not in seen:
                    seen.add(nxt)
                    queue.append(nxt)
    return False
