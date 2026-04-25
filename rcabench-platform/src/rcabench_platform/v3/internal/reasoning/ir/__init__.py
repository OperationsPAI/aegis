"""State-machine IR for the reasoning pipeline.

Per-kind state enums + stateful adapter protocol + transition stream synth.
Phase 1 scope: zero wiring into the legacy detector / CLI. See issue #165.
"""

from rcabench_platform.v3.internal.reasoning.ir.adapter import (
    AdapterContext,
    StateAdapter,
    get_registered_adapters,
    register_adapter,
)
from rcabench_platform.v3.internal.reasoning.ir.evidence import Evidence, EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.inference import InferenceRule, run_fixpoint
from rcabench_platform.v3.internal.reasoning.ir.pipeline import run_reasoning_ir
from rcabench_platform.v3.internal.reasoning.ir.states import (
    ContainerStateIR,
    PodStateIR,
    ServiceStateIR,
    SpanStateIR,
    severity,
)
from rcabench_platform.v3.internal.reasoning.ir.synth import synth_timelines
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition

__all__ = [
    "AdapterContext",
    "ContainerStateIR",
    "Evidence",
    "EvidenceLevel",
    "InferenceRule",
    "PodStateIR",
    "ServiceStateIR",
    "SpanStateIR",
    "StateAdapter",
    "StateTimeline",
    "TimelineWindow",
    "Transition",
    "get_registered_adapters",
    "register_adapter",
    "run_fixpoint",
    "run_reasoning_ir",
    "severity",
    "synth_timelines",
]
