"""Type-aware matcher: pair each agent root_cause to a GT fault, plus
service-level node / edge F1 against the ground-truth causal graph."""

from __future__ import annotations

from enum import Enum

from pydantic import BaseModel, Field

from ..causal_graph import CausalGraph
from .fault_kind import NETWORK_KINDS, FaultKind
from .ground_truth import GTFault
from .schema import AgentRCAOutput, RootCauseClaim


class MatchStatus(str, Enum):
    HIT = "HIT"
    WRONG_DIRECTION = "WRONG_DIRECTION"
    WRONG_KIND = "WRONG_KIND"
    MISS = "MISS"


# Partial-credit weights, used for the `partial_*` scoring tier.
# Strict scoring (`root_cause_*`) still treats anything other than HIT as 0;
# the partial tier acknowledges that getting the service right but missing
# direction or kind is a closer answer than a complete miss.
_PARTIAL_WEIGHT: dict[MatchStatus, float] = {
    MatchStatus.HIT: 1.0,
    MatchStatus.WRONG_DIRECTION: 0.5,
    MatchStatus.WRONG_KIND: 0.5,
    MatchStatus.MISS: 0.0,
}


class FaultMatchResult(BaseModel):
    """Per-GT-fault diagnostic."""

    gt_service: str
    gt_fault_kind: FaultKind
    matched_root_cause_index: int | None = None
    status: MatchStatus
    method_match: bool | None = None


class GraphMetrics(BaseModel):
    """Service-level graph comparison vs ground-truth causal_graph.json.

    The agent's service set is the union of every service mentioned across
    root_causes, propagation endpoints, and Network direction pairs. Its edge
    set is the propagation list collapsed to (src, dst) tuples (self-loops
    dropped).
    """

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
    """Three-tier outcome scoring derived from the same per_fault assignment.

    All three tiers share denominators (n_agent_rcs for precision,
    n_gt_faults for recall) — only the credit per matched fault changes:

    - ``service_*`` (most lenient): any non-MISS pairing counts as 1.0, i.e.
      "agent picked the right service even if kind/direction wrong".
    - ``root_cause_partial_*``: HIT=1.0, WRONG_DIRECTION=0.5, WRONG_KIND=0.5,
      MISS=0. Acknowledges that "service + most attributes right" is closer
      to correct than "complete miss".
    - ``root_cause_*`` (strictest): HIT=1.0, everything else=0. The metric
      most papers cite. ``case_correct`` is the boolean version.
    """

    service_precision: float
    service_recall: float
    service_f1: float

    root_cause_partial_precision: float
    root_cause_partial_recall: float
    root_cause_partial_f1: float

    root_cause_precision: float
    root_cause_recall: float
    root_cause_f1: float

    overclaim_rate: float
    per_fault: list[FaultMatchResult] = Field(default_factory=list)
    overclaim_indices: list[int] = Field(default_factory=list)
    case_correct: bool = False


def _norm(name: str | None) -> str:
    """Uniform service-name normalization used by both the matcher and the
    SQL verifier. Lower-case, strip dashes and underscores. No system-specific
    prefix stripping — agents may use various writings, but they must avoid
    inventing names not present in the data.
    """
    if not name:
        return ""
    return name.strip().lower().replace("-", "").replace("_", "")


def _service_eq(a: str | None, b: str | None) -> bool:
    return _norm(a) == _norm(b) and bool(_norm(a))


def _evaluate_pair(rc: RootCauseClaim, gt: GTFault) -> tuple[MatchStatus, bool | None]:
    """Score one (agent_rc, gt_fault) pair without committing — caller picks best."""
    if not _service_eq(rc.service, gt.service):
        return MatchStatus.MISS, None

    if rc.fault_kind != gt.fault_kind:
        return MatchStatus.WRONG_KIND, None

    if gt.fault_kind in NETWORK_KINDS and (gt.direction_src or gt.direction_dst):
        # Skip the direction check when GT itself doesn't carry direction
        # (old-format injection.json — its data.jsonl side-channel only gives
        # us chaos_type + service, not the netem src/dst). In that case the
        # fault is unscorable on direction and we let kind+service alone
        # decide the match — otherwise no agent could ever HIT old-format
        # network cases.
        d = rc.direction
        if d is None:
            return MatchStatus.WRONG_DIRECTION, None
        src_ok = _service_eq(d.src, gt.direction_src)
        dst_ok = _service_eq(d.dst, gt.direction_dst)
        if not (src_ok and dst_ok):
            return MatchStatus.WRONG_DIRECTION, None

    method_match: bool | None = None
    if gt.method:
        method_match = (rc.method or "").strip() == gt.method.strip()

    return MatchStatus.HIT, method_match


_RANK = {
    MatchStatus.HIT: 0,
    MatchStatus.WRONG_DIRECTION: 1,
    MatchStatus.WRONG_KIND: 2,
    MatchStatus.MISS: 3,
}


