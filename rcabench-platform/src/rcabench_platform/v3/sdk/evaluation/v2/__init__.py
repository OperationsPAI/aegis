"""Top-down RCA evaluation v2 (single-tier match + per-evidence judge).

Public API:
    AgentRCAOutput / RootCauseClaim / Evidence / PropagationClaim — agent contract
    FaultKind, map_chaos_type — controlled fault-kind vocabulary
    extract_gt_faults — pull list[GTFault] from injection.json
    evaluate_v2 — async top-level scorer returning EvaluationResultV2
    get_agent_contract_prompt — canonical enum+schema text integrators splice
        into their own system prompt so the contract version follows the SDK
"""

from .agent_contract import get_agent_contract_prompt
from .chain_judge import EvidenceJudgeResult, evidence_support
from .evaluator import EvaluationResultV2, PerEvidenceRecord, evaluate_v2
from .fault_kind import FaultKind, map_chaos_type
from .ground_truth import GTFault, extract_gt_faults
from .matcher import (
    FaultMatchResult,
    GraphMetrics,
    MatchStatus,
    OutcomeResult,
    compute_graph_metrics,
    compute_outcome,
)
from .schema import (
    AgentRCAOutput,
    Direction,
    Evidence,
    EvidenceKind,
    PropagationClaim,
    RootCauseClaim,
)
from .sql_verify import EvidenceStatus, EvidenceVerifyResult, verify_evidence

__all__ = [
    "AgentRCAOutput",
    "Direction",
    "Evidence",
    "EvidenceKind",
    "PropagationClaim",
    "RootCauseClaim",
    "FaultKind",
    "map_chaos_type",
    "GTFault",
    "extract_gt_faults",
    "MatchStatus",
    "FaultMatchResult",
    "GraphMetrics",
    "OutcomeResult",
    "compute_outcome",
    "compute_graph_metrics",
    "EvidenceVerifyResult",
    "EvidenceStatus",
    "verify_evidence",
    "EvidenceJudgeResult",
    "evidence_support",
    "EvaluationResultV2",
    "PerEvidenceRecord",
    "evaluate_v2",
    "get_agent_contract_prompt",
]
