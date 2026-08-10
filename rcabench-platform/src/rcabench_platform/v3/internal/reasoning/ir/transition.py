"""Transition event emitted by signature-driven StateAdapters.

Transitions are the only thing adapters produce; synth consumes the stream
and reconstructs per-node ``StateTimeline``s. Kept intentionally small and
frozen so adapters cannot mutate state downstream of their own emit site.

The node identity is a string (``uniq_name``, e.g. ``"span|GET /foo"``,
``"pod|ts-order-7b4...-xxx"``) rather than a ``HyperGraph`` int id. Phase 1
deliberately has no graph dependency in the IR core; the str→int binding
happens in Phase 3 when synth is wired into ``rule_matcher``.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from rcabench_platform.v3.internal.reasoning.ir.evidence import Evidence, EvidenceLevel
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind


@dataclass(frozen=True, slots=True)
class Transition:
    node_key: str
    kind: PlaceKind
    at: int
    from_state: str
    to_state: str
    trigger: str
    level: EvidenceLevel
    evidence: Evidence = field(default_factory=dict)  # type: ignore[arg-type]
