"""Rule schema for bidirectional fault propagation.

Rules define how faults propagate through the topology based on node states,
edge types, and edge data conditions.

Supports both single-hop and multi-hop propagation paths to reduce false positives
by expressing complete causal chains.

Rules are tiered (``RuleTier``):

* ``core`` — operates only on canonical IR states (``HEALTHY``/``SLOW``/
  ``ERRORING``/``DEGRADED``/``UNAVAILABLE``/``MISSING``/``RESTARTING``/
  ``UNKNOWN``) and topology kinds. Should fire on any OTel-instrumented
  stack out-of-the-box.
* ``augmentation`` — requires specialization labels (e.g. ``frequent_gc``,
  ``oom_killed``) that only specific augmenter adapters emit. Optional;
  used for richer explanations and loaded only when explicitly requested.
"""

from collections.abc import Callable
from enum import auto
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator

from rcabench_platform.compat import StrEnum
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, Edge, PlaceKind


class PropagationDirection(StrEnum):
    FORWARD = auto()  # Propagate along edge direction (src → dst)
    BACKWARD = auto()  # Propagate against edge direction (dst → src)


class RuleTier(StrEnum):
    """Tier classification for propagation rules.

    ``core``: rule predicates speak only the canonical IR state vocabulary
    (and topology kinds), so the rule fires on any OTel-instrumented stack
    out-of-the-box. ``get_builtin_rules()`` returns these by default.

    ``augmentation``: rule depends on a specialization label that only
    specific augmenter adapters emit (e.g. ``frequent_gc`` from a JVM
    augmenter). These are skipped by default and opt-in via
    ``get_builtin_rules(include_augmentation=True)``.
    """

    core = auto()
    augmentation = auto()


class PathHop(BaseModel):
    """A single hop in a multi-hop propagation path.

    Represents one edge traversal: node_A --[edge_kind, direction]--> node_B
    """

    edge_kind: DepKind = Field(description="Edge type for this hop")
    direction: PropagationDirection = Field(description="Traversal direction for this hop")
    intermediate_kind: PlaceKind | None = Field(
        default=None,
        description="Expected PlaceKind of intermediate node (None for last hop)",
    )
    intermediate_states: list[str] | None = Field(
        default=None,
        description=(
            "Allowed states at intermediate node. If specified, the intermediate node must have at "
            "least one of these states for the path to be valid. Use ['healthy', 'unknown'] to "
            "bypass nodes without detected anomalies."
        ),
    )
    edge_condition: Callable[[Edge], bool] | None = Field(
        default=None,
        description="Optional condition on edge data for this hop",
    )

    model_config = ConfigDict(arbitrary_types_allowed=True)

    @field_validator("intermediate_states", mode="after")
    @classmethod
    def normalize_intermediate_states(cls, v: list[str] | None) -> list[str] | None:
        """Normalize state names to lowercase for consistent matching."""
        if v is None:
            return None
        return [s.lower() for s in v]


class FirstHopConfig(BaseModel):
    """Configuration for first-hop validation behavior.

    First-hop validation has different semantics depending on source PlaceKind:
    - Span injection: Must validate anomalous states at source
    - Service injection: Service is a dummy aggregation node, lenient matching
    - Container/Pod injection: May not have exact rule states at injection point

    This config allows rules to override default first-hop behavior.
    """

    require_src_states: bool = Field(
        default=False,
        description="If True, source must have states matching rule.src_states. "
        "Span injection requires this; non-span injection is lenient by default.",
    )
    require_dst_states: bool = Field(
        default=True,
        description="If True, destination must have detected states. Always True for valid propagation paths.",
    )
    lenient_dst_state_match: bool = Field(
        default=False,
        description="If True, accept any detected states at destination, "
        "not just rule.possible_dst_states. Useful for service->span first hop.",
    )


