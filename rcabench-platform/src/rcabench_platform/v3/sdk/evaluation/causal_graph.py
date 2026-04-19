from datetime import datetime, timezone
from typing import Any

from pydantic import BaseModel, ConfigDict, Field


def parse_timestamp(ts: Any) -> int | None:
    """Parse timestamp into Unix nanoseconds.

    Handles:
    - None → None
    - int/float → int (assumed nanoseconds if > 1e15, otherwise seconds→nanos)
    - ISO 8601 string (e.g. "2025-08-28T12:16:55.445Z") → nanoseconds
    - Numeric string → parsed as number
    """
    if ts is None:
        return None

    if isinstance(ts, (int, float)):
        ts_int = int(ts)
        # Heuristic: if value < 1e15 it's likely in seconds, convert to nanos
        if ts_int < 1_000_000_000_000_000:
            return ts_int * 1_000_000_000
        return ts_int

    if isinstance(ts, str):
        ts = ts.strip()
        if not ts:
            return None

        # Try numeric string first
        try:
            return parse_timestamp(float(ts))
        except ValueError:
            pass

        # Try ISO 8601 formats
        for fmt in (
            "%Y-%m-%dT%H:%M:%S.%fZ",
            "%Y-%m-%dT%H:%M:%SZ",
            "%Y-%m-%dT%H:%M:%S.%f%z",
            "%Y-%m-%dT%H:%M:%S%z",
            "%Y-%m-%dT%H:%M:%S.%f",
            "%Y-%m-%dT%H:%M:%S",
        ):
            try:
                dt = datetime.strptime(ts, fmt)
                if dt.tzinfo is None:
                    dt = dt.replace(tzinfo=timezone.utc)
                return int(dt.timestamp() * 1_000_000_000)
            except ValueError:
                continue

    return None


def _parse_state(state: Any) -> frozenset[str]:
    """Parse state field into frozenset."""
    if isinstance(state, list):
        return frozenset(state)
    elif isinstance(state, str):
        return frozenset([state])
    elif isinstance(state, (set, frozenset)):
        return frozenset(state)
    return frozenset()


class CausalNode(BaseModel):
    """A node in the causal graph representing an event.

    The three essential elements:
    - timestamp: WHEN did it happen (optional, for temporal analysis)
    - component: WHERE did it happen (e.g., "service|ts-order-service", "span|/api/order")
    - state: WHAT happened (e.g., {"high_latency", "high_error_rate"})
    """

    model_config = ConfigDict(frozen=True)

    timestamp: int | None = Field(
        default=None,
        description="Unix timestamp (nanoseconds) when the abnormal state started",
    )
    component: str = Field(
        ...,
        description="Component identifier (e.g., 'ts-order-service')",
    )
    state: frozenset[str] = Field(
        default=frozenset(),
        description="Set of abnormal states (e.g., {'high_latency', 'timeout'})",
    )

    def __hash__(self) -> int:
        return hash((self.component, self.state))

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, CausalNode):
            return False
        return self.component == other.component and self.state == other.state

    @property
    def service(self) -> str:
        return self.component


class CausalEdge(BaseModel):
    """A directed edge representing causal relationship: source -> target."""

    model_config = ConfigDict(frozen=True)

    source: str = Field(..., description="Source component identifier")
    target: str = Field(..., description="Target component identifier")

    def __hash__(self) -> int:
        return hash((self.source, self.target))

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, CausalEdge):
            return False
        return self.source == other.source and self.target == other.target

    def to_service_edge(self, component_to_service: dict[str, str] | None = None) -> "CausalEdge":
        if component_to_service is None:
            return CausalEdge(source=self.source, target=self.target)
        src_service = component_to_service.get(self.source, self.source)
        tgt_service = component_to_service.get(self.target, self.target)
        return CausalEdge(source=src_service, target=tgt_service)


class CausalGraph(BaseModel):
    nodes: list[CausalNode] = Field(
        default_factory=list,
        description="All nodes in the causal graph",
    )
    edges: list[CausalEdge] = Field(
        default_factory=list,
        description="All causal edges (source -> target)",
    )
    root_causes: list[CausalNode] = Field(
        default_factory=list,
        description="Root cause nodes (injection points / identified root causes). Can be multiple.",
    )
    alarm_nodes: list[CausalNode] = Field(
        default_factory=list,
        description="Alarm/symptom nodes (endpoints of propagation paths). These are the observable symptoms.",
    )
    component_to_service: dict[str, str] = Field(
        default_factory=dict,
        description="Mapping from component (span/pod) to service name",
    )

    def get_service_edges(self) -> set[tuple[str, str]]:
        service_edges: set[tuple[str, str]] = set()
        for edge in self.edges:
            src = self.component_to_service.get(edge.source, edge.source)
            tgt = self.component_to_service.get(edge.target, edge.target)
            if src != tgt:
                service_edges.add((src, tgt))
        return service_edges

    def get_service_nodes(self) -> set[str]:
        services: set[str] = set()
        for node in self.nodes:
            service = self.component_to_service.get(node.component, node.component)
            services.add(service)
        return services

    def get_root_cause_services(self) -> set[str]:
        services: set[str] = set()
        for rc in self.root_causes:
            service = self.component_to_service.get(rc.component, rc.component)
            services.add(service)
        return services

    def get_alarm_services(self) -> set[str]:
        services: set[str] = set()
        for alarm in self.alarm_nodes:
            service = self.component_to_service.get(alarm.component, alarm.component)
            services.add(service)
        return services

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "CausalGraph":
        """Parse CausalGraph from a dictionary.

        Args:
            data: Dictionary containing nodes, edges, root_causes, and optionally
                  component_to_service mapping.

        Returns:
            CausalGraph instance.
        """
        # Parse nodes
        nodes = []
        for node_data in data.get("nodes", []):
            if isinstance(node_data, dict):
                nodes.append(
                    CausalNode(
                        component=node_data.get("component", ""),
                        state=_parse_state(node_data.get("state", [])),
                        timestamp=parse_timestamp(node_data.get("timestamp")),
                    )
                )

        # Parse edges
        edges = []
        for edge_data in data.get("edges", []):
            if isinstance(edge_data, dict):
                edges.append(
                    CausalEdge(
                        source=edge_data.get("source", ""),
                        target=edge_data.get("target", ""),
                    )
                )

        # Parse root_causes
        root_causes = []
        for rc_data in data.get("root_causes", []):
            if isinstance(rc_data, dict):
                root_causes.append(
                    CausalNode(
                        component=rc_data.get("component", ""),
                        state=_parse_state(rc_data.get("state", [])),
                        timestamp=parse_timestamp(rc_data.get("timestamp")),
                    )
                )

        # Parse alarm_nodes
        alarm_nodes = []
        for alarm_data in data.get("alarm_nodes", []):
            if isinstance(alarm_data, dict):
                alarm_nodes.append(
                    CausalNode(
                        component=alarm_data.get("component", ""),
                        state=_parse_state(alarm_data.get("state", [])),
                        timestamp=parse_timestamp(alarm_data.get("timestamp")),
                    )
                )

        # Parse component_to_service mapping
        component_to_service = data.get("component_to_service", {})

        return cls(
            nodes=nodes,
            edges=edges,
            root_causes=root_causes,
            alarm_nodes=alarm_nodes,
            component_to_service=component_to_service,
        )


GroundTruthGraph = CausalGraph
AgentGraph = CausalGraph
