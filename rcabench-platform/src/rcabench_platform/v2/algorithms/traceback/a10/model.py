import json
from collections import defaultdict
from dataclasses import dataclass
from enum import Enum, auto
from pathlib import Path

import duckdb
import numpy as np
from scipy import stats

from ....logging import logger, timeit
from ....utils.env import debug
from ...spec import Algorithm, AlgorithmAnswer, AlgorithmArgs

# ============================================================================
# Data Structures
# ============================================================================


@dataclass(frozen=True, slots=True)
class SpanEdge:
    """Represents a call edge in the span dependency graph (span-to-span relationship)."""

    caller_service: str
    caller_span_name: str
    callee_service: str
    callee_span_name: str


@dataclass(frozen=True, slots=True)
class SDG:
    services: set[str]
    edges: list[SpanEdge]


class SymptomType(Enum):
    LATENCY = auto()
    ERROR_RATE = auto()


@dataclass(frozen=True, slots=True)
class Observation:
    observation_type: SymptomType
    entry_span_name: str  # Original span name from conclusion.parquet
    service_name: str
    trace_ids: list[str]
    trace_span_name: str | None = None  # Actual span name in traces table (may differ from entry_span_name)
    abnormal_avg_duration: float | None = None  # Abnormal average duration in nanoseconds
    normal_avg_duration: float | None = None  # Normal average duration in nanoseconds
    abnormal_succ_rate: float | None = None  # Abnormal success rate (0.0-1.0)
    normal_succ_rate: float | None = None  # Normal success rate (0.0-1.0)


@dataclass(frozen=True, slots=True)
class Symptom:
    symptom_type: SymptomType
    service_name: str
    span_name: str
    impact_score: float


@dataclass(frozen=True, slots=True)
class Relation:
    """
    Represents R(S|P) - the relation between Observation P and Symptom S.

    IMPORTANT: value is now a distribution (list of counts), not an average.

    In microservices RCA context (aligned with Perspect paper):
    - P (Predecessor/Observation): Entry-level spans with SLO violations
    - S (Symptom): Internal spans that CAUSE the observation violations
    - distribution: list where distribution[i] = number of S events in the same trace as P_i

    This captures the causal relationship: how many internal symptoms appear
    when we observe entry-level SLO violations.
    """

    predecessor_service: str  # Observation's service
    predecessor_span_name: str  # Observation's entry span name
    symptom_service: str  # Symptom's service
    symptom_span_name: str  # Symptom's specific span operation name
    symptom_type: SymptomType
    distribution: list[int]  # Distribution of S counts per P (not average!)
    sample_size: int  # Number of P events (observations)


@dataclass(frozen=True, slots=True)
class RelationChange:
    relation_good: Relation
    relation_bad: Relation
    change_magnitude: float  # Absolute difference
    statistical_significance: float  # p-value or test statistic


@dataclass(frozen=True, slots=True)
class RootCauseCandidate:
    service_name: str  # Service name (used for aggregation)
    span_name: str | None  # NEW: Specific span operation name (the actual root cause)
    symptom_type: SymptomType
    attribute_type: str  # e.g., "host", "pod", "status_code", "overall", "self-root"
    attribute_value: str | None  # e.g., "host-123", None for overall
    impact_score: float
    relation_change: RelationChange | None  # None for self-root-causes
