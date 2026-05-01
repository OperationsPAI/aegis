"""ManifestLayerGate — per-edge check against manifest derivation layers.

Phase 1 stub. Phase 3 implements layer-by-layer expansion: a downstream
node admitted at ``derivation_layers[k]`` must match at least one
``expected_features`` entry, with magnitude bands optionally decayed via
``magnitude_decay``.

Like :class:`ManifestEntryGate`, this gate is opt-in: not in
``default_gates()`` so production pipelines never trip
``NotImplementedError``.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import (
    GateContext,
    GateResult,
)
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import CandidatePath
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext


class ManifestLayerGate:
    """Verify that each path edge satisfies the corresponding layer's
    ``expected_features``.

    Phase 3 implementation walks ``path`` in tandem with
    ``manifest.derivation_layers`` (layer index = hop index, capped at
    ``len(layers)``). Each downstream node must match at least one
    expected feature for that layer.
    """

    name = "manifest_layer"

    def __init__(self, reasoning_ctx: ReasoningContext | None = None) -> None:
        self._reasoning_ctx = reasoning_ctx

    @property
    def reasoning_ctx(self) -> ReasoningContext | None:
        return self._reasoning_ctx

    def evaluate(self, path: CandidatePath, ctx: GateContext) -> GateResult:
        raise NotImplementedError(
            "ManifestLayerGate is a Phase 1 stub; Phase 3 implements per-layer "
            "expected_features matching. Do not add this gate to default_gates() "
            "until then."
        )


__all__ = ["ManifestLayerGate"]
