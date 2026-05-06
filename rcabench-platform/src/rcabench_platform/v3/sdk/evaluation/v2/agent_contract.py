"""Canonical agent-contract prompt fragment.

The judge code (matcher / ground_truth / evaluator) and the prompt that
*tells the agent how to answer* must agree on:

  * the fault_kind enum,
  * the JSON output schema,
  * what the evidence SQL is allowed to do.

If the prompt drifts from the SDK, every answer silently maps to UNKNOWN /
WRONG_KIND with no error. ``get_agent_contract_prompt()`` returns a single
canonical block of text integrators splice into their own system prompt, so
the contract version follows the SDK version automatically.

Usage (e.g. in a langgraph synthesis prompt)::

    from rcabench_platform.v3.sdk.evaluation.v2 import get_agent_contract_prompt

    SYSTEM_PROMPT = f\"\"\"
    You are an RCA synthesizer.
    {get_agent_contract_prompt()}
    Convert the investigation messages above into ONE JSON object.
    \"\"\"
"""

from __future__ import annotations

from .fault_kind import FaultKind


def _enum_block() -> str:
    """The 26 producible fault_kind values, excluding the UNKNOWN sentinel
    (agents should never emit UNKNOWN — it's the matcher's "couldn't decode"
    bucket). Wraps every ~6 names so the line stays readable in a prompt.
    """
    kinds = [k.value for k in FaultKind if k is not FaultKind.UNKNOWN]
    chunks: list[list[str]] = []
    for k in kinds:
        if not chunks or len(chunks[-1]) >= 6:
            chunks.append([])
        chunks[-1].append(k)
    return ",\n  ".join(", ".join(c) for c in chunks)


_SCHEMA_BLOCK = """{
  "root_causes": [
    {
      "service": "<canonical service_name as it appears in the data>",
      "fault_kind": "<one of the enum values below>",
      "evidence": [
        {"kind": "metric|trace|log",
         "sql": "<DuckDB SQL re-executable on this case dir>",
         "claim": "<<=20-word claim the rows support>"}
      ]
    }
  ],
  "propagation": [
    {"from": "<failing service where the fault originates>",
     "to":   "<service further along the impact chain, closer to the user>",
     "evidence": [{"kind": "trace", "sql": "...", "claim": "..."}]}
  ]
}"""


def get_agent_contract_prompt() -> str:
    """Return the canonical agent-contract prompt fragment.

    Stable across SDK patch releases unless the underlying enum or schema
    changes. Integrators should NOT hand-maintain a copy of the enum — pull
    this string at agent-startup so a SDK upgrade flows through.
    """
    return f"""## Output schema (STRICT JSON, no markdown / commentary)

```json
{_SCHEMA_BLOCK}
```

## fault_kind enum (pick exactly one per root_cause)

  {_enum_block()}

## Field rules
  * Service names must match strings present in the data — do not invent.
  * One entry per distinct root cause; do NOT collapse multiple distinct
    faults into a single root_cause.
  * `propagation` is the FAULT-IMPACT chain: edges flow FROM the failing
    service TOWARD the user-visible alarm tier (e.g. `frontend`,
    `front-end`, `ts-ui-dashboard`). Each chain should reach a frontend-tier
    service. Do NOT use the request-call direction (caller → callee).
  * Synthetic traffic generators (`loadgenerator`, `locust`, `wrk2`,
    `dsb-wrk2`, `k6`) are NOT services — never list them as `root_causes`,
    `from`, or `to`.

## SQL rules
  * DuckDB on this case dir's parquets.
  * SELECT only; one statement per `sql` field. No DDL/DML/ATTACH/etc.
  * Reference parquets via `read_parquet('<file>.parquet')` with bare
    filenames, OR directly `FROM abnormal_traces` (each *.parquet is mounted
    as a same-named view).
  * Each `evidence.sql` MUST be the actual SQL — not a description.
  * Each `evidence.claim` is the <=20-word natural-language assertion the SQL
    rows back.

## Hard rules
  * EVERY root_cause carries >=1 `evidence` (real runnable SQL).
  * EVERY propagation edge carries >=1 evidence. Do not emit edges you cannot
    back with a concrete trace/metric query."""
