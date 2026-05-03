"""LLM-as-judge for causal-chain coherence.

Inputs the agent's structured output, the executed SQL preview, and the GT
causal_graph; asks the LLM whether the agent's claims are mutually coherent
AND consistent with the GT graph. Returns a 0–1 coherence score plus a short
explanation. The score is informational; the deterministic outcome metrics
remain primary.
"""
from __future__ import annotations

import json
from typing import Any

from openai import AsyncOpenAI
from pydantic import BaseModel, Field

from ..causal_graph import CausalGraph
from .schema import AgentRCAOutput
from .sql_verify import EvidenceStatus, EvidenceVerifyResult


class ChainJudgeResult(BaseModel):
    score: float = Field(0.0, ge=0.0, le=1.0)
    reasoning: str = ""
    raw_response: str | None = None


_JUDGE_PROMPT = """You are scoring whether an RCA agent's reasoning chain is coherent
and consistent with the ground-truth causal graph.

You will be given:
  • the agent's structured output (root_causes + propagation edges, each with
    an evidence SQL and a natural-language claim);
  • a preview (first rows) of what each SQL actually returned when re-executed;
  • the ground-truth causal_graph at service granularity.

Score on a single 0.0–1.0 scale:
  1.0 — every claim is internally consistent, the evidence rows demonstrably
        support it, AND the propagation chain is reachable in the GT graph.
  0.0 — the chain is incoherent, or contradicts the GT graph, or the SQL
        results disprove the claims.
  Intermediate values are allowed; calibrate so 0.5 means "half the chain is
  supported."

You must NOT use any prior knowledge of which case this is. Judge only what is
shown.

Respond with strict JSON: {"score": <float 0..1>, "reasoning": "<≤80 words>"}.

== Agent output ==
{agent_output}

== Evidence executions (status + sample rows) ==
{evidence_block}

== Ground truth causal graph (service-level) ==
{gt_block}
"""


def _format_evidence_block(
    agent: AgentRCAOutput, results: list[tuple[str, EvidenceVerifyResult]]
) -> str:
    """Pair each evidence with its verifier result; truncate sample rows."""
    if not results:
        return "(no evidence)"
    lines: list[str] = []
    for label, vr in results:
        head = f"[{label}] status={vr.status.value} rows={vr.row_count}"
        if vr.error:
            lines.append(f"{head} error={vr.error[:200]}")
            continue
        lines.append(head)
        for row in vr.sample_rows[:3]:
            shrunk = {k: (str(v)[:80] if v is not None else None) for k, v in row.items()}
            lines.append(f"    {json.dumps(shrunk, ensure_ascii=False, default=str)}")
    return "\n".join(lines)


def _format_gt_block(gt_graph: CausalGraph | None) -> str:
    if gt_graph is None:
        return "(no ground-truth graph available)"
    nodes = sorted(gt_graph.get_service_nodes())
    edges = sorted(gt_graph.get_service_edges())
    roots = sorted(gt_graph.get_root_cause_services())
    alarms = sorted(gt_graph.get_alarm_services())
    return (
        f"services: {nodes}\n"
        f"edges: {edges}\n"
        f"root_cause_services: {roots}\n"
        f"alarm_services: {alarms}"
    )


async def chain_coherence(
    agent: AgentRCAOutput,
    evidence_results: list[tuple[str, EvidenceVerifyResult]],
    gt_graph: CausalGraph | None,
    llm_client: AsyncOpenAI | None,
    model: str = "gpt-4o-mini",
) -> ChainJudgeResult:
    if llm_client is None:
        ok = sum(1 for _, vr in evidence_results if vr.status == EvidenceStatus.OK)
        total = len(evidence_results)
        fallback = ok / total if total else 0.0
        return ChainJudgeResult(score=fallback, reasoning="(no llm_client; fallback = sql_executable_rate)")

    prompt = _JUDGE_PROMPT.format(
        agent_output=agent.model_dump_json(by_alias=True, indent=2),
        evidence_block=_format_evidence_block(agent, evidence_results),
        gt_block=_format_gt_block(gt_graph),
    )

    try:
        response = await llm_client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": prompt}],
            temperature=0,
            max_tokens=400,
            response_format={"type": "json_object"},
        )
    except Exception as exc:
        return ChainJudgeResult(score=0.0, reasoning=f"(judge error: {exc!s:.200})")

    content = response.choices[0].message.content or ""
    try:
        parsed = json.loads(content)
    except json.JSONDecodeError:
        return ChainJudgeResult(score=0.0, reasoning="(judge returned non-JSON)", raw_response=content)
    score = float(parsed.get("score") or 0.0)
    score = max(0.0, min(1.0, score))
    reasoning = str(parsed.get("reasoning") or "")
    return ChainJudgeResult(score=score, reasoning=reasoning, raw_response=content)