class PropagationRule(BaseModel):
    """A single fault propagation rule.

    Supports both single-hop and multi-hop propagation paths:
    - Single-hop: Use edge_kind + direction (backward compatible)
    - Multi-hop: Use path (list of PathHop), more precise causal chains

    Examples:
        # Single-hop: Span SLOW --calls(BACKWARD)--> Span SLOW
        PropagationRule(
            rule_id="span_slow_to_caller",
            description="...",
            tier=RuleTier.core,
            src_kind=PlaceKind.span,
            src_states=["slow"],
            edge_kind=DepKind.calls,
            direction=PropagationDirection.BACKWARD,
            dst_kind=PlaceKind.span,
            possible_dst_states=["slow", "erroring"],
        )

        # Multi-hop: Pod UNAVAILABLE --routes_to(BACKWARD)--> Service --includes(FORWARD)--> Span
        PropagationRule(
            rule_id="pod_unavailable_to_span",
            description="...",
            tier=RuleTier.core,
            src_kind=PlaceKind.pod,
            src_states=["unavailable"],
            path=[
                PathHop(edge_kind=DepKind.routes_to, direction=BACKWARD, intermediate_kind=PlaceKind.service),
                PathHop(edge_kind=DepKind.includes, direction=FORWARD),
            ],
            dst_kind=PlaceKind.span,
            possible_dst_states=["missing", "unavailable", "erroring"],
        )
    """

    rule_id: str = Field(description="Unique identifier for the rule")

    description: str = Field(description="Human-readable description")

    tier: RuleTier = Field(
        description=(
            "Rule classification — `core` rules speak only canonical IR states and "
            "fire on any OTel-instrumented stack; `augmentation` rules depend on "
            "specialization labels emitted by specific augmenter adapters."
        ),
    )

    # Source node constraints
    src_kind: PlaceKind = Field(description="Source node PlaceKind")
    src_states: list[str] = Field(description="Source states that trigger propagation")

    # Path specification: single-hop (legacy) or multi-hop (recommended)
    edge_kind: DepKind | None = Field(default=None, description="[Single-hop] Edge type for propagation")
    direction: PropagationDirection | None = Field(default=None, description="[Single-hop] Propagation direction")
    path: list[PathHop] | None = Field(
        default=None,
        description="[Multi-hop] Sequence of hops from src to dst. Mutually exclusive with edge_kind+direction",
    )

    # Destination node constraints
    dst_kind: PlaceKind = Field(description="Destination node PlaceKind")
    possible_dst_states: list[str] = Field(description="Possible resulting states in destination node")

    # Optional edge data condition (for single-hop only, use PathHop.edge_condition for multi-hop)
    edge_condition: Callable[[Edge], bool] | None = Field(
        default=None,
        description="[Single-hop] Optional function to check edge data",
    )

    # Specialization-label gating (Phase 4 of #163)
    required_labels: frozenset[str] = Field(
        default_factory=frozenset,
        description=(
            "Specialization labels (e.g. ``frequent_gc``, ``oom_killed``) that must "
            "all be present on the source node's timeline for the rule to fire. "
            "Empty (default) means the rule is label-agnostic and matches purely on "
            "canonical state — preserving the behaviour of every core rule. "
            "Augmenter rules use this to gate on labels emitted by specific augmenter "
            "adapters (the JVM augmenter emits ``frequent_gc`` / ``high_heap_pressure`` / "
            "``oom_killed``; only rules listing those labels here will fire when the "
            "stack carries them, and won't fire on stacks that don't)."
        ),
    )

    # Rule metadata
    confidence: float = Field(
        default=0.8,
        ge=0.0,
        le=1.0,
        description="Confidence score for this propagation rule",
    )

    # Temporal constraints for causality verification
    min_delay: float | None = Field(
        default=None,
        ge=0.0,
        description="Minimum propagation delay in seconds",
    )
    max_delay: float | None = Field(
        default=None,
        ge=0.0,
        description="Maximum propagation delay in seconds",
    )

    source: str = Field(
        default="builtin",
        description="Source of the rule (builtin, llm_rag, manual)",
    )

    propagation_source: Literal["injection_point", "caller", "callee"] | None = Field(
        default="injection_point",
        description=(
            "Where propagation starts: injection_point=physical injection location, "
            "caller=for response faults where caller observes delay, "
            "callee=for request faults where callee processes fault"
        ),
    )

    first_hop_config: FirstHopConfig | None = Field(
        default=None,
        description="Override first-hop validation behavior. If None, uses defaults based on src_kind: "
        "span requires src_states; service is lenient with dst_states; container/pod are lenient with src_states.",
    )

    model_config = ConfigDict(arbitrary_types_allowed=True)

    @field_validator("src_states", mode="after")
    @classmethod
    def normalize_src_states(cls, v: list[str]) -> list[str]:
        """Normalize state names to lowercase for consistent matching."""
        return [s.lower() for s in v]

    @field_validator("possible_dst_states", mode="after")
    @classmethod
    def normalize_dst_states(cls, v: list[str]) -> list[str]:
        """Normalize state names to lowercase for consistent matching."""
        return [s.lower() for s in v]

    @field_validator("required_labels", mode="before")
    @classmethod
    def coerce_required_labels(cls, v):
        """Allow JSON arrays / lists / sets to specify ``required_labels``.

        Pydantic accepts ``frozenset`` natively but the JSON loader hands us
        a ``list``; coerce here so callers don't have to.
        """
        if v is None:
            return frozenset()
        if isinstance(v, frozenset):
            return v
        return frozenset(v)

    @field_validator("path", mode="after")
    @classmethod
    def validate_path_xor_single_hop(cls, v, info):
        """Ensure either path OR (edge_kind+direction) is specified, not both."""
        edge_kind = info.data.get("edge_kind")
        direction = info.data.get("direction")

        has_single_hop = edge_kind is not None and direction is not None
        has_path = v is not None and len(v) > 0

        if has_single_hop and has_path:
            raise ValueError("Cannot specify both path and (edge_kind, direction). Use one or the other.")

        if not has_single_hop and not has_path:
            raise ValueError("Must specify either path or (edge_kind, direction)")

        return v

    @property
    def is_multi_hop(self) -> bool:
        """Check if this rule uses multi-hop path."""
        return self.path is not None and len(self.path) > 0

    @property
    def hop_count(self) -> int:
        """Get number of hops in the propagation path."""
        if self.is_multi_hop:
            return len(self.path)  # type: ignore[arg-type]
        return 1
