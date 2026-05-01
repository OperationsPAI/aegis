"""ManifestEntryGate — checks v_root features against the entry signature.

Phase 1 stub. Phase 3 implements the actual feature-band check against
real telemetry. The gate is *intentionally not* added to
``default_gates()`` so production pipelines never hit
``NotImplementedError`` — opt-in only.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import (
    GateContext,
    GateResult,
)
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext


class ManifestEntryGate:
    """Verify that ``v_root`` matches ``manifest.entry_signature``.

    Pass criterion (Phase 3): all ``required_features`` match within the
    ``entry_window_sec`` window AND at least ``optional_min_match``
    optional features match. Magnitude bands evaluated against
    measured-value timelines.

    Phase 1: stub. Construction is allowed (so registry / wiring code
    can reference it); calling :meth:`evaluate` raises
    ``NotImplementedError`` so any premature wiring fails loudly.
    """

    name = "manifest_entry"

    def __init__(self, reasoning_ctx: ReasoningContext | None = None) -> None:
        self._reasoning_ctx = reasoning_ctx

    @property
    def reasoning_ctx(self) -> ReasoningContext | None:
        return self._reasoning_ctx

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult:
        raise NotImplementedError(
            "ManifestEntryGate is a Phase 1 stub; Phase 3 implements feature-band "
            "matching. Do not add this gate to default_gates() until then."
        )


__all__ = ["ManifestEntryGate"]
