"""Single-tier (service, fault_kind) multiset matcher.

Match key is the normalized ``(service, fault_kind)`` pair. The agent's
``direction`` and ``method`` fields are allowed in the schema but do not
affect HIT/WRONG_KIND/MISS — this is the deliberate simplification spelled
out in the v2 README.

Service-level node_f1 / edge_f1 against the GT causal_graph stay as a
separate ``GraphMetrics`` block.
"""

from __future__ import annotations

from collections import deque
from enum import Enum

from pydantic import BaseModel, Field

from ..causal_graph import CausalGraph
from .fault_kind import NETWORK_KINDS, FaultKind
from .ground_truth import GTFault
from .schema import AgentRCAOutput, RootCauseClaim


class MatchStatus(str, Enum):
    HIT = "HIT"
    WRONG_KIND = "WRONG_KIND"
    MISS = "MISS"


class FaultMatchResult(BaseModel):
    gt_service: str
    gt_fault_kind: FaultKind
    matched_root_cause_index: int | None = None
    status: MatchStatus


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


class OutcomeResult(BaseModel):
    """Single-tier outcome derived from the (service, fault_kind) multiset.

    - ``f1`` (with ``precision`` / ``recall``) — the headline rate.
    - ``exact_match`` — True iff every agent rc and every GT fault paired by HIT.
    - ``fault_kind_accuracy`` — among service-correct rcs (HIT + WRONG_KIND),
      the share that are HIT. ``None`` when the denominator is 0; aggregator
      excludes those cases from the benchmark mean.
    """

    precision: float
    recall: float
    f1: float
    exact_match: bool

    fault_kind_accuracy: float | None
    kind_accuracy_denom: int

    per_fault: list[FaultMatchResult] = Field(default_factory=list)
    overclaim_indices: list[int] = Field(default_factory=list)


def _norm(name: str | None) -> str:
    if not name:
        return ""
    return name.strip().lower().replace("-", "").replace("_", "")


# Synthetic traffic sources excluded from graph metrics: they're never GT
# root causes (we don't inject into them), they're filtered out of GT
# alarm_nodes upstream (parquet_loader.is_root_alarm_candidate_row), and
# the right "user-visible" boundary is the topmost non-loadgen span
# (frontend / ts-ui-dashboard / front-end). Whether the agent mentions
# loadgen or not should not move node_f1 / edge_f1 / path_reachability.
_LOADGEN_NORM: frozenset[str] = frozenset(
    _norm(s)
    for s in (
        "loadgenerator",
        "load-generator",
        "locust",
        "wrk2",
        "dsb-wrk2",
        "k6",
    )
)


def _service_eq(a: str | None, b: str | None) -> bool:
    return _norm(a) == _norm(b) and bool(_norm(a))


def _gt_service_candidates(gt: GTFault) -> list[str]:
    """Service names that count as "the right side" for this GT fault.

    Network-class faults (netem rules) install on one end but the latency /
    drop signal shows on both — the agent can't tell which side has the rule
    from telemetry alone, so we accept either ``direction_src`` or
    ``direction_dst``. All other kinds stick to the single GT.service.
    """
    if gt.fault_kind in NETWORK_KINDS:
        out = [gt.service]
        if gt.direction_src and gt.direction_src != gt.service:
            out.append(gt.direction_src)
        if gt.direction_dst and gt.direction_dst != gt.service:
            out.append(gt.direction_dst)
        return out
    return [gt.service]


# Fault-kind equivalence classes for HIT determination. Two kinds in the same
# class differ only in a feature the agent cannot observe from the released
# telemetry, so the matcher treats them as the same answer.
#
# `pod_failure` vs `pod_unavailable` differ only in whether the unavailability
# extends past the injection window. The window length is GT-side knowledge;
# from telemetry the agent sees a service that stopped emitting and may or
# may not resume within the abnormal slice. Different models calibrate the
# threshold in opposite directions (e.g., one reads any sustained gap as
# `pod_unavailable` while another reserves it for cases that never recover),
# so the distinction empirically tracks model bias rather than diagnostic
# skill. The two are collapsed into one equivalence class at evaluation time.
_KIND_EQUIV_GROUPS: tuple[frozenset[FaultKind], ...] = (frozenset({FaultKind.POD_FAILURE, FaultKind.POD_UNAVAILABLE}),)


def _kind_eq(a: FaultKind, b: FaultKind) -> bool:
    if a == b:
        return True
    for group in _KIND_EQUIV_GROUPS:
        if a in group and b in group:
            return True
    return False


def _evaluate_pair(rc: RootCauseClaim, gt: GTFault) -> MatchStatus:
    if not any(_service_eq(rc.service, c) for c in _gt_service_candidates(gt)):
        return MatchStatus.MISS
    if not _kind_eq(rc.fault_kind, gt.fault_kind):
        return MatchStatus.WRONG_KIND
    return MatchStatus.HIT


_RANK = {
    MatchStatus.HIT: 0,
    MatchStatus.WRONG_KIND: 1,
    MatchStatus.MISS: 2,
}


