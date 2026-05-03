"""Per-evidence LLM-as-judge: claim-vs-rows + chain coherence.

Each evidence is judged independently against its own SQL row preview, with
a brief view of the agent's overall claim chain so the judge can flag
broken propagation links. Output is binary ``supported: bool``; the
benchmark-level ``evidence_support_rate`` aggregates these.

A failed judge call (transient outage etc.) returns ``supported=None`` so
the aggregator can distinguish it from a judged-as-incoherent evidence
and exclude it from the per-case denominator.

The judge stays blind to the ground-truth causal graph — GT alignment is
already covered deterministically by ``f1`` / ``node_f1`` / ``edge_f1``.
"""

from __future__ import annotations

import json
import re

from openai import AsyncOpenAI
from pydantic import BaseModel, Field

from ...llm_eval.utils import get_logger
from .schema import Evidence
from .sql_verify import EvidenceVerifyResult

logger = get_logger("llm_eval.chain_judge")

_LOG_BODY_LIMIT = 8000


class EvidenceJudgeResult(BaseModel):
    supported: bool | None = Field(default=None)
    """True/False if judged; None when the judge call itself failed."""
    reasoning: str = ""
    raw_response: str | None = None


_JUDGE_PROMPT = """You are scoring whether ONE piece of evidence supports its paired claim,
in the context of an RCA agent's overall reasoning chain.

You will be given:
  • a compact summary of the agent's full chain (root_causes + propagation
    edges) — for context only, so you can spot broken upstream links;
  • the specific evidence under review: where in the chain it lives, the
    natural-language claim, the SQL, and a sample of rows the SQL actually
    returned when re-executed.

Decide BOTH conditions hold:
  1. The SQL row sample plausibly supports the claim — the rows do not
     contradict the assertion (e.g. "p99 latency spiked" needs rows that
     actually show elevated latency, not flat values).
  2. The evidence fits coherently into the chain — its claim is not a
     dangling assertion whose upstream cause is missing from the chain,
     and is not contradicted by adjacent claims.

If both hold → supported=true. Otherwise → supported=false. EMPTY or
SQL_ERROR results almost always mean supported=false (no rows to support
the claim).

You are NOT given the ground-truth answer. Do not guess which case this
is or whether the named services are "correct" — that is judged
separately. Your job is only claim-vs-data plus internal coherence.

OUTPUT FORMAT — strict, no exceptions:
  • Output exactly one JSON object: {{"supported": true|false, "reasoning": "<=80 words"}}
  • No prose before or after. No markdown fences. No commentary.
  • Begin your response with `{{` and end with `}}`.

== Agent's full chain (context) ==
{chain_summary}

== Evidence under review ==
Location: {location}
Label: {label}
Claim: {claim}
SQL kind: {kind}
SQL:
{sql}

SQL execution result: status={status} rows={row_count}{error_block}
Sample rows:
{rows_block}
"""


_JSON_FENCE_RE = re.compile(r"```(?:json)?\s*([\s\S]*?)\s*```", re.IGNORECASE)


def _extract_json_object(text: str) -> str | None:
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


def _truncate(s: str, limit: int = _LOG_BODY_LIMIT) -> str:
    if len(s) <= limit:
        return s
    return f"{s[:limit]}... [+{len(s) - limit} bytes truncated]"


def _format_rows(verify_result: EvidenceVerifyResult, max_rows: int = 5, max_cell: int = 200) -> str:
    if not verify_result.sample_rows:
        return "  (no rows)"
    lines: list[str] = []
    for row in verify_result.sample_rows[:max_rows]:
        shrunk = {k: (str(v)[:max_cell] if v is not None else None) for k, v in row.items()}
        lines.append(f"  {json.dumps(shrunk, ensure_ascii=False, default=str)}")
    return "\n".join(lines)


