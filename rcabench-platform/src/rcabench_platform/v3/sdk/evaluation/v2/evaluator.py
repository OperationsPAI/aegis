"""Top-level v2 evaluator (single-tier match + per-evidence judge).

Pipeline per case:
    1. Parse agent output JSON           → AgentRCAOutput
    2. Extract GT faults                  → list[GTFault]
    3. Single-tier (service, fault_kind) multiset match
                                          → precision/recall/f1, exact_match,
                                            fault_kind_accuracy
    4. Service-level node_f1 / edge_f1 vs GT causal_graph
    5. Verify each evidence SQL via DuckDB
                                          → sql_executable_rate, per_evidence
    6. Per-evidence LLM judge             → evidence_support_rate
"""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from typing import Any

from openai import AsyncOpenAI
from pydantic import BaseModel, Field, ValidationError

from ..causal_graph import CausalGraph
from .chain_judge import EvidenceJudgeResult, evidence_support
from .ground_truth import GTContext, extract_gt_faults
from .matcher import (
    FaultMatchResult,
    GraphMetrics,
    OutcomeResult,
    compute_graph_metrics,
    compute_outcome,
    compute_path_reachability,
)
from .schema import AgentRCAOutput, Evidence
from .sql_verify import EvidenceStatus, EvidenceVerifyResult, verify_evidence


class PerEvidenceRecord(BaseModel):
    label: str
    kind: str
    sql: str
    claim: str
    status: EvidenceStatus
    error: str | None = None
    row_count: int = 0
    supported: bool | None = None
    judge_reasoning: str = ""


class EvaluationResultV2(BaseModel):
    """Per-case scoring under the simplified contract.

    Headline numbers:
      - precision / recall / f1 — over the (service, fault_kind) multiset
      - exact_match              — boolean, multiset equality after match
      - fault_kind_accuracy      — HIT / (HIT + WRONG_KIND); None when denom=0
      - sql_executable_rate      — mechanical (DuckDB returns rows)
      - evidence_support_rate    — supported / judged among per-evidence judges;
                                   None when no evidence was judged
      - node_f1 / edge_f1        — service-level graph alignment

    No multiplicative headline. Each axis is reported independently so the
    aggregator can show which one is failing without composing them.
    """

    precision: float
    recall: float
    f1: float
    exact_match: bool

    fault_kind_accuracy: float | None
    kind_accuracy_denom: int

    sql_executable_rate: float
    evidence_support_rate: float | None

    path_reachability: bool | None = None

    node_f1: float = 0.0
    edge_f1: float = 0.0
    node_precision: float = 0.0
    node_recall: float = 0.0
    edge_precision: float = 0.0
    edge_recall: float = 0.0

    per_fault: list[FaultMatchResult] = Field(default_factory=list)
    overclaim_indices: list[int] = Field(default_factory=list)
    per_evidence: list[PerEvidenceRecord] = Field(default_factory=list)
    graph_metrics: GraphMetrics | None = None

    n_evidence: int = 0
    n_evidence_judged: int = 0
    n_evidence_judge_failed: int = 0

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
        precision=0.0,
        recall=0.0,
        f1=0.0,
        exact_match=False,
        fault_kind_accuracy=None,
        kind_accuracy_denom=0,
        sql_executable_rate=0.0,
        evidence_support_rate=None,
        parse_error=parse_error,
        notes=notes or [],
    )


