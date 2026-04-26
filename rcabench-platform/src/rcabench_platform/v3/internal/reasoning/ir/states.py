"""Per-PlaceKind IR state enums.

Each kind declares its own StrEnum. A common pivot vocabulary
(``UNKNOWN`` / ``HEALTHY`` / ``SLOW`` / ``ERRORING`` / ``UNAVAILABLE`` /
``SILENT``) is shared across kinds where semantics apply; kind-specific
states (``MISSING`` for span, ``DEGRADED`` for pod/container,
``RESTARTING`` for pod) are kept local. ``SILENT`` (Class E — traffic
isolation: entity is alive but the expected request flow has dropped to
near zero) applies only to request-layer kinds (span, service); pod and
container have no flow concept and therefore do not carry SILENT.

Severity ordering used by synth's multi-adapter merge:
    UNKNOWN < HEALTHY < SLOW < {DEGRADED, RESTARTING} < {ERRORING, SILENT} < {UNAVAILABLE, MISSING}
Ties inside the top tier are broken by earliest transition timestamp (handled
in ``synth``; not expressed here).

See ``docs/reasoning-feature-taxonomy.md`` §11.1 for the tier admission
rule that pins SILENT at tier 4.

Phase 1 keeps the vocabulary minimal — additions (e.g. ``RESTARTING``)
should land when the concrete adapter that emits them is written, not
pre-emptively.
"""

from __future__ import annotations

from enum import auto

from rcabench_platform.compat import StrEnum


class SpanStateIR(StrEnum):
    UNKNOWN = auto()
    HEALTHY = auto()
    SLOW = auto()
    ERRORING = auto()
    UNAVAILABLE = auto()
    MISSING = auto()
    SILENT = auto()


class ServiceStateIR(StrEnum):
    UNKNOWN = auto()
    HEALTHY = auto()
    SLOW = auto()
    DEGRADED = auto()
    ERRORING = auto()
    UNAVAILABLE = auto()
    SILENT = auto()


class PodStateIR(StrEnum):
    UNKNOWN = auto()
    HEALTHY = auto()
    DEGRADED = auto()
    RESTARTING = auto()
    ERRORING = auto()
    UNAVAILABLE = auto()


class ContainerStateIR(StrEnum):
    UNKNOWN = auto()
    HEALTHY = auto()
    DEGRADED = auto()
    ERRORING = auto()
    UNAVAILABLE = auto()


_SEVERITY: dict[str, int] = {
    "unknown": 0,
    "healthy": 1,
    "slow": 2,
    "degraded": 3,
    "restarting": 3,
    "erroring": 4,
    "silent": 4,
    "unavailable": 5,
    "missing": 5,
}


def severity(state: str) -> int:
    """Return the severity rank for a state name.

    Unknown state names rank at 0 so buggy adapters degrade silently into
    UNKNOWN-equivalent rather than poisoning the merge with an unbounded
    severity.
    """
    return _SEVERITY.get(state, 0)
