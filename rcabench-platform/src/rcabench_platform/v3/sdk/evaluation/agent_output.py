"""Agent RCA output contract (v2).

The agent does not know in advance whether the case is hybrid or single-fault.
It emits a flat list of root_causes; the matcher pairs each one against the
ground-truth fault list extracted from injection.json.

Each root_cause MUST carry at least one DuckDB-executable SQL evidence row.
"""

from __future__ import annotations

from enum import Enum

from pydantic import BaseModel, ConfigDict, Field, field_validator

from .fault_kind import FaultKind


class EvidenceKind(str, Enum):
    METRIC = "metric"
    TRACE = "trace"
    LOG = "log"


class Direction(BaseModel):
    """For Network* faults: the netem rule is installed on `src` and shapes
    traffic toward `dst`. For other fault kinds this field is null.
    """

    model_config = ConfigDict(frozen=True)

    src: str = Field(..., description="The service the netem rule sits on (== engine_config.app)")
    dst: str = Field(..., description="The remote peer (== engine_config.target_service)")


class Evidence(BaseModel):
    """One DuckDB SQL + a natural-language claim it is supposed to prove.

    The SQL is executed read-only against the case's parquets. The claim is
    used by the LLM-as-judge to decide whether the row set returned actually
    supports the assertion.
    """

    model_config = ConfigDict(frozen=False)

    kind: EvidenceKind
    sql: str = Field(..., description="DuckDB SQL; only read_parquet on the case dir is allowed.")
    claim: str = Field(..., description="What the SQL result is supposed to demonstrate.")


class RootCauseClaim(BaseModel):
    """One root cause the agent is asserting for this case."""

    service: str
    fault_kind: FaultKind
    direction: Direction | None = None
    method: str | None = Field(
        default=None,
        description="class.method for jvm_*/http_* faults; ignored otherwise.",
    )
    confidence: float | None = Field(default=None, ge=0.0, le=1.0)
    evidence: list[Evidence] = Field(default_factory=list)

    @field_validator("evidence")
    @classmethod
    def _at_least_one_evidence(cls, v: list[Evidence]) -> list[Evidence]:
        if not v:
            raise ValueError("each root_cause must carry at least one evidence")
        return v


class PropagationClaim(BaseModel):
    """An asserted causal edge from `from_` (upstream) to `to` (downstream)."""

    from_: str = Field(..., alias="from")
    to: str
    evidence: list[Evidence] = Field(default_factory=list)

    model_config = ConfigDict(populate_by_name=True)


class AgentRCAOutput(BaseModel):
    """The structured JSON the agent must produce.

    Shape is uniform across hybrid and single-fault cases; the agent simply
    fills `root_causes` with as many entries as it believes there are.
    """

    root_causes: list[RootCauseClaim] = Field(default_factory=list)
    propagation: list[PropagationClaim] = Field(default_factory=list)

    @classmethod
    def parse_str(cls, raw: str) -> AgentRCAOutput:
        import json

        return cls.model_validate(json.loads(raw))
