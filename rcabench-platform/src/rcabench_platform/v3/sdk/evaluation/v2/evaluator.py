"""Top-level v2 evaluator.

Pipeline per case:
    1. Parse agent output JSON      → AgentRCAOutput
    2. Extract GT faults             → list[GTFault] (+ time window)
    3. Type-aware match              → root_cause_f1, overclaim_rate, per_fault
    4. Verify each evidence SQL      → sql_executable_rate, per_evidence
    5. LLM-as-judge over chain        → chain_coherence
    6. Compose 4 numbers + headline.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from openai import AsyncOpenAI
from pydantic import BaseModel, Field, ValidationError

from ..causal_graph import CausalGraph
from .chain_judge import ChainJudgeResult, chain_coherence
from .ground_truth import GTContext, extract_gt_faults
from .matcher import FaultMatchResult, GraphMetrics, OutcomeResult, compute_graph_metrics, compute_outcome
from .schema import AgentRCAOutput
from .sql_verify import EvidenceStatus, EvidenceVerifyResult, verify_evidence


class PerEvidenceRecord(BaseModel):
    label: str
    kind: str
    sql: str
    claim: str
    status: EvidenceStatus
    error: str | None = None
    row_count: int = 0


class EvaluationResultV2(BaseModel):
    """Headline scores + every diagnostic needed to debug them.

    Headline numbers (per case):
      - root_cause_f1:     type-aware match against engine_config faults
      - overclaim_rate:    agent root_causes that did not align to any GT fault
      - sql_executable_rate: evidence SQL that ran, returned rows, and aligned
      - chain_coherence:   LLM judge over (claims + SQL preview); blind to GT
      - node_f1 / edge_f1: agent's claimed graph vs GT causal_graph (service-level)

    `headline = root_cause_f1 × sql_executable_rate × chain_coherence`

    ``chain_coherence`` and ``headline`` are ``None`` when the LLM judge call
    itself failed (e.g. network outage). Aggregators are expected to drop
    those samples from the headline average rather than treat them as 0.0,
    so a transient outage doesn't infect the case-level score.
    """

    root_cause_f1: float
    root_cause_partial_f1: float = 0.0
    overclaim_rate: float
    sql_executable_rate: float
    chain_coherence: float | None

    node_f1: float = 0.0
    edge_f1: float = 0.0

    headline: float | None = Field(..., description="root_cause_f1 × sql_executable_rate × chain_coherence")
    case_correct: bool = False

    service_precision: float = 0.0
    service_recall: float = 0.0
    service_f1: float = 0.0

    root_cause_partial_precision: float = 0.0
    root_cause_partial_recall: float = 0.0

    root_cause_precision: float = 0.0
    root_cause_recall: float = 0.0
    node_precision: float = 0.0
    node_recall: float = 0.0
    edge_precision: float = 0.0
    edge_recall: float = 0.0

    per_fault: list[FaultMatchResult] = Field(default_factory=list)
    overclaim_indices: list[int] = Field(default_factory=list)
    per_evidence: list[PerEvidenceRecord] = Field(default_factory=list)
    graph_metrics: GraphMetrics | None = None

    chain_judge: ChainJudgeResult | None = None

    parse_error: str | None = None
    notes: list[str] = Field(default_factory=list)


def _parse_agent(raw: str | dict[str, Any] | None) -> tuple[AgentRCAOutput | None, str | None]:
    if raw is None:
        return None, "agent output is empty"
    try:
        data = json.loads(raw) if isinstance(raw, str) else raw
    except json.JSONDecodeError as exc:
        return None, f"JSON decode error: {exc}"
    try:
        return AgentRCAOutput.model_validate(data), None
    except ValidationError as exc:
        return None, f"schema validation error: {exc}"


def _zero_result(parse_error: str, notes: list[str] | None = None) -> EvaluationResultV2:
    return EvaluationResultV2(
        root_cause_f1=0.0,
        root_cause_partial_f1=0.0,
        overclaim_rate=0.0,
        sql_executable_rate=0.0,
        chain_coherence=0.0,
        headline=0.0,
        case_correct=False,
        parse_error=parse_error,
        notes=notes or [],
    )


async def evaluate_v2(
    agent_output_raw: str | dict[str, Any] | None,
    injection: dict[str, Any],
    parquet_dir: str | Path,
    gt_graph: CausalGraph | None = None,
    llm_client: AsyncOpenAI | None = None,
    judge_model: str = "gpt-4o-mini",
    case_name: str | None = None,
) -> EvaluationResultV2:
    parquet_dir = Path(parquet_dir)

    agent, parse_error = _parse_agent(agent_output_raw)
    if agent is None:
        return _zero_result(parse_error or "agent output unparseable")

    gt_ctx: GTContext = extract_gt_faults(injection, case_name=case_name)
    if not gt_ctx.faults:
        return _zero_result("no GT faults extractable from injection.json")

    outcome: OutcomeResult = compute_outcome(agent, gt_ctx.faults)
    graph: GraphMetrics = compute_graph_metrics(agent, gt_graph)

    per_evidence: list[PerEvidenceRecord] = []
    sql_evidence_results: list[tuple[str, EvidenceVerifyResult]] = []

    for ri, rc in enumerate(agent.root_causes):
        for ei, ev in enumerate(rc.evidence):
            label = f"rc[{ri}].ev[{ei}]"
            vr = verify_evidence(evidence=ev, parquet_dir=parquet_dir)
            per_evidence.append(
                PerEvidenceRecord(
                    label=label,
                    kind=ev.kind.value,
                    sql=ev.sql,
                    claim=ev.claim,
                    status=vr.status,
                    error=vr.error,
                    row_count=vr.row_count,
                )
            )
            sql_evidence_results.append((label, vr))

    for pi, prop in enumerate(agent.propagation):
        for ei, ev in enumerate(prop.evidence):
            label = f"prop[{pi}].ev[{ei}]"
            vr = verify_evidence(evidence=ev, parquet_dir=parquet_dir)
            per_evidence.append(
                PerEvidenceRecord(
                    label=label,
                    kind=ev.kind.value,
                    sql=ev.sql,
                    claim=ev.claim,
                    status=vr.status,
                    error=vr.error,
                    row_count=vr.row_count,
                )
            )
            sql_evidence_results.append((label, vr))

    n_ev = len(per_evidence)
    n_ok = sum(1 for r in per_evidence if r.status == EvidenceStatus.OK)
    sql_executable_rate = n_ok / n_ev if n_ev else 0.0

    judge_result = await chain_coherence(
        agent=agent,
        evidence_results=sql_evidence_results,
        llm_client=llm_client,
        model=judge_model,
        case_name=case_name,
    )

    if judge_result.score is None:
        headline: float | None = None
    else:
        headline = outcome.root_cause_f1 * sql_executable_rate * judge_result.score

    return EvaluationResultV2(
        root_cause_f1=outcome.root_cause_f1,
        root_cause_partial_f1=outcome.root_cause_partial_f1,
        overclaim_rate=outcome.overclaim_rate,
        sql_executable_rate=sql_executable_rate,
        chain_coherence=judge_result.score,
        node_f1=graph.node_f1,
        edge_f1=graph.edge_f1,
        headline=headline,
        case_correct=outcome.case_correct,
        service_precision=outcome.service_precision,
        service_recall=outcome.service_recall,
        service_f1=outcome.service_f1,
        root_cause_partial_precision=outcome.root_cause_partial_precision,
        root_cause_partial_recall=outcome.root_cause_partial_recall,
        root_cause_precision=outcome.root_cause_precision,
        root_cause_recall=outcome.root_cause_recall,
        node_precision=graph.node_precision,
        node_recall=graph.node_recall,
        edge_precision=graph.edge_precision,
        edge_recall=graph.edge_recall,
        per_fault=outcome.per_fault,
        overclaim_indices=outcome.overclaim_indices,
        per_evidence=per_evidence,
        graph_metrics=graph,
        chain_judge=judge_result,
    )
