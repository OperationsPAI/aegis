"""LLM-as-judge for evidence–claim coherence.

The judge looks ONLY at internal consistency: does each natural-language
claim hold up against the rows the evidence SQL actually returned? It is
deliberately blind to the ground-truth causal graph — GT alignment is
already covered by the deterministic ``root_cause_f1`` / ``node_f1`` /
``edge_f1`` metrics, so re-judging it here would duplicate work and inject
LLM noise into the headline.

Returns a 0–1 score (or ``None`` when the judge call itself failed, e.g.
network outage after retries are exhausted) plus a short explanation.
``None`` lets aggregators distinguish "couldn't judge" from "judged poorly"
instead of conflating both as 0.0.
"""

from __future__ import annotations

import json

from openai import AsyncOpenAI
from pydantic import BaseModel, Field

from .schema import AgentRCAOutput
from .sql_verify import EvidenceVerifyResult


class ChainJudgeResult(BaseModel):
    score: float | None = Field(default=None, ge=0.0, le=1.0)
    """0–1 coherence score, or None when the judge call itself failed."""
    reasoning: str = ""
    raw_response: str | None = None


_JUDGE_PROMPT = """You are scoring whether an RCA agent's reasoning chain is internally
coherent: does each natural-language claim hold up against the rows the
evidence SQL actually returned when re-executed?

You will be given:
  • the agent's structured output (root_causes + propagation edges, each with
    an evidence SQL and a natural-language claim);
  • a preview (sample rows) of what each SQL actually returned.

Score on a single 0.0–1.0 scale:
  1.0 — every claim is plausibly supported by the SQL rows shown, and the
        chain is internally consistent (claimed root cause feeds the
        claimed propagation, etc.).
  0.0 — the chain is incoherent, or the SQL results clearly contradict the
        claims (e.g. the agent says "p99 latency spiked" but the rows show
        no such pattern, or the propagation edge has no supporting
        evidence at all).
  Intermediate values are allowed; calibrate so 0.5 means "half the chain
  is supported, the other half is asserted without backing rows."

You are NOT given the ground-truth answer. Do not try to infer which case
this is or whether the agent's named services match a "correct" set —
that is judged separately. Your job is only claim-vs-data consistency.

Respond with strict JSON: {{"score": <float 0..1>, "reasoning": "<=80 words>"}}.

== Agent output ==
{agent_output}

== Evidence executions (status + sample rows) ==
{evidence_block}
"""


def _format_evidence_block(agent: AgentRCAOutput, results: list[tuple[str, EvidenceVerifyResult]]) -> str:
    """Pair each evidence with its verifier result.

    Shows up to 5 rows per evidence and 200 chars per cell — wider than the
    older 3-row × 80-char preview so claims like "p99 increased N×" or
    "error count spiked" actually have enough signal in the preview to be
    judged. The trade-off is a bigger prompt; cases with many evidence rows
    will lean harder on the model's context window.
    """
    if not results:
        return "(no evidence)"
    lines: list[str] = []
    for label, vr in results:
        head = f"[{label}] status={vr.status.value} rows={vr.row_count}"
        if vr.error:
            lines.append(f"{head} error={vr.error[:200]}")
            continue
        lines.append(head)
        for row in vr.sample_rows[:5]:
            shrunk = {k: (str(v)[:200] if v is not None else None) for k, v in row.items()}
            lines.append(f"    {json.dumps(shrunk, ensure_ascii=False, default=str)}")
    return "\n".join(lines)


async def chain_coherence(
    agent: AgentRCAOutput,
    evidence_results: list[tuple[str, EvidenceVerifyResult]],
    llm_client: AsyncOpenAI | None,
    model: str = "gpt-4o-mini",
) -> ChainJudgeResult:
    """Judge whether agent claims are supported by their evidence SQL rows.

    On any exception (network outage, malformed JSON, etc.) returns
    ``ChainJudgeResult(score=None, ...)`` so callers can distinguish
    "couldn't judge" from "judged as incoherent". Aggregators are expected
    to drop None values from the average; do not silently coerce to 0.0.
    """
    if llm_client is None:
        raise ValueError(
            "chain_coherence requires an LLM client; configure judge_model in "
            "the eval config so a non-None client is provided."
        )

    prompt = _JUDGE_PROMPT.format(
        agent_output=agent.model_dump_json(by_alias=True, indent=2),
        evidence_block=_format_evidence_block(agent, evidence_results),
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
        return ChainJudgeResult(score=None, reasoning=f"(judge error: {exc!s:.200})")

    content = response.choices[0].message.content or ""
    try:
        parsed = json.loads(content)
    except json.JSONDecodeError:
        return ChainJudgeResult(score=None, reasoning="(judge returned non-JSON)", raw_response=content)
    raw_score = parsed.get("score")
    if raw_score is None:
        return ChainJudgeResult(score=None, reasoning="(judge omitted score)", raw_response=content)
    score = max(0.0, min(1.0, float(raw_score)))
    reasoning = str(parsed.get("reasoning") or "")
    return ChainJudgeResult(score=score, reasoning=reasoning, raw_response=content)