def compute_outcome(agent: AgentRCAOutput, gt_faults: list[GTFault]) -> OutcomeResult:
    """Greedy 1-1 assignment of agent root_causes to GT faults.

    Each agent rc and each GT fault gets used at most once. Pairs are taken
    in HIT-first order so a HIT never gets shadowed by a WRONG_KIND that
    consumes the same agent rc.
    """
    n_agent = len(agent.root_causes)
    n_gt = len(gt_faults)

    triples: list[tuple[int, int, MatchStatus]] = []
    for i, rc in enumerate(agent.root_causes):
        for j, gt in enumerate(gt_faults):
            triples.append((i, j, _evaluate_pair(rc, gt)))
    triples.sort(key=lambda t: _RANK[t[2]])

    assigned_agent: dict[int, tuple[int, MatchStatus]] = {}
    assigned_gt: dict[int, tuple[int, MatchStatus]] = {}
    for i, j, status in triples:
        if status == MatchStatus.MISS:
            break
        if i in assigned_agent or j in assigned_gt:
            continue
        assigned_agent[i] = (j, status)
        assigned_gt[j] = (i, status)

    per_fault: list[FaultMatchResult] = []
    for j, gt in enumerate(gt_faults):
        if j in assigned_gt:
            i, status = assigned_gt[j]
            per_fault.append(
                FaultMatchResult(
                    gt_service=gt.service,
                    gt_fault_kind=gt.fault_kind,
                    matched_root_cause_index=i,
                    status=status,
                )
            )
        else:
            per_fault.append(
                FaultMatchResult(
                    gt_service=gt.service,
                    gt_fault_kind=gt.fault_kind,
                    matched_root_cause_index=None,
                    status=MatchStatus.MISS,
                )
            )

    overclaim_indices = [i for i in range(n_agent) if i not in assigned_agent]

    n_hit = sum(1 for v in assigned_agent.values() if v[1] == MatchStatus.HIT)
    n_wrong_kind = sum(1 for v in assigned_agent.values() if v[1] == MatchStatus.WRONG_KIND)

    if n_agent:
        precision = n_hit / n_agent
    else:
        precision = 1.0 if n_gt == 0 else 0.0
    if n_gt:
        recall = n_hit / n_gt
    else:
        recall = 1.0 if n_agent == 0 else 0.0
    f1 = (2 * precision * recall / (precision + recall)) if (precision + recall) else 0.0

    exact_match = (n_hit == n_gt) and (n_hit == n_agent) and n_gt > 0

    kind_denom = n_hit + n_wrong_kind
    kind_accuracy: float | None = (n_hit / kind_denom) if kind_denom > 0 else None

    return OutcomeResult(
        precision=precision,
        recall=recall,
        f1=f1,
        exact_match=exact_match,
        fault_kind_accuracy=kind_accuracy,
        kind_accuracy_denom=kind_denom,
        per_fault=per_fault,
        overclaim_indices=overclaim_indices,
    )


def _agent_service_set(agent: AgentRCAOutput) -> set[str]:
    out: set[str] = set()
    for rc in agent.root_causes:
        out.add(_norm(rc.service))
        if rc.direction:
            out.add(_norm(rc.direction.src))
            out.add(_norm(rc.direction.dst))
    for prop in agent.propagation:
        out.add(_norm(prop.from_))
        out.add(_norm(prop.to))
    out.discard("")
    out -= _LOADGEN_NORM
    return out


def _agent_edge_set(agent: AgentRCAOutput) -> set[tuple[str, str]]:
    out: set[tuple[str, str]] = set()
    for prop in agent.propagation:
        s, t = _norm(prop.from_), _norm(prop.to)
        if s and t and s != t and s not in _LOADGEN_NORM and t not in _LOADGEN_NORM:
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


def compute_path_reachability(
    agent: AgentRCAOutput,
    outcome: OutcomeResult,
    gt_graph: CausalGraph | None,
) -> bool | None:
    """Does the agent's propagation graph contain at least one path from a
    correctly-identified root cause to a GT alarm service?

    Returns ``None`` when the metric is not applicable (no GT graph, or GT graph
    has no alarm services). Otherwise returns ``True`` iff there exists some HIT
    agent root_cause whose service can reach some alarm service via the agent's
    own ``propagation`` edges, traversed as **undirected** (both ``from -> to``
    and ``to -> from`` admit traversal). Path length 0 counts.

    Direction is intentionally collapsed: the agent contract uses ``from`` /
    ``to`` for the fault-impact direction (failing service → user-visible
    alarm), but some models silently re-interpret it as the request-call
    direction (caller → callee), which inverts every edge. This metric grades
    the CONNECTIVITY of the agent's chain claim, not the arrow direction;
    ``edge_f1`` is the strict-direction counterpart.

    Anchoring on HIT root causes prevents trivial passes — an agent that emits
    a generic ``loadgen -> frontend -> cart`` chain without identifying any GT
    fault scores 0 here even though its edges might overlap real ones.
    """
    if gt_graph is None:
        return None
    alarm_services = {_norm(s) for s in gt_graph.get_alarm_services()}
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
        start = _norm(agent.root_causes[idx].service)
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


def compute_graph_metrics(agent: AgentRCAOutput, gt_graph: CausalGraph | None) -> GraphMetrics:
    if gt_graph is None:
        return GraphMetrics(applicable=False)

    agent_nodes = _agent_service_set(agent)
    agent_edges = _agent_edge_set(agent)

    gt_nodes_raw = gt_graph.get_service_nodes()
    gt_edges_raw = gt_graph.get_service_edges()
    gt_nodes = {_norm(s) for s in gt_nodes_raw}
    gt_nodes.discard("")
    gt_nodes -= _LOADGEN_NORM
    gt_edges = {(_norm(s), _norm(t)) for s, t in gt_edges_raw}
    gt_edges = {(s, t) for s, t in gt_edges if s and t and s != t and s not in _LOADGEN_NORM and t not in _LOADGEN_NORM}

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
