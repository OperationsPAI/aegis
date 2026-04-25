"""IR pipeline driver: assemble adapters, synth timelines, run inference fixpoint.

Phase 2 entry point. Constructs the standard adapter set (Injection +
Traces + K8sMetrics + JVM) explicitly and feeds their transition stream
into ``synth_timelines`` + ``run_fixpoint``. The ``@register_adapter``
registry is preserved for future plug-in style assembly; this driver
wires the core set manually so that callers don't need to know which
adapter classes are registered.

Execution is two-phase:

1. The observation adapters (Injection, Traces, K8sMetrics, JVM, plus any
   ``extra_adapters``) emit transitions, which are synthesised into a first
   pass of timelines.
2. ``StructuralInheritanceAdapter`` is then constructed with those phase-1
   timelines and the graph, and emits inferred transitions for derived
   nodes (pod / service / span) when an infra-level node went unavailable
   or degraded but its derived nodes have no observation. The combined
   transition stream is synthesised once more so the final timelines
   reflect both observed and inherited state.

The structural pass is the only place where containment-driven state is
expressed at the IR layer; rule matching and propagation continue to run
unchanged downstream of synth.
"""

from __future__ import annotations

from collections.abc import Iterable

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext, StateAdapter
from rcabench_platform.v3.internal.reasoning.ir.adapters.injection import InjectionAdapter
from rcabench_platform.v3.internal.reasoning.ir.adapters.jvm import JvmAugmenterAdapter
from rcabench_platform.v3.internal.reasoning.ir.adapters.k8s_metrics import K8sMetricsAdapter
from rcabench_platform.v3.internal.reasoning.ir.adapters.structural_inheritance import (
    StructuralInheritanceAdapter,
)
from rcabench_platform.v3.internal.reasoning.ir.adapters.traces import TraceStateAdapter
from rcabench_platform.v3.internal.reasoning.ir.inference import InferenceRule, run_fixpoint
from rcabench_platform.v3.internal.reasoning.ir.synth import synth_timelines
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph
from rcabench_platform.v3.internal.reasoning.models.injection import ResolvedInjection


def run_reasoning_ir(
    *,
    graph: HyperGraph,
    ctx: AdapterContext,
    resolved: ResolvedInjection,
    injection_at: int,
    baseline_traces: object,
    abnormal_traces: object,
    extra_adapters: Iterable[StateAdapter] | None = None,
    inference_rules: list[InferenceRule] | None = None,
    observation_start: int | None = None,
    observation_end: int | None = None,
) -> dict[str, StateTimeline]:
    """Build canonical ``StateTimeline``s for every node observed by any adapter.

    Args:
        graph: HyperGraph containing pod/container nodes whose
            ``baseline_metrics`` / ``abnormal_metrics`` will be read.
        ctx: AdapterContext (case_name, datapack_dir).
        resolved: ResolvedInjection — feeds the InjectionAdapter seed.
        injection_at: Unix-seconds time the fault was injected.
        baseline_traces / abnormal_traces: polars DataFrames; passed
            untyped because polars is an optional import path elsewhere
            and this signature is shared with tests that pass mocks.
        extra_adapters: optional adapters appended to the standard set
            (e.g. JVM augmenter once it exists).
        inference_rules: rules for the post-synth fixpoint pass; default
            is empty (no rules).
        observation_start / observation_end: pinned observation bounds
            forwarded to ``synth_timelines``.
    """
    observation_adapters: list[StateAdapter] = [
        InjectionAdapter(resolved=resolved, injection_at=injection_at),
        TraceStateAdapter(baseline_traces=baseline_traces, abnormal_traces=abnormal_traces),  # type: ignore[arg-type]
        K8sMetricsAdapter(graph=graph),
        # Specialization augmenters — safe to wire unconditionally; each one
        # no-ops on stacks that don't carry the metrics it cares about.
        JvmAugmenterAdapter(graph=graph),
    ]
    if extra_adapters:
        observation_adapters.extend(extra_adapters)

    transitions: list[Transition] = []
    for adapter in observation_adapters:
        transitions.extend(adapter.emit(ctx))

    phase1_timelines = synth_timelines(
        transitions,
        observation_start=observation_start,
        observation_end=observation_end,
    )

    structural = StructuralInheritanceAdapter(graph=graph, prior_timelines=phase1_timelines)
    transitions.extend(structural.emit(ctx))

    timelines = synth_timelines(
        transitions,
        observation_start=observation_start,
        observation_end=observation_end,
    )
    if inference_rules:
        timelines = run_fixpoint(timelines, inference_rules)
    return timelines


__all__ = ["run_reasoning_ir"]
