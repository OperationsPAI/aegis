"""Per-path validation gates for fault-propagation candidates."""

from rcabench_platform.v3.internal.reasoning.algorithms.gates.base import (
    Gate,
    GateContext,
    GateResult,
    evaluate_path,
)
from rcabench_platform.v3.internal.reasoning.algorithms.gates.drift import DriftGate
from rcabench_platform.v3.internal.reasoning.algorithms.gates.inject_time import (
    INJECT_TIME_TOLERANCE_SECONDS,
    InjectTimeGate,
)
from rcabench_platform.v3.internal.reasoning.algorithms.gates.manifest_entry import (
    ManifestEntryGate,
)
from rcabench_platform.v3.internal.reasoning.algorithms.gates.manifest_layer import (
    ManifestLayerGate,
)
from rcabench_platform.v3.internal.reasoning.algorithms.gates.temporal import TemporalGate
from rcabench_platform.v3.internal.reasoning.algorithms.gates.topology import TopologyGate
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext


def default_gates() -> list[Gate]:
    """Generic 4-gate stack for fault types without a manifest.

    The manifest-aware gates are intentionally omitted — they require a
    populated ``ReasoningContext`` and would no-op (returning a soft
    pass) for the generic path. Callers with a registered manifest
    should use :func:`manifest_aware_gates` instead.
    """
    return [TopologyGate(), DriftGate(), TemporalGate(), InjectTimeGate()]


def manifest_aware_gates(reasoning_ctx: ReasoningContext) -> list[Gate]:
    """Manifest-driven gate stack used when ``reasoning_ctx.manifest`` exists.

    Per FORGE rework §3.1 / §3.2:

    * ``ManifestEntryGate`` replaces ``InjectTimeGate``'s role for the
      injection point — the entry signature is a stronger predicate
      than "did anything happen in the window".
    * ``ManifestLayerGate`` replaces ``TopologyGate`` + ``DriftGate``
      with a single magnitude-band check per layer. The rule-based
      topology check is unnecessary because the manifest's
      ``edge_kinds`` field already constrains admissible edges, and the
      binary drift check is subsumed by the layer's magnitude bands
      (a non-matching node either has no observable feature or has one
      out of band — both rejected).
    * ``TemporalGate`` is preserved with its FORGE-§3.3 reversed-order
      tolerance.

    If ``reasoning_ctx.manifest`` is ``None`` the function returns
    :func:`default_gates`, so callers can safely route through this
    factory regardless of whether a manifest is present.
    """
    if reasoning_ctx.manifest is None:
        return default_gates()
    return [
        ManifestEntryGate(reasoning_ctx),
        ManifestLayerGate(reasoning_ctx),
        TemporalGate(),
        InjectTimeGate(),
    ]


__all__ = [
    "Gate",
    "GateContext",
    "GateResult",
    "evaluate_path",
    "TopologyGate",
    "DriftGate",
    "TemporalGate",
    "InjectTimeGate",
    "INJECT_TIME_TOLERANCE_SECONDS",
    "ManifestEntryGate",
    "ManifestLayerGate",
    "default_gates",
    "manifest_aware_gates",
]