def _summarize_chain(agent: AgentRCAOutput) -> str:
    """Compact text view of the chain for the per-evidence judge prompt."""
    lines: list[str] = []
    for i, rc in enumerate(agent.root_causes):
        bits = [f"service={rc.service}", f"fault_kind={rc.fault_kind.value}"]
        if rc.direction:
            bits.append(f"direction={rc.direction.src}->{rc.direction.dst}")
        if rc.method:
            bits.append(f"method={rc.method}")
        lines.append(f"  rc[{i}]: " + " ".join(bits))
    for i, prop in enumerate(agent.propagation):
        lines.append(f"  prop[{i}]: {prop.from_} -> {prop.to}")
    return "\n".join(lines) if lines else "  (empty chain)"


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
    path_reachable: bool | None = compute_path_reachability(agent, outcome, gt_graph)

    per_evidence: list[PerEvidenceRecord] = []
    judge_inputs: list[tuple[int, str, str, Evidence, EvidenceVerifyResult]] = []

    for ri, rc in enumerate(agent.root_causes):
        location = f"root_cause[{ri}] service={rc.service} fault_kind={rc.fault_kind.value}"
        for ei, ev in enumerate(rc.evidence):
            label = f"rc[{ri}].ev[{ei}]"
            vr = verify_evidence(evidence=ev, parquet_dir=parquet_dir)
            rec_idx = len(per_evidence)
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
            judge_inputs.append((rec_idx, label, location, ev, vr))

    for pi, prop in enumerate(agent.propagation):
        location = f"propagation[{pi}] {prop.from_} -> {prop.to}"
        for ei, ev in enumerate(prop.evidence):
            label = f"prop[{pi}].ev[{ei}]"
            vr = verify_evidence(evidence=ev, parquet_dir=parquet_dir)
            rec_idx = len(per_evidence)
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
            judge_inputs.append((rec_idx, label, location, ev, vr))

    n_ev = len(per_evidence)
    n_ok = sum(1 for r in per_evidence if r.status == EvidenceStatus.OK)
    sql_executable_rate = n_ok / n_ev if n_ev else 0.0

    # Per-evidence judge fan-out (concurrent within case).
    if judge_inputs and llm_client is not None:
        chain_summary = _summarize_chain(agent)

        async def _run(
            idx: int, label: str, location: str, ev: Evidence, vr: EvidenceVerifyResult
        ) -> tuple[int, EvidenceJudgeResult]:
            try:
                jr = await evidence_support(
                    chain_summary=chain_summary,
                    location=location,
                    label=label,
                    evidence=ev,
                    verify_result=vr,
                    llm_client=llm_client,
                    model=judge_model,
                    case_name=case_name,
                )
            except Exception as exc:
                jr = EvidenceJudgeResult(supported=None, reasoning=f"(judge error: {exc!s:.200})")
            return idx, jr

        results = await asyncio.gather(*(_run(*t) for t in judge_inputs))
        for idx, jr in results:
            rec = per_evidence[idx]
            rec.supported = jr.supported
            rec.judge_reasoning = jr.reasoning

    n_supported = sum(1 for r in per_evidence if r.supported is True)
    n_unsupported = sum(1 for r in per_evidence if r.supported is False)
    n_judged = n_supported + n_unsupported
    # judge_failed = evidences that went into the judge but came back None
    # (transient outage etc.). When no judge ran at all (no client), it's 0.
    n_failed = (n_ev - n_judged) if (judge_inputs and llm_client is not None) else 0

    if n_judged > 0:
        evidence_support_rate: float | None = n_supported / n_judged
    elif n_ev == 0:
        # No evidence at all — caller will see zero_evidence in the per-case
        # diagnostic. Score is 0 (nothing to support).
        evidence_support_rate = 0.0
    else:
        # Evidence exists but no judge ran (no client) or all judges failed.
        # Per README: case scores 0 in benchmark mean; judge_failed exposed.
        evidence_support_rate = 0.0

    return EvaluationResultV2(
        precision=outcome.precision,
        recall=outcome.recall,
        f1=outcome.f1,
        exact_match=outcome.exact_match,
        fault_kind_accuracy=outcome.fault_kind_accuracy,
        kind_accuracy_denom=outcome.kind_accuracy_denom,
        sql_executable_rate=sql_executable_rate,
        evidence_support_rate=evidence_support_rate,
        path_reachability=path_reachable,
        node_f1=graph.node_f1,
        edge_f1=graph.edge_f1,
        node_precision=graph.node_precision,
        node_recall=graph.node_recall,
        edge_precision=graph.edge_precision,
        edge_recall=graph.edge_recall,
        per_fault=outcome.per_fault,
        overclaim_indices=outcome.overclaim_indices,
        per_evidence=per_evidence,
        graph_metrics=graph,
        n_evidence=n_ev,
        n_evidence_judged=n_judged,
        n_evidence_judge_failed=n_failed,
    )
