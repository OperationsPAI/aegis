"""Top-level evaluator — orchestrate set-match, graph-match, evidence.

Pipeline per case:
    1. Parse agent output JSON → AgentRCAOutput
    2. Extract GT faults from injection.json → list[GTFault]
    3. Set match (strict + service-only) via set_match.compute_outcome
    4. Graph match + path reachability via graph_match
    5. Verify each evidence SQL via DuckDB → sql_executable_rate, per_evidence
    6. Per-evidence LLM judge → evidence_support_rate
"""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from typing import Any

from openai import AsyncOpenAI
from pydantic import ValidationError

from .agent_output import AgentRCAOutput, Evidence
from .causal_graph import CausalGraph
from .chain_judge import EvidenceJudgeResult, evidence_support
from .graph_match import compute_graph_metrics, compute_path_reachability
from .ground_truth import GTContext, extract_gt_faults
from .result import EvaluationResult, PerEvidenceRecord
from .set_match import MatchStatus, compute_outcome
from .sql_verify import EvidenceStatus, EvidenceVerifyResult, verify_evidence


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


def _zero_result(parse_error: str, notes: list[str] | None = None) -> EvaluationResult:
    return EvaluationResult(
        precision=0.0,
        recall=0.0,
        f1=0.0,
        exact_match=False,
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


async def evaluate(
    agent_output_raw: str | dict[str, Any] | None,
    injection: dict[str, Any],
    parquet_dir: str | Path,
    gt_graph: CausalGraph | None = None,
    llm_client: AsyncOpenAI | None = None,
    judge_model: str = "gpt-4o-mini",
    case_name: str | None = None,
) -> EvaluationResult:
    parquet_dir = Path(parquet_dir)

    agent, parse_error = _parse_agent(agent_output_raw)
    if agent is None:
        return _zero_result(parse_error or "agent output unparseable")

    gt_ctx: GTContext = extract_gt_faults(injection, case_name=case_name)
    if not gt_ctx.faults:
        return _zero_result("no GT faults extractable from injection.json")

    outcome = compute_outcome(agent, gt_ctx.faults)
    graph = compute_graph_metrics(agent, gt_graph)
    path_reachable = compute_path_reachability(agent, outcome, gt_graph)
    any_hit = any(m.status == MatchStatus.HIT for m in outcome.per_fault)
    # Kind-agnostic siblings: HIT and WRONG_KIND both mean "service was right".
    # any_service_hit ≥ any_root_cause_hit; all_service_hit ≥ (recall == 1.0).
    _service_correct = (MatchStatus.HIT, MatchStatus.WRONG_KIND)
    any_service = any(m.status in _service_correct for m in outcome.per_fault)
    all_service = bool(outcome.per_fault) and all(m.status in _service_correct for m in outcome.per_fault)

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
    n_failed = (n_ev - n_judged) if (judge_inputs and llm_client is not None) else 0

    if n_judged > 0:
        evidence_support_rate: float | None = n_supported / n_judged
    else:
        # No judge ran (no client, or no evidence at all, or all judges failed):
        # per contract the case scores 0 in the benchmark mean; n_failed exposes
        # the transient-judge-error case for diagnostics.
        evidence_support_rate = 0.0

    return EvaluationResult(
        precision=outcome.precision,
        recall=outcome.recall,
        f1=outcome.f1,
        exact_match=outcome.exact_match,
        service_precision=outcome.service_precision,
        service_recall=outcome.service_recall,
        service_f1=outcome.service_f1,
        service_exact_match=outcome.service_exact_match,
        fault_kind_accuracy=outcome.fault_kind_accuracy,
        kind_accuracy_denom=outcome.kind_accuracy_denom,
        sql_executable_rate=sql_executable_rate,
        evidence_support_rate=evidence_support_rate,
        path_reachability=path_reachable,
        any_root_cause_hit=any_hit,
        any_service_hit=any_service,
        all_service_hit=all_service,
        node_precision=graph.node_precision,
        node_recall=graph.node_recall,
        node_f1=graph.node_f1,
        edge_precision=graph.edge_precision,
        edge_recall=graph.edge_recall,
        edge_f1=graph.edge_f1,
        per_fault=outcome.per_fault,
        overclaim_indices=outcome.overclaim_indices,
        per_evidence=per_evidence,
        graph_metrics=graph,
        n_evidence=n_ev,
        n_evidence_judged=n_judged,
        n_evidence_judge_failed=n_failed,
    )
