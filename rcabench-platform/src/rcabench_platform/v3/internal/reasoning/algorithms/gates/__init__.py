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


def default_gates() -> list[Gate]:
    # ManifestEntryGate / ManifestLayerGate intentionally NOT included:
    # they are Phase 1 stubs that raise NotImplementedError. Phase 3
    # supplies a separate constructor for the manifest-aware pipeline.
    return [TopologyGate(), DriftGate(), TemporalGate(), InjectTimeGate()]


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
]
