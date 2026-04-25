"""Per-node state timeline produced by synth.

A ``StateTimeline`` is a contiguous list of ``TimelineWindow`` spanning the
observation period. Each window carries its winning state plus the evidence
level that earned it, so downstream consumers can decide whether to trust
or discard it.

The first window of every timeline starts in ``UNKNOWN`` with
``EvidenceLevel.inferred`` (conceptually "no signal yet") — this is what
makes UNKNOWN rewritable by the inference layer without losing the
distinction from observed-HEALTHY.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from rcabench_platform.v3.internal.reasoning.ir.evidence import Evidence, EvidenceLevel
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind


@dataclass(frozen=True, slots=True)
class TimelineWindow:
    start: int
    end: int
    state: str
    level: EvidenceLevel
    trigger: str
    evidence: Evidence = field(default_factory=dict)  # type: ignore[arg-type]


@dataclass(frozen=True, slots=True)
class StateTimeline:
    node_key: str
    kind: PlaceKind
    windows: tuple[TimelineWindow, ...]

    def state_at(self, t: int) -> str | None:
        for w in self.windows:
            if w.start <= t < w.end:
                return w.state
        return None
