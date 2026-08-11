"""Deterministic fault_type → canonical seed-state mapping.

Phase 6 of #163. The chaos-tool fault catalog is a closed contract: every
fault_type the injector knows how to apply has a single, unambiguous
canonical effect on the injection node. The reasoning pipeline must seed
that canonical effect *before* it looks at any signal, so propagation has
a non-UNKNOWN starting point on every datapack — even on stacks whose
observability is too coarse for adapters to derive it.

This module is the single source of truth for that mapping. It is
consumed by:

- :class:`InjectionAdapter` (`ir/adapters/injection.py`) — emits the
  structural seed transition.
- :class:`StartingPointResolver` (`algorithms/starting_point_resolver.py`)
  — consults the mapping as the **first** step before falling back to
  rule / topology heuristics.

# Canonical mapping

The "tier" column is one of the canonical state tiers shared across kinds
(`unavailable`, `erroring`, `slow`, `degraded`). The actual on-the-wire
state value is then resolved against the kind-specific IR enum
(`SpanStateIR`, `ServiceStateIR`, `PodStateIR`, `ContainerStateIR`) by
:func:`pick_canonical_state` — e.g. tier ``degraded`` on a span falls back
to ``slow`` because :class:`SpanStateIR` does not have a ``DEGRADED`` member.

| Fault type                  | Tier         | Notes                                        |
| --------------------------- | ------------ | -------------------------------------------- |
| `PodKill`                   | unavailable  | container/pod terminated                     |
| `PodFailure`                | unavailable  | pod stops serving                            |
| `ContainerKill`             | unavailable  | container terminated                         |
| `MemoryStress`              | degraded     | resource pressure, no outright failure       |
| `CPUStress`                 | degraded     | resource pressure, no outright failure       |
| `HTTPRequestAbort`          | erroring     | server-side observes request abort           |
| `HTTPResponseAbort`         | erroring     | client-side observes response abort          |
| `HTTPRequestDelay`          | slow         | request-side latency                         |
| `HTTPResponseDelay`         | slow         | response-side latency                        |
| `HTTPResponseReplaceBody`   | erroring     | malformed payload → caller treats as error   |
| `HTTPResponsePatchBody`     | erroring     | malformed payload → caller treats as error   |
| `HTTPRequestReplacePath`    | erroring     | wrong path → 4xx/5xx                         |
| `HTTPRequestReplaceMethod`  | erroring     | wrong method → 405/4xx                       |
| `HTTPResponseReplaceCode`   | erroring     | non-2xx forced                               |
| `DNSError`                  | silent       | outbound flow silenced (Class E)             |
| `DNSRandom`                 | silent       | outbound flow silenced (Class E)             |
| `TimeSkew`                  | degraded     | clock drift; ambiguous (see notes)           |
| `NetworkDelay`              | slow         | latency-style                                |
| `NetworkLoss`               | degraded     | partial loss; not full outage                |
| `NetworkDuplicate`          | degraded     | duplicate packets; service still flowing     |
| `NetworkCorrupt`            | degraded     | corrupted packets; degraded but reachable    |
| `NetworkBandwidth`          | slow         | throughput cap                               |
| `NetworkPartition`          | silent       | inbound flow silenced (Class E)              |
| `JVMLatency`                | slow         | method-level injected delay                  |
| `JVMReturn`                 | erroring     | forced return value → semantic error         |
| `JVMException`              | erroring     | thrown exception                             |
| `JVMGarbageCollector`       | slow         | forced GC pause                              |
| `JVMCPUStress`              | degraded     | JVM-level CPU pressure                       |
| `JVMMemoryStress`           | degraded     | JVM-level memory pressure                    |
| `JVMMySQLLatency`           | slow         | DB-call latency                              |
| `JVMMySQLException`         | erroring     | DB-call exception                            |

# Ambiguous cases

A handful of fault types map to an arguably different canonical tier
depending on observation granularity. This file documents the choices we
made; these are intentional, conservative defaults:

- ``TimeSkew`` → ``degraded`` (not ``slow``): clock drift breaks
  consistency more than it adds latency in the typical microservice case.
- ``NetworkLoss`` / ``NetworkDuplicate`` / ``NetworkCorrupt`` →
  ``degraded`` (not ``erroring``): packet-level disturbance, retries
  often paper over it; full failure is reserved for ``NetworkPartition``.
- ``JVMCPUStress`` / ``JVMMemoryStress`` → ``degraded``: a JVM-level
  augmenter (Phase 4) may later promote these to specialization labels
  like ``HIGH_CPU`` / ``OOM_KILLED``.
- ``HTTPResponseReplaceBody`` / ``HTTPResponsePatchBody`` → ``erroring``:
  malformed payload to the caller is functionally an error even though
  the HTTP status code may be 2xx. Conservative call.

# Unknown fault types

Any ``fault_type_name`` not in :data:`FAULT_TYPE_TO_SEED_TIER` is treated
as an unknown chaos-tool extension. In that case
:func:`canonical_seed_tier` returns :data:`UNKNOWN_FAULT_DEFAULT_TIER`
(``degraded``) — the most-conservative tier that still gives propagation
something to start from. Callers MUST log a warning when they hit this
path (see :class:`InjectionAdapter`).
"""

from __future__ import annotations

from enum import Enum
from typing import Final

from rcabench_platform.v3.internal.reasoning.ir.states import (
    ContainerStateIR,
    PodStateIR,
    ServiceStateIR,
    SpanStateIR,
)
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind

# Canonical severity tier — shared across PlaceKinds. The kind-appropriate
# concrete state is resolved by :func:`pick_canonical_state`.
SeedTier = str  # one of: "unavailable" | "erroring" | "slow" | "degraded" | "silent"

