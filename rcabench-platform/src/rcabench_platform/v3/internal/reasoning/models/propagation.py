"""Data models for fault propagation analysis."""

from dataclasses import dataclass, field


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


@dataclass
class PropagationResult:
    injection_node_ids: list[int]
    injection_states: list[str]
    paths: list[PropagationPath]
    visited_nodes: set[int]  # All nodes visited during propagation
    max_hops_reached: int
    subgraph_edges: list[tuple[int, int]] = field(default_factory=list)  # All edges in the reachable subgraph
    warnings: list[str] = field(default_factory=list)  # Warnings about anomalies during propagation

    def to_dict(self) -> dict:
        """Convert PropagationResult to dictionary."""
        return {
            "injection_node_ids": self.injection_node_ids,
            "injection_states": self.injection_states,
            "paths": [
                {
                    "nodes": path.nodes,
                    "states": path.states,
                    "edges": path.edges,
                    "rules": path.rules,
                    "confidence": path.confidence,
                    "state_start_times": path.state_start_times,
                    "propagation_delays": path.propagation_delays,
                }
                for path in self.paths
            ],
            "visited_nodes": list(self.visited_nodes),
            "max_hops_reached": self.max_hops_reached,
            "subgraph_edges": [{"src": src, "dst": dst} for src, dst in self.subgraph_edges],
            "warnings": self.warnings,
        }
