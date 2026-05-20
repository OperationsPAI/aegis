"""Layered RCA evaluation.

See ``EVALUATION_CONTRACT.md`` for the full per-case scoring contract.

Layers:
    * ``ranking``        — Top-k / MRR over ranked candidate lists (traditional algos)
    * ``set_match``      — (service, fault_kind) multiset match; strict + service-only
    * ``graph_match``    — node/edge F1 + path reachability vs GT causal_graph
    * ``sql_verify``     — DuckDB executability for agent SQL evidence
    * ``chain_judge``    — per-evidence LLM judge (claim ↔ rows + chain coherence)
    * ``evaluator``      — orchestrates the layers into one ``EvaluationResult``
"""

from .agent_contract import get_agent_contract_prompt, get_fault_kind_disambiguation
from .agent_output import (
    AgentRCAOutput,
    Direction,
    Evidence,
    EvidenceKind,
    PropagationClaim,
    RootCauseClaim,
)
from .causal_graph import AgentGraph, CausalEdge, CausalGraph, CausalNode, GroundTruthGraph
from .chain_judge import EvidenceJudgeResult, evidence_support
from .evaluator import evaluate
from .fault_kind import FaultKind, map_chaos_type
from .graph_match import GraphMetrics, compute_graph_metrics, compute_path_reachability
from .ground_truth import GTFault, extract_gt_faults
from .result import EvaluationResult, PerEvidenceRecord
from .set_match import FaultMatchResult, MatchStatus, OutcomeResult, compute_outcome
from .sql_verify import EvidenceStatus, EvidenceVerifyResult, verify_evidence

__all__ = [
    # graph data
    "AgentGraph",
    "CausalEdge",
    "CausalGraph",
    "CausalNode",
    "GroundTruthGraph",
    # agent contract
    "AgentRCAOutput",
    "Direction",
    "Evidence",
    "EvidenceKind",
    "PropagationClaim",
    "RootCauseClaim",
    "get_agent_contract_prompt",
    "get_fault_kind_disambiguation",
    # fault vocabulary + GT
    "FaultKind",
    "map_chaos_type",
    "GTFault",
    "extract_gt_faults",
    # set match
    "MatchStatus",
    "FaultMatchResult",
    "OutcomeResult",
    "compute_outcome",
    # graph match
    "GraphMetrics",
    "compute_graph_metrics",
    "compute_path_reachability",
    # evidence
    "EvidenceStatus",
    "EvidenceVerifyResult",
    "verify_evidence",
    "EvidenceJudgeResult",
    "evidence_support",
    # top-level
    "EvaluationResult",
    "PerEvidenceRecord",
    "evaluate",
]