# Closed set of valid tiers.
SEED_TIERS: Final[frozenset[str]] = frozenset({"unavailable", "erroring", "slow", "degraded", "silent"})

# Default tier for unknown fault types. ``degraded`` is the most
# conservative choice that still gives propagation a non-UNKNOWN seed.
UNKNOWN_FAULT_DEFAULT_TIER: Final[SeedTier] = "degraded"


# Authoritative fault_type_name → canonical seed tier map. Every entry of
# ``FAULT_TYPES`` in ``models/injection.py`` MUST appear here.
FAULT_TYPE_TO_SEED_TIER: Final[dict[str, SeedTier]] = {
    # Pod / container lifecycle
    "PodKill": "unavailable",
    "PodFailure": "unavailable",
    "ContainerKill": "unavailable",
    # Container-level resource pressure
    "MemoryStress": "degraded",
    "CPUStress": "degraded",
    # HTTP request-side
    "HTTPRequestAbort": "erroring",
    "HTTPRequestDelay": "slow",
    "HTTPRequestReplacePath": "erroring",
    "HTTPRequestReplaceMethod": "erroring",
    # HTTP response-side
    "HTTPResponseAbort": "erroring",
    "HTTPResponseDelay": "slow",
    "HTTPResponseReplaceBody": "erroring",
    "HTTPResponsePatchBody": "erroring",
    "HTTPResponseReplaceCode": "erroring",
    # DNS resolution failure → outbound flow silenced (Class E per §3.E)
    "DNSError": "silent",
    "DNSRandom": "silent",
    # Time
    "TimeSkew": "degraded",
    # Network
    "NetworkDelay": "slow",
    "NetworkLoss": "degraded",
    "NetworkDuplicate": "degraded",
    "NetworkCorrupt": "degraded",
    "NetworkBandwidth": "slow",
    # Inbound flow silenced (Class E per §3.E)
    "NetworkPartition": "silent",
    # JVM method-level
    "JVMLatency": "slow",
    "JVMReturn": "erroring",
    "JVMException": "erroring",
    "JVMGarbageCollector": "slow",
    "JVMCPUStress": "degraded",
    "JVMMemoryStress": "degraded",
    # JVM database-level
    "JVMMySQLLatency": "slow",
    "JVMMySQLException": "erroring",
    # JVM runtime-level method mutator: chaos-mesh's JVMRuntimeMutator
    # rewrites bytecode to short-circuit / alter return / throw. The
    # dominant observation in real datasets is the affected service's
    # outbound RPC fan-out collapsing — both inbound and outbound spans
    # on the service plane go silent in abnormal — so the canonical
    # tier is silent. Throw-mutations look like erroring at the entry
    # but downstream the cascade is observability-bounded the same way;
    # silent's structural cascade admission handles both.
    "JVMRuntimeMutator": "silent",
}


# Map start_kind string → PlaceKind. Used by both InjectionAdapter (to
# pick the right state enum) and StartingPointResolver (to short-circuit
# kind-aware decisions).
START_KIND_TO_PLACE_KIND: Final[dict[str, PlaceKind]] = {
    "span": PlaceKind.span,
    "service": PlaceKind.service,
    "pod": PlaceKind.pod,
    "container": PlaceKind.container,
}


_ENUM_BY_KIND: Final[dict[PlaceKind, type[Enum]]] = {
    PlaceKind.span: SpanStateIR,
    PlaceKind.service: ServiceStateIR,
    PlaceKind.pod: PodStateIR,
    PlaceKind.container: ContainerStateIR,
}


def canonical_seed_tier(fault_type_name: str) -> tuple[SeedTier, bool]:
    """Return ``(tier, is_known)`` for a fault type name.

    ``is_known`` is ``True`` iff ``fault_type_name`` appears in
    :data:`FAULT_TYPE_TO_SEED_TIER`. When ``False`` the caller has hit an
    unknown chaos-tool fault and SHOULD log a warning before using the
    returned (default) tier.
    """
    tier = FAULT_TYPE_TO_SEED_TIER.get(fault_type_name)
    if tier is None:
        return UNKNOWN_FAULT_DEFAULT_TIER, False
    return tier, True


def pick_canonical_state(kind: PlaceKind, tier: SeedTier) -> str:
    """Resolve a canonical tier to the on-the-wire state name for ``kind``.

    Falls back gracefully if the kind-specific enum doesn't define the
    tier verbatim — e.g. :class:`SpanStateIR` has no ``DEGRADED`` member,
    so ``degraded`` on a span resolves to ``slow``.
    """
    enum_cls = _ENUM_BY_KIND.get(kind)
    if enum_cls is None:
        # Should never happen for the four supported kinds; return the
        # tier as-is so callers can still emit a sensible string.
        return tier
    members = enum_cls.__members__
    key = tier.upper()
    if key in members:
        return str(members[key].value)
    # Spans have no DEGRADED — collapse to SLOW which is the closest
    # severity-preserving neighbour.
    if tier == "degraded" and "SLOW" in members:
        return str(members["SLOW"].value)
    # Last-ditch: return the tier verbatim. Synth's severity table
    # ranks unknown strings at 0 so the merge degrades safely.
    return tier


__all__ = [
    "FAULT_TYPE_TO_SEED_TIER",
    "SEED_TIERS",
    "START_KIND_TO_PLACE_KIND",
    "SeedTier",
    "UNKNOWN_FAULT_DEFAULT_TIER",
    "canonical_seed_tier",
    "pick_canonical_state",
]
