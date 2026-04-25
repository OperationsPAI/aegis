"""IR pipeline driver: assemble adapters, synth timelines, run inference fixpoint.

Phase 2 entry point. Constructs the standard adapter trio (Injection +
Traces + K8sMetrics) explicitly and feeds their transition stream into
``synth_timelines`` + ``run_fixpoint``. The ``@register_adapter`` registry
is preserved for future plug-in style assembly; this driver wires the
core trio manually so that callers don't need to know which adapter
classes are registered.
"""

from __future__ import annotations

from collections.abc import Iterable

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext, StateAdapter
from rcabench_platform.v3.internal.reasoning.ir.adapters.injection import InjectionAdapter
from rcabench_platform.v3.internal.reasoning.ir.adapters.jvm import JvmAugmenterAdapter
from rcabench_platform.v3.internal.reasoning.ir.adapters.k8s_metrics import K8sMetricsAdapter
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
        extra_adapters: optional adapters appended to the standard trio
            (e.g. JVM augmenter once it exists).
        inference_rules: rules for the post-synth fixpoint pass; default
            is empty (no rules).
        observation_start / observation_end: pinned observation bounds
            forwarded to ``synth_timelines``.
    """
    adapters: list[StateAdapter] = [
        InjectionAdapter(resolved=resolved, injection_at=injection_at),
        TraceStateAdapter(baseline_traces=baseline_traces, abnormal_traces=abnormal_traces),  # type: ignore[arg-type]
        K8sMetricsAdapter(graph=graph),
        # Specialization augmenters — safe to wire unconditionally; each one
        # no-ops on stacks that don't carry the metrics it cares about.
        JvmAugmenterAdapter(graph=graph),
    ]
    if extra_adapters:
        adapters.extend(extra_adapters)

    transitions: list[Transition] = []
    for adapter in adapters:
        transitions.extend(adapter.emit(ctx))

    timelines = synth_timelines(
        transitions,
        observation_start=observation_start,
        observation_end=observation_end,
    )
    if inference_rules:
        timelines = run_fixpoint(timelines, inference_rules)
    return timelines


__all__ = ["run_reasoning_ir"]