def compute_outcome(agent: AgentRCAOutput, gt_faults: list[GTFault]) -> OutcomeResult:
    """Greedy assignment: each agent_rc and each gt_fault used at most once.

    Strategy: enumerate all (rc, gt) pairs, sort by tightness (HIT first), then
    consume top-down skipping pairs whose endpoints are already taken. Remaining
    GT faults become MISS; remaining agent rcs become overclaim.
    """
    n_agent = len(agent.root_causes)
    n_gt = len(gt_faults)

    triples: list[tuple[int, int, MatchStatus, bool | None]] = []
    for i, rc in enumerate(agent.root_causes):
        for j, gt in enumerate(gt_faults):
            status, method_match = _evaluate_pair(rc, gt)
            triples.append((i, j, status, method_match))
    triples.sort(key=lambda t: _RANK[t[2]])

    assigned_agent: dict[int, tuple[int, MatchStatus, bool | None]] = {}
    assigned_gt: dict[int, tuple[int, MatchStatus, bool | None]] = {}
    for i, j, status, method_match in triples:
        if status == MatchStatus.MISS:
            break
        if i in assigned_agent or j in assigned_gt:
            continue
        assigned_agent[i] = (j, status, method_match)
        assigned_gt[j] = (i, status, method_match)

    per_fault: list[FaultMatchResult] = []
    for j, gt in enumerate(gt_faults):
        if j in assigned_gt:
            i, status, method_match = assigned_gt[j]
            per_fault.append(
                FaultMatchResult(
                    gt_service=gt.service,
                    gt_fault_kind=gt.fault_kind,
                    matched_root_cause_index=i,
                    status=status,
                    method_match=method_match,
                )
            )
        else:
            per_fault.append(
                FaultMatchResult(
                    gt_service=gt.service,
                    gt_fault_kind=gt.fault_kind,
                    matched_root_cause_index=None,
                    status=MatchStatus.MISS,
                    method_match=None,
                )
            )

    overclaim_indices = [i for i in range(n_agent) if i not in assigned_agent]

    # Three tiers, same denominators (n_agent / n_gt), different per-fault weights.
    n_service_hit = sum(1 for r in per_fault if r.status != MatchStatus.MISS)
    partial_hit_score = sum(_PARTIAL_WEIGHT[r.status] for r in per_fault)
    n_kind_hit = sum(1 for r in per_fault if r.status == MatchStatus.HIT)

    def _prf(hits: float) -> tuple[float, float, float]:
        p = hits / n_agent if n_agent else (1.0 if n_gt == 0 else 0.0)
        r = hits / n_gt if n_gt else (1.0 if n_agent == 0 else 0.0)
        f = (2 * p * r / (p + r)) if (p + r) else 0.0
        return p, r, f

    sp, sr, sf = _prf(float(n_service_hit))
    pp, pr_, pf = _prf(partial_hit_score)
    kp, kr, kf = _prf(float(n_kind_hit))

    overclaim_rate = len(overclaim_indices) / n_agent if n_agent else 0.0
    case_correct = (n_kind_hit == n_gt) and (len(overclaim_indices) == 0) and n_gt > 0

    return OutcomeResult(
        service_precision=sp,
        service_recall=sr,
        service_f1=sf,
        root_cause_partial_precision=pp,
        root_cause_partial_recall=pr_,
        root_cause_partial_f1=pf,
        root_cause_precision=kp,
        root_cause_recall=kr,
        root_cause_f1=kf,
        overclaim_rate=overclaim_rate,
        per_fault=per_fault,
        overclaim_indices=overclaim_indices,
        case_correct=case_correct,
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
    return out


def _agent_edge_set(agent: AgentRCAOutput) -> set[tuple[str, str]]:
    out: set[tuple[str, str]] = set()
    for prop in agent.propagation:
        s, t = _norm(prop.from_), _norm(prop.to)
        if s and t and s != t:
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
    """Service-level node/edge F1 of the agent's claimed graph against the GT.

    Names are normalized with the same rule as the type-aware matcher (lowercased,
    `ts-` stripped, hyphens/underscores removed) so trivial naming variance does
    not show up as missed/hallucinated.
    """
    if gt_graph is None:
        return GraphMetrics(applicable=False)

    agent_nodes = _agent_service_set(agent)
    agent_edges = _agent_edge_set(agent)

    gt_nodes_raw = gt_graph.get_service_nodes()
    gt_edges_raw = gt_graph.get_service_edges()
    gt_nodes = {_norm(s) for s in gt_nodes_raw}
    gt_nodes.discard("")
    gt_edges = {(_norm(s), _norm(t)) for s, t in gt_edges_raw}
    gt_edges = {(s, t) for s, t in gt_edges if s and t and s != t}

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
