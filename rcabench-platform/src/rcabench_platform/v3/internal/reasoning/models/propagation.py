"""Data models for fault propagation analysis."""

from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any, Literal

if TYPE_CHECKING:
    from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import GateResult

LabelT = Literal[
    "ineffective",
    "absorbed",
    "attributed",
    "unexplained_impact",
    "contaminated",
]


@dataclass
class PropagationPath:
    """A single path in the fault propagation chain."""

    nodes: list[int]  # Node IDs in order
    states: list[list[str]]  # List of states at each node (each node can have multiple states)
    edges: list[str]  # Edge descriptions (for visualization)
    rules: list[str]  # Rule IDs applied at each hop
    confidence: float  # Overall confidence (product of rule confidences)
    state_start_times: list[int | None] = field(
        default_factory=list
    )  # Timestamp when each state started (Unix seconds, aligned to 10s)
    propagation_delays: list[float] = field(default_factory=list)  # Time delay for each hop (seconds)
    gate_results: list["GateResult"] = field(default_factory=list)


@dataclass(frozen=True, slots=True)
class RejectedPath:
    node_ids: list[int]
    gate_results: list["GateResult"]


@dataclass(frozen=True, slots=True)
class LocalEffect:
    """L: whether the fault produced any observable non-healthy state at the injection point.

    L=0 means the fault did not manifest locally — typically a misconfigured
    or absorbed injection. L=1 means at least one injection node carries a
    non-healthy state during the abnormal window.
    """

    detected: bool
    evidence: dict[str, Any]


@dataclass(frozen=True, slots=True)
class SLOImpact:
    """E: whether the fault reached the SLO surface (alarm-bearing nodes).

    E=0 means no alarms fired anywhere in the graph; the fault was contained
    upstream of any SLO-relevant entity. E=1 means at least one alarm
    triggered, regardless of whether a propagation path was found.
    """

    detected: bool
    impacted_nodes: list[str]
    evidence: dict[str, Any]


@dataclass(frozen=True, slots=True)
class MechanismPath:
    """M: the rule-admitted causal chain from injection to SLO surface.

    M is None when E=1 but no path was found (unexplained_impact). Otherwise
    M.paths is the rule-admitted set; n_paths and confidence are convenience
    summaries derived from that set.
    """

    paths: list[PropagationPath]
    n_paths: int
    confidence: float


@dataclass(frozen=True, slots=True)
class FaultDecomposition:
    """The (L, E, M) decomposition that drives the 5-class label classifier."""

    L: LocalEffect
    E: SLOImpact
    M: MechanismPath | None


def _serialize_gate_result(gr: "GateResult") -> dict[str, Any]:
    return {
        "gate_name": gr.gate_name,
        "passed": gr.passed,
        "evidence": gr.evidence,
        "reason": gr.reason,
    }


def _serialize_propagation_path(path: PropagationPath) -> dict[str, Any]:
    return {
        "nodes": path.nodes,
        "states": path.states,
        "edges": path.edges,
        "rules": path.rules,
        "confidence": path.confidence,
        "state_start_times": path.state_start_times,
        "propagation_delays": path.propagation_delays,
        "gate_results": [_serialize_gate_result(g) for g in path.gate_results],
    }


def _serialize_rejected_path(rp: "RejectedPath") -> dict[str, Any]:
    return {
        "node_ids": list(rp.node_ids),
        "gate_results": [_serialize_gate_result(g) for g in rp.gate_results],
    }


def _serialize_decomposition(decomp: FaultDecomposition) -> dict[str, Any]:
    out: dict[str, Any] = {
        "L": {
            "detected": decomp.L.detected,
            "evidence": decomp.L.evidence,
        },
        "E": {
            "detected": decomp.E.detected,
            "impacted_nodes": decomp.E.impacted_nodes,
            "evidence": decomp.E.evidence,
        },
    }
    if decomp.M is None:
        out["M"] = None
    else:
        out["M"] = {
            "paths": [_serialize_propagation_path(p) for p in decomp.M.paths],
            "n_paths": decomp.M.n_paths,
            "confidence": decomp.M.confidence,
        }
    return out


@dataclass
class PropagationResult:
    injection_node_ids: list[int]
    injection_states: list[str]
    paths: list[PropagationPath]
    visited_nodes: set[int]  # All nodes visited during propagation
    max_hops_reached: int
    subgraph_edges: list[tuple[int, int]] = field(default_factory=list)  # All edges in the reachable subgraph
    warnings: list[str] = field(default_factory=list)  # Warnings about anomalies during propagation
    label: LabelT | None = None
    label_reason: str = ""
    decomposition: FaultDecomposition | None = None
    rejected_paths: list[RejectedPath] = field(default_factory=list)
    injection_state_reasons: list[str | None] = field(default_factory=list)
    injection_state_details: list[dict[str, Any]] = field(default_factory=list)

    def to_dict(self) -> dict[str, Any]:
        """Convert PropagationResult to dictionary."""
        out: dict[str, Any] = {
            "injection_node_ids": self.injection_node_ids,
            "injection_states": self.injection_states,
            "paths": [_serialize_propagation_path(p) for p in self.paths],
            "visited_nodes": list(self.visited_nodes),
            "max_hops_reached": self.max_hops_reached,
            "subgraph_edges": [{"src": src, "dst": dst} for src, dst in self.subgraph_edges],
            "warnings": self.warnings,
            "rejected_paths": [_serialize_rejected_path(r) for r in self.rejected_paths],
        }
        if self.injection_state_reasons:
            out["injection_state_reasons"] = self.injection_state_reasons
        if self.injection_state_details:
            out["injection_state_details"] = self.injection_state_details
        if self.label is not None:
            out["label"] = self.label
        if self.label_reason:
            out["label_reason"] = self.label_reason
        if self.decomposition is not None:
            out["decomposition"] = _serialize_decomposition(self.decomposition)
        return out
