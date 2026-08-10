"""Set-match layer: pair agent root_causes against GT faults.

Match is a greedy 1-1 multiset assignment over (service, fault_kind) pairs.
Each agent claim and each GT fault is consumed at most once; HIT pairs win
over WRONG_KIND so a correct claim is never shadowed.

Two scoring views are reported on every case:

    strict (HIT only):
        precision / recall / f1 / exact_match
        — both service and kind must match.

    service-only (HIT + WRONG_KIND treated as correct):
        service_precision / service_recall / service_f1 / service_exact_match
        — the agent identified the right service; kind may be off.

The cross-axis ``fault_kind_accuracy = HIT / (HIT + WRONG_KIND)`` reports
how often the agent got the kind right *given* it got the service right;
it is ``None`` when the denominator is zero (no service-correct claims to
grade), and the aggregator excludes those cases from the benchmark mean.
"""

from __future__ import annotations

from enum import Enum

from pydantic import BaseModel, Field

from ._normalize import service_eq
from .agent_output import AgentRCAOutput, RootCauseClaim
from .fault_kind import NETWORK_KINDS, FaultKind
from .ground_truth import GTFault


class MatchStatus(str, Enum):
    HIT = "HIT"
    WRONG_KIND = "WRONG_KIND"
    MISS = "MISS"


class FaultMatchResult(BaseModel):
    gt_service: str
    gt_fault_kind: FaultKind
    matched_root_cause_index: int | None = None
    status: MatchStatus


class OutcomeResult(BaseModel):
    """Set-match outcome with both strict and service-only views."""

    # Strict: HIT only (service AND kind must match).
    precision: float
    recall: float
    f1: float
    exact_match: bool

    # Service-only: HIT + WRONG_KIND both count as correct.
    service_precision: float
    service_recall: float
    service_f1: float
    service_exact_match: bool

    # Kind-conditional-on-service: HIT / (HIT + WRONG_KIND). None when denom=0.
    fault_kind_accuracy: float | None
    kind_accuracy_denom: int

    per_fault: list[FaultMatchResult] = Field(default_factory=list)
    overclaim_indices: list[int] = Field(default_factory=list)


# Fault-kind equivalence classes for HIT determination. Two kinds in the same
# class differ only in a feature the agent cannot observe from the released
# telemetry, so the matcher treats them as the same answer.
#
# (1) `pod_failure` vs `pod_unavailable` differ only in whether the
#     unavailability extends past the injection window. The window length is
#     GT-side knowledge; from telemetry the agent sees a service that stopped
#     emitting and may or may not resume within the abnormal slice. Different
#     models calibrate the threshold in opposite directions, so the
#     distinction empirically tracks model bias rather than diagnostic skill.
#
# (2) `network_loss` vs `network_partition` are mechanistically distinct
#     (tc netem probabilistic drop vs iptables 100% DROP), but the
#     reasoning chains that could separate them from our parquets break
#     above ~60% loss rate: success-count signature converges, JDBC
#     "Connection refused" / "Communications link failure" overlap, and
#     long-tail duration distributions merge. See
#     ``docs/openrca-2-lite.md`` §3.5 for the empirical rationale.
_KIND_EQUIV_GROUPS: tuple[frozenset[FaultKind], ...] = (
    frozenset({FaultKind.POD_FAILURE, FaultKind.POD_UNAVAILABLE}),
    frozenset({FaultKind.NETWORK_LOSS, FaultKind.NETWORK_PARTITION}),
)


def _kind_eq(a: FaultKind, b: FaultKind) -> bool:
    if a == b:
        return True
    for group in _KIND_EQUIV_GROUPS:
        if a in group and b in group:
            return True
    return False


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


def _evaluate_pair(rc: RootCauseClaim, gt: GTFault) -> MatchStatus:
    if not any(service_eq(rc.service, c) for c in _gt_service_candidates(gt)):
        return MatchStatus.MISS
    if not _kind_eq(rc.fault_kind, gt.fault_kind):
        return MatchStatus.WRONG_KIND
    return MatchStatus.HIT


_RANK = {
    MatchStatus.HIT: 0,
    MatchStatus.WRONG_KIND: 1,
    MatchStatus.MISS: 2,
}


def _prf(n_correct: int, n_pred: int, n_gt: int) -> tuple[float, float, float]:
    if n_pred:
        precision = n_correct / n_pred
    else:
        precision = 1.0 if n_gt == 0 else 0.0
    if n_gt:
        recall = n_correct / n_gt
    else:
        recall = 1.0 if n_pred == 0 else 0.0
    f1 = (2 * precision * recall / (precision + recall)) if (precision + recall) else 0.0
    return precision, recall, f1


def compute_outcome(agent: AgentRCAOutput, gt_faults: list[GTFault]) -> OutcomeResult:
    """Greedy 1-1 assignment of agent root_causes to GT faults.

    Pairs are taken in HIT-first order so a HIT never gets shadowed by a
    WRONG_KIND that consumes the same agent rc.
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
    n_service_correct = n_hit + n_wrong_kind

    precision, recall, f1 = _prf(n_hit, n_agent, n_gt)
    exact_match = (n_hit == n_gt) and (n_hit == n_agent) and n_gt > 0

    service_precision, service_recall, service_f1 = _prf(n_service_correct, n_agent, n_gt)
    service_exact_match = (n_service_correct == n_gt) and (n_service_correct == n_agent) and n_gt > 0

    kind_denom = n_service_correct
    kind_accuracy: float | None = (n_hit / kind_denom) if kind_denom > 0 else None

    return OutcomeResult(
        precision=precision,
        recall=recall,
        f1=f1,
        exact_match=exact_match,
        service_precision=service_precision,
        service_recall=service_recall,
        service_f1=service_f1,
        service_exact_match=service_exact_match,
        fault_kind_accuracy=kind_accuracy,
        kind_accuracy_denom=kind_denom,
        per_fault=per_fault,
        overclaim_indices=overclaim_indices,
    )
