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

Compatibility note: the OpenAI ``response_format={"type": "json_object"}``
flag is intentionally NOT used. Some OpenAI-compatible gateways (litellm
etc.) reject it with HTTP 400 even when the underlying model behaves fine
without it. Instead the prompt strictly demands raw JSON and the parser
tolerates markdown fences / preamble text, falling back to a brace-walk
when ``json.loads`` on the whole content rejects it.
"""

from __future__ import annotations

import json
import re

from openai import AsyncOpenAI
from pydantic import BaseModel, Field

from ...llm_eval.utils import get_logger
from .schema import AgentRCAOutput
from .sql_verify import EvidenceVerifyResult

# Logs flow through the shared `llm_eval` logger tree, which `setup_logging`
# already wires to both a colored stream handler (level = $LLM_EVAL_LOG_LEVEL,
# default WARNING) and a rotating file handler at $LLM_EVAL_LOG_DIR/llm_eval.log
# (always DEBUG). So:
#   • To see judge prompts + raw responses in the terminal, run with
#     LLM_EVAL_LOG_LEVEL=DEBUG.
#   • Otherwise tail logs/llm_eval.log — DEBUG-level records are always there.
logger = get_logger("llm_eval.chain_judge")

# Cap how much of the prompt and the raw response we drop into logs. Full
# prompts can hit hundreds of KB once evidence rows are spliced in, so an
# unbounded log line would balloon log files and risk leaking large case
# payloads. 8 KB keeps DEBUG useful (you can read the first ~120 lines)
# without turning the log into a data dump.
_LOG_BODY_LIMIT = 8000


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

OUTPUT FORMAT — strict, no exceptions:
  • Output exactly one JSON object: {{"score": <float 0..1>, "reasoning": "<=80 words>"}}
  • No prose before or after. No markdown fences. No commentary.
  • Begin your response with `{{` and end with `}}`.

== Agent output ==
{agent_output}

== Evidence executions (status + sample rows) ==
{evidence_block}
"""


_JSON_FENCE_RE = re.compile(r"```(?:json)?\s*([\s\S]*?)\s*```", re.IGNORECASE)


def _extract_json_object(text: str) -> str | None:
    """Pull a balanced JSON object out of arbitrary model output.

    Tolerates three real-world failure modes seen in the wild:
      • the whole content is already raw JSON (fast path),
      • the model wraps JSON in a ```json ... ``` fence,
      • the model prepends prose ("Sure, here's the JSON: { ... }").

    Returns the substring containing the first balanced ``{...}`` object,
    or ``None`` if no plausible candidate is found.
    """
    if not text:
        return None
    fence = _JSON_FENCE_RE.search(text)
    if fence:
        candidate = fence.group(1).strip()
        if candidate.startswith("{"):
            return candidate

    first_brace = text.find("{")
    if first_brace == -1:
        return None
    depth = 0
    in_string = False
    escape_next = False
    for i in range(first_brace, len(text)):
        ch = text[i]
        if escape_next:
            escape_next = False
            continue
        if ch == "\\":
            escape_next = True
            continue
        if ch == '"' and not escape_next:
            in_string = not in_string
            continue
        if in_string:
            continue
        if ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                return text[first_brace : i + 1]
    return None


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


def _truncate(s: str, limit: int = _LOG_BODY_LIMIT) -> str:
    if len(s) <= limit:
        return s
    return f"{s[:limit]}... [+{len(s) - limit} bytes truncated]"


async def chain_coherence(
    agent: AgentRCAOutput,
    evidence_results: list[tuple[str, EvidenceVerifyResult]],
    llm_client: AsyncOpenAI | None,
    model: str = "gpt-4o-mini",
    case_name: str | None = None,
) -> ChainJudgeResult:
    """Judge whether agent claims are supported by their evidence SQL rows.

    On any exception (network outage, malformed JSON, etc.) returns
    ``ChainJudgeResult(score=None, ...)`` so callers can distinguish
    "couldn't judge" from "judged as incoherent". Aggregators are expected
    to drop None values from the average; do not silently coerce to 0.0.

    ``case_name`` is purely a log correlation key — it tags every log
    record this judge call emits so multi-case runs stay readable.
    """
    tag = f"[case={case_name}]" if case_name else "[case=?]"

    if llm_client is None:
        raise ValueError(
            "chain_coherence requires an LLM client; configure judge_model in "
            "the eval config so a non-None client is provided."
        )

    prompt = _JUDGE_PROMPT.format(
        agent_output=agent.model_dump_json(by_alias=True, indent=2),
        evidence_block=_format_evidence_block(agent, evidence_results),
    )
    logger.debug("%s judge prompt (model=%s, len=%d):\n%s", tag, model, len(prompt), _truncate(prompt))

    try:
        response = await llm_client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": prompt}],
            temperature=0,
            max_tokens=400,
        )
    except Exception as exc:
        logger.warning("%s judge call failed: %s", tag, exc)
        return ChainJudgeResult(score=None, reasoning=f"(judge error: {exc!s:.200})")

    content = response.choices[0].message.content or ""
    logger.debug("%s judge raw response (len=%d):\n%s", tag, len(content), _truncate(content))

    # Try the cheap path first: model already obeyed and returned raw JSON.
    parsed: dict | None = None
    try:
        candidate = json.loads(content)
        if isinstance(candidate, dict):
            parsed = candidate
    except json.JSONDecodeError:
        pass

    # Fallback: extract a balanced JSON object from text that may have
    # markdown fences or prose preamble.
    if parsed is None:
        extracted = _extract_json_object(content)
        if extracted is None:
            logger.warning("%s judge returned no JSON object; raw=%r", tag, content[:200])
            return ChainJudgeResult(
                score=None,
                reasoning="(judge returned no JSON object)",
                raw_response=content,
            )
        try:
            candidate = json.loads(extracted)
        except json.JSONDecodeError as exc:
            logger.warning("%s judge JSON parse failed: %s; raw=%r", tag, exc, content[:200])
            return ChainJudgeResult(
                score=None,
                reasoning=f"(judge JSON parse failed: {exc!s:.120})",
                raw_response=content,
            )
        if not isinstance(candidate, dict):
            logger.warning("%s judge JSON was not an object; raw=%r", tag, content[:200])
            return ChainJudgeResult(
                score=None,
                reasoning="(judge JSON was not an object)",
                raw_response=content,
            )
        parsed = candidate

    raw_score = parsed.get("score")
    if raw_score is None:
        logger.warning("%s judge omitted score; parsed=%r", tag, parsed)
        return ChainJudgeResult(score=None, reasoning="(judge omitted score)", raw_response=content)
    try:
        score = max(0.0, min(1.0, float(raw_score)))
    except (TypeError, ValueError):
        logger.warning("%s judge score not numeric: %r", tag, raw_score)
        return ChainJudgeResult(
            score=None,
            reasoning=f"(judge score not numeric: {raw_score!r:.40})",
            raw_response=content,
        )
    reasoning = str(parsed.get("reasoning") or "")
    logger.info("%s judge score=%.3f reasoning=%s", tag, score, _truncate(reasoning, 200))
    return ChainJudgeResult(score=score, reasoning=reasoning, raw_response=content)
