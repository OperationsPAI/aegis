"""Gate protocol + shared dataclasses for per-path validation."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Protocol, runtime_checkable

from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.algorithms.rule_matcher import RuleMatcher
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationRule


@dataclass(frozen=True, slots=True)
class GateResult:
    gate_name: str
    passed: bool
    evidence: dict[str, Any] = field(default_factory=dict)
    reason: str = ""


@dataclass(frozen=True, slots=True)
class GateContext:
    graph: HyperGraph
    timelines: dict[str, StateTimeline]
    rules: list[PropagationRule]
    rule_matcher: RuleMatcher
    injection_window: tuple[int, int]
    injection_node_ids: frozenset[int]


@runtime_checkable
class Gate(Protocol):
    name: str

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult: ...


def evaluate_path(path: CandidatePath, ctx: GateContext, gates: list[Gate]) -> list[GateResult]:
    return [g.evaluate(path, ctx) for g in gates]