async def evidence_support(
    *,
    chain_summary: str,
    location: str,
    label: str,
    evidence: Evidence,
    verify_result: EvidenceVerifyResult,
    llm_client: AsyncOpenAI,
    model: str = "gpt-4o-mini",
    case_name: str | None = None,
) -> EvidenceJudgeResult:
    """Judge a single (claim, SQL row preview) pair in chain context.

    On any exception (network outage, malformed JSON, etc.) returns
    ``EvidenceJudgeResult(supported=None, ...)`` so callers can distinguish
    "couldn't judge" from "judged as unsupported". The aggregator excludes
    None entries from the per-case denominator and surfaces the count via
    ``judge_failed`` instead of conflating them with a 0 score.

    ``case_name`` + ``label`` together tag every log record this call emits
    so multi-case multi-evidence runs stay readable.
    """
    tag = f"[case={case_name or '?'} {label}]"

    error_block = ""
    if verify_result.error:
        error_block = f"\nSQL error: {verify_result.error[:300]}"

    prompt = _JUDGE_PROMPT.format(
        chain_summary=chain_summary,
        location=location,
        label=label,
        claim=evidence.claim,
        kind=evidence.kind.value,
        sql=evidence.sql,
        status=verify_result.status.value,
        row_count=verify_result.row_count,
        error_block=error_block,
        rows_block=_format_rows(verify_result),
    )
    logger.debug("%s judge prompt (model=%s, len=%d):\n%s", tag, model, len(prompt), _truncate(prompt))

    try:
        response = await llm_client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": prompt}],
        )
    except Exception as exc:
        logger.warning("%s judge call failed: %s", tag, exc)
        return EvidenceJudgeResult(supported=None, reasoning=f"(judge error: {exc!s:.200})")

    finish_reason = getattr(response.choices[0], "finish_reason", None)
    content = response.choices[0].message.content or ""
    logger.debug("%s judge raw response (finish=%s, len=%d):\n%s", tag, finish_reason, len(content), _truncate(content))
    if not content and finish_reason == "length":
        return EvidenceJudgeResult(
            supported=None,
            reasoning="(judge returned empty content with finish_reason=length)",
        )

    parsed: dict | None = None
    try:
        candidate = json.loads(content)
        if isinstance(candidate, dict):
            parsed = candidate
    except json.JSONDecodeError:
        pass

    if parsed is None:
        extracted = _extract_json_object(content)
        if extracted is None:
            logger.warning("%s judge returned no JSON object; raw=%r", tag, content[:200])
            return EvidenceJudgeResult(
                supported=None,
                reasoning="(judge returned no JSON object)",
                raw_response=content,
            )
        try:
            candidate = json.loads(extracted)
        except json.JSONDecodeError as exc:
            logger.warning("%s judge JSON parse failed: %s; raw=%r", tag, exc, content[:200])
            return EvidenceJudgeResult(
                supported=None,
                reasoning=f"(judge JSON parse failed: {exc!s:.120})",
                raw_response=content,
            )
        if not isinstance(candidate, dict):
            return EvidenceJudgeResult(
                supported=None,
                reasoning="(judge JSON was not an object)",
                raw_response=content,
            )
        parsed = candidate

    raw_supported = parsed.get("supported")
    if raw_supported is None:
        logger.warning("%s judge omitted supported; parsed=%r", tag, parsed)
        return EvidenceJudgeResult(
            supported=None,
            reasoning="(judge omitted 'supported')",
            raw_response=content,
        )

    if isinstance(raw_supported, bool):
        supported_val: bool = raw_supported
    elif isinstance(raw_supported, str):
        s = raw_supported.strip().lower()
        if s in ("true", "yes", "y", "1"):
            supported_val = True
        elif s in ("false", "no", "n", "0"):
            supported_val = False
        else:
            return EvidenceJudgeResult(
                supported=None,
                reasoning=f"(judge 'supported' not boolean: {raw_supported!r:.40})",
                raw_response=content,
            )
    elif isinstance(raw_supported, (int, float)):
        supported_val = bool(raw_supported)
    else:
        return EvidenceJudgeResult(
            supported=None,
            reasoning=f"(judge 'supported' not boolean: {raw_supported!r:.40})",
            raw_response=content,
        )

    reasoning = str(parsed.get("reasoning") or "")
    logger.info("%s judge supported=%s reasoning=%s", tag, supported_val, _truncate(reasoning, 200))
    return EvidenceJudgeResult(supported=supported_val, reasoning=reasoning, raw_response=content)
