"""Per-case evaluation result schema.

Fields are flat (not namespaced by layer) so downstream aggregators and DB
columns stay simple. Layers:

  - Strict set match : precision / recall / f1 / exact_match
  - Service-only set match : service_precision / service_recall /
    service_f1 / service_exact_match
  - Kind-given-service : fault_kind_accuracy (None when no service-correct)
  - Boolean existence checks : any_root_cause_hit / any_service_hit /
    all_service_hit
  - Graph match : node_precision / node_recall / node_f1 /
    edge_precision / edge_recall / edge_f1 / path_reachability
  - Evidence : sql_executable_rate / evidence_support_rate (None when no
    evidence was judged)

No multiplicative headline. Each axis is reported independently so the
aggregator can show which one is failing without composing them.
"""

from __future__ import annotations

from pydantic import BaseModel, Field

from .graph_match import GraphMetrics
from .set_match import FaultMatchResult
from .sql_verify import EvidenceStatus


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


class EvaluationResult(BaseModel):
    """Per-case scoring result."""

    # ── Strict set match: HIT only ──────────────────────────────────────
    precision: float
    recall: float
    f1: float
    exact_match: bool

    # ── Service-only set match: HIT + WRONG_KIND ────────────────────────
    service_precision: float = 0.0
    service_recall: float = 0.0
    service_f1: float = 0.0
    service_exact_match: bool = False

    # ── Kind given service ──────────────────────────────────────────────
    fault_kind_accuracy: float | None = None
    kind_accuracy_denom: int = 0

    # ── Evidence ────────────────────────────────────────────────────────
    sql_executable_rate: float = 0.0
    evidence_support_rate: float | None = None

    # ── Graph + booleans ────────────────────────────────────────────────
    path_reachability: bool | None = None
    any_root_cause_hit: bool = False
    any_service_hit: bool = False
    all_service_hit: bool = False

    node_precision: float = 0.0
    node_recall: float = 0.0
    node_f1: float = 0.0
    edge_precision: float = 0.0
    edge_recall: float = 0.0
    edge_f1: float = 0.0

    # ── Diagnostics ─────────────────────────────────────────────────────
    per_fault: list[FaultMatchResult] = Field(default_factory=list)
    overclaim_indices: list[int] = Field(default_factory=list)
    per_evidence: list[PerEvidenceRecord] = Field(default_factory=list)
    graph_metrics: GraphMetrics | None = None

    n_evidence: int = 0
    n_evidence_judged: int = 0
    n_evidence_judge_failed: int = 0

    parse_error: str | None = None
    notes: list[str] = Field(default_factory=list)
