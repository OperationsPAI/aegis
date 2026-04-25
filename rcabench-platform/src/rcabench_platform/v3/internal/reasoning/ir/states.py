"""Per-PlaceKind IR state enums.

Each kind declares its own StrEnum. A common pivot vocabulary
(``UNKNOWN`` / ``HEALTHY`` / ``SLOW`` / ``ERRORING`` / ``UNAVAILABLE``) is
shared across kinds where semantics apply; kind-specific states
(``MISSING`` for span, ``DEGRADED`` for pod/container, ``RESTARTING`` for pod)
are kept local.

Severity ordering used by synth's multi-adapter merge:
    UNKNOWN < HEALTHY < SLOW < DEGRADED < ERRORING < {UNAVAILABLE, MISSING}
Ties inside the top tier are broken by earliest transition timestamp (handled
in ``synth``; not expressed here).

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


class ServiceStateIR(StrEnum):
    UNKNOWN = auto()
    HEALTHY = auto()
    SLOW = auto()
    DEGRADED = auto()
    ERRORING = auto()
    UNAVAILABLE = auto()


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
