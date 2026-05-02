"""ManifestAwarePathBuilder — in-build manifest-driven expansion (Phase 5).

Drives :class:`ManifestAwarePathBuilder` over a synthetic 3-layer
manifest + synthetic graph and verifies:

* Only manifest-conformant paths are produced (out-of-layer edges
  rejected; nodes whose features fall outside the band rejected).
* Path count is bounded by ``prod(max_fanout)`` per the SCHEMA.md
  contract.
* Hand-off forks happen at the right ``on_layer`` and only when the
  trigger ``threshold`` fires.
* Fall-back to the generic builder when no manifest is registered
  (covered indirectly in :mod:`test_propagator_canonical` —
  re-asserted here to keep the routing-level claim explicit).
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.algorithms.manifest_path_builder import (
    ManifestAwarePathBuilder,
)
from rcabench_platform.v3.internal.reasoning.algorithms.propagator import FaultPropagator
from rcabench_platform.v3.internal.reasoning.algorithms.temporal_validator import TemporalValidator
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline, TimelineWindow
from rcabench_platform.v3.internal.reasoning.manifests import (
    Feature,
    FeatureKind,
    ManifestRegistry,
    ReasoningContext,
)
from rcabench_platform.v3.internal.reasoning.manifests.schema import (
    DerivationLayer,
    EntrySignature,
    FaultManifest,
    FeatureMatch,
    HandOff,
    HandOffTrigger,
)
from rcabench_platform.v3.internal.reasoning.models.fault_seed import (
    FAULT_TYPE_TO_SEED_TIER,
)
from rcabench_platform.v3.internal.reasoning.models.graph import (
    CallsEdgeData,
    DepKind,
    Edge,
    HyperGraph,
    Node,
    PlaceKind,
)
from rcabench_platform.v3.internal.reasoning.rules.builtin_rules import get_builtin_rules

# ---------------------------------------------------------------------------
# Fixture builders
# ---------------------------------------------------------------------------


def _calls_edge(src_id: int, dst_id: int, src_name: str, dst_name: str) -> Edge:
    return Edge(
        src_id=src_id,
        dst_id=dst_id,
        src_name=src_name,
        dst_name=dst_name,
        kind=DepKind.calls,
        data=CallsEdgeData(),
    )


def _tl(name: str, kind: PlaceKind, state: str, start: int = 1000, end: int = 2000) -> StateTimeline:
    return StateTimeline(
        node_key=name,
        kind=kind,
        windows=(
            TimelineWindow(
                start=start,
                end=end,
                state=state,
                level=EvidenceLevel.observed,
                trigger="test",
                evidence={},
            ),
        ),
    )


def _make_chain(n_spans: int, base_start: int = 1000) -> tuple[HyperGraph, dict[str, StateTimeline], list[int]]:
    """Build a linear caller→callee chain of ``n_spans`` spans.

    Returns the graph, a per-node-key timelines map, and the ordered
    span node-id list (callee[0] is the v_root; subsequent nodes are
    its backward callers).
    """
    g = HyperGraph()
    node_ids: list[int] = []
    span_nodes: list[Node] = []
    for i in range(n_spans):
        n = g.add_node(Node(kind=PlaceKind.span, self_name=f"svc-{i}::POST /api"))
        span_nodes.append(n)
        assert n.id is not None
        node_ids.append(n.id)

    for i in range(n_spans - 1):
        # caller calls callee. v_root is span_nodes[0] (callee), so the
        # chain is built backward: span_nodes[i+1] -> span_nodes[i].
        caller = span_nodes[i + 1]
        callee = span_nodes[i]
        assert caller.id is not None and callee.id is not None
        g.add_edge(_calls_edge(caller.id, callee.id, caller.uniq_name, callee.uniq_name))

    timelines: dict[str, StateTimeline] = {}
    for i, n in enumerate(span_nodes):
        timelines[n.uniq_name] = _tl(
            n.uniq_name,
            PlaceKind.span,
            "slow",
            start=base_start + i * 5,
            end=base_start + 1000,
        )
    return g, timelines, node_ids


def _make_manifest(
    fault_type_name: str = "JVMException",
    n_layers: int = 3,
    max_fanout: int = 4,
    handoff_target: str | None = None,
    handoff_on_layer: int = 2,
) -> FaultManifest:
    """Build a synthetic manifest using ``calls/backward`` for every layer.

    Picks a fault_type_name that exists in FAULT_TYPE_TO_SEED_TIER. The
    chosen entry-signature feature is ``span.error_rate`` so we can
    drive entry pass/fail by setting feature_samples.
    """
    layers = [
        DerivationLayer(
            layer=k + 1,
            edge_kinds=["calls"],
            edge_directions=["backward"],
            expected_features=[
                FeatureMatch(
                    kind=FeatureKind.span,
                    feature=Feature.latency_p99_ratio,
                    band=(1.2, float("inf")),
                ),
            ],
            max_fanout=max_fanout,
        )
        for k in range(n_layers)
    ]

    handoffs: list[HandOff] = []
    if handoff_target is not None:
        handoffs.append(
            HandOff(
                to=handoff_target,
                trigger=HandOffTrigger(
                    kind=FeatureKind.span,
                    feature=Feature.error_rate,
                    threshold=0.5,
                ),
                on_layer=handoff_on_layer,
                rationale="test",
            )
        )

    return FaultManifest(
        fault_type_name=fault_type_name,
        target_kind="span",
        seed_tier=FAULT_TYPE_TO_SEED_TIER[fault_type_name],
        description="synthetic test manifest",
        entry_signature=EntrySignature(
            entry_window_sec=30,
            required_features=[
                FeatureMatch(
                    kind=FeatureKind.span,
                    feature=Feature.error_rate,
                    band=(0.05, 1.0),
                )
            ],
            optional_features=[],
            optional_min_match=0,
        ),
        derivation_layers=layers,
        hand_offs=handoffs,
    )


def _samples_for_chain(
    node_ids: list[int],
    p99_value: float = 2.0,
    error_rate_value: float = 0.1,
) -> dict:
    """All nodes in the chain have the same in-band feature values.

    Drives admission to be 100%; max_fanout is the only bound.
    """
    samples: dict = {}
    for nid in node_ids:
        samples[(nid, FeatureKind.span, Feature.latency_p99_ratio)] = p99_value
        samples[(nid, FeatureKind.span, Feature.error_rate)] = error_rate_value
    return samples


# ---------------------------------------------------------------------------
# 1. Manifest-driven expansion produces only manifest-conformant paths.
# ---------------------------------------------------------------------------


def test_manifest_path_builder_emits_only_manifest_conformant_paths() -> None:
    g, timelines, node_ids = _make_chain(n_spans=4)
    v_root = node_ids[0]
    manifest = _make_manifest(n_layers=3, max_fanout=4)
    rctx = ReasoningContext(
        fault_type_name=manifest.fault_type_name,
        manifest=manifest,
        v_root_node_id=v_root,
        t0=900,
        feature_samples=_samples_for_chain(node_ids),
    )

    builder = ManifestAwarePathBuilder(
        graph=g,
        timelines=timelines,
        temporal_validator=TemporalValidator(timelines),
        reasoning_ctx=rctx,
    )
    out = builder.build_all(v_root)

    # Every emitted path: starts at v_root, every edge is calls_backward,
    # every interior node is in node_ids.
    assert out.paths, "expected at least one manifest-conformant path"
    for p in out.paths:
        assert p.node_ids[0] == v_root
        for ed in p.edge_descs:
            assert ed == "calls_backward", f"unexpected edge {ed!r}"
        for nid in p.node_ids:
            assert nid in node_ids


def test_manifest_path_builder_rejects_nodes_outside_band() -> None:
    """Nodes whose ``latency_p99_ratio`` is below the band are not admitted.

    Uses ``HTTPRequestDelay`` (seed_tier=slow) so strict-band semantics
    apply at every layer: the cascade really must stop where the band
    fails. The erroring tier intentionally relaxes this (see
    :func:`test_manifest_path_builder_extends_erroring_cascade_past_band`).
    """
    g, timelines, node_ids = _make_chain(n_spans=4)
    v_root = node_ids[0]
    # HTTPRequestDelay has seed_tier=slow → strict bands all the way down.
    manifest = _make_manifest(n_layers=3, fault_type_name="HTTPRequestDelay")
    # Only the immediate caller (node_ids[1]) is in band; node_ids[2]
    # and node_ids[3] are below — chain should stop at depth 1.
    samples: dict = {}
    for nid in node_ids:
        samples[(nid, FeatureKind.span, Feature.error_rate)] = 0.1
    samples[(node_ids[1], FeatureKind.span, Feature.latency_p99_ratio)] = 2.0
    samples[(node_ids[2], FeatureKind.span, Feature.latency_p99_ratio)] = 0.5
    samples[(node_ids[3], FeatureKind.span, Feature.latency_p99_ratio)] = 0.5

    rctx = ReasoningContext(
        fault_type_name=manifest.fault_type_name,
        manifest=manifest,
        v_root_node_id=v_root,
        t0=900,
        feature_samples=samples,
    )
    builder = ManifestAwarePathBuilder(
        graph=g,
        timelines=timelines,
        temporal_validator=TemporalValidator(timelines),
        reasoning_ctx=rctx,
    )
    out = builder.build_all(v_root)

    # Every path stops at depth 1 (one edge, two nodes).
    assert out.paths
    assert all(len(p.node_ids) == 2 for p in out.paths)
    # The single admitted child must be node_ids[1].
    assert all(p.node_ids[1] == node_ids[1] for p in out.paths)


def test_manifest_path_builder_extends_erroring_cascade_past_band() -> None:
    """Erroring tier: once a strict admission has anchored the cascade at
    layer 1, deeper hops are admitted structurally even when the strict
    band fails.

    Encodes the per-physics rule for the erroring tier: the bad response
    really did propagate to every transitive caller along the request
    graph, but Spring/Java exception swallowing silences error_rate
    observations partway up the chain. The strict layer-1 admission is
    the corroboration anchor; the deep-cascade extension picks up where
    the strict bands stop firing.

    Mirror of :func:`test_manifest_path_builder_rejects_nodes_outside_band`
    but with ``JVMException`` (seed_tier=erroring): the band-failed depth-2
    and depth-3 nodes are now admitted via the extension.
    """
    g, timelines, node_ids = _make_chain(n_spans=4)
    v_root = node_ids[0]
    # JVMException has seed_tier=erroring → deep-cascade extension runs.
    manifest = _make_manifest(n_layers=3, fault_type_name="JVMException")
    samples: dict = {}
    for nid in node_ids:
        samples[(nid, FeatureKind.span, Feature.error_rate)] = 0.1
    samples[(node_ids[1], FeatureKind.span, Feature.latency_p99_ratio)] = 2.0
    samples[(node_ids[2], FeatureKind.span, Feature.latency_p99_ratio)] = 0.5
    samples[(node_ids[3], FeatureKind.span, Feature.latency_p99_ratio)] = 0.5

    rctx = ReasoningContext(
        fault_type_name=manifest.fault_type_name,
        manifest=manifest,
        v_root_node_id=v_root,
        t0=900,
        feature_samples=samples,
    )
    builder = ManifestAwarePathBuilder(
        graph=g,
        timelines=timelines,
        temporal_validator=TemporalValidator(timelines),
        reasoning_ctx=rctx,
    )
    out = builder.build_all(v_root)

    # The cascade reached node_ids[3] (depth 3) despite the band miss.
    deepest = max(len(p.node_ids) for p in out.paths) if out.paths else 0
    assert deepest >= 4, (
        f"erroring-tier extension should reach depth >=3 (4 nodes), "
        f"got deepest={deepest}"
    )
    # The deeper hops carry the ``Lext`` rule_id stamp.
    assert any(
        any(rid.endswith(":Lext") for rid in p.rule_ids) for p in out.paths
    ), "expected at least one path edge tagged as the erroring extension"


# ---------------------------------------------------------------------------
# 2. Path count bounded by Π max_fanout.
# ---------------------------------------------------------------------------


def test_manifest_path_builder_path_count_bounded_by_fanout() -> None:
    """A bushy graph: v_root has many backward callers — fanout cap kicks in.

    Build a star: 10 callers each call v_root. Manifest has 1 layer
    with max_fanout=3. The expander should produce at most 3 paths.
    """
    g = HyperGraph()
    callee = g.add_node(Node(kind=PlaceKind.span, self_name="root::callee"))
    assert callee.id is not None
    callers: list[Node] = []
    for i in range(10):
        c = g.add_node(Node(kind=PlaceKind.span, self_name=f"caller-{i}::caller"))
        assert c.id is not None
        callers.append(c)
        g.add_edge(_calls_edge(c.id, callee.id, c.uniq_name, callee.uniq_name))

    timelines = {
        callee.uniq_name: _tl(callee.uniq_name, PlaceKind.span, "slow", 1000),
    }
    for c in callers:
        timelines[c.uniq_name] = _tl(c.uniq_name, PlaceKind.span, "slow", 1010)

    samples: dict = {}
    samples[(callee.id, FeatureKind.span, Feature.error_rate)] = 0.1
    for c in callers:
        assert c.id is not None
        samples[(c.id, FeatureKind.span, Feature.latency_p99_ratio)] = 2.0

    manifest = _make_manifest(n_layers=1, max_fanout=3)
    rctx = ReasoningContext(
        fault_type_name=manifest.fault_type_name,
        manifest=manifest,
        v_root_node_id=callee.id,
        t0=900,
        feature_samples=samples,
    )
    builder = ManifestAwarePathBuilder(
        graph=g,
        timelines=timelines,
        temporal_validator=TemporalValidator(timelines),
        reasoning_ctx=rctx,
    )
    out = builder.build_all(callee.id)

    # 10 candidate edges, fanout cap of 3 → at most 3 paths.
    assert 1 <= len(out.paths) <= 3, f"got {len(out.paths)} paths, expected ≤3"


# ---------------------------------------------------------------------------
# 3. Hand-off integration: trigger fires → fork; cap=2 enforced.
# ---------------------------------------------------------------------------


def test_handoff_forks_at_on_layer_when_trigger_fires() -> None:
    """Set error_rate > threshold on a layer-2 admitted node; expect a fork.

    The fork uses a different fault_type_name in the synthesized rule_id.
    We can't execute the target manifest's layers without registering it;
    use a registry that returns the same manifest under a different name
    so the fork has somewhere to go.
    """
    g, timelines, node_ids = _make_chain(n_spans=5)
    v_root = node_ids[0]
    parent_manifest = _make_manifest(
        fault_type_name="JVMException",
        n_layers=3,
        max_fanout=4,
        handoff_target="HTTPResponseAbort",
        handoff_on_layer=2,
    )
    # Synthetic registry that resolves the hand-off target to a separate
    # manifest. The target manifest also reuses the chain.
    target_manifest = _make_manifest(
        fault_type_name="HTTPResponseAbort",
        n_layers=2,
        max_fanout=4,
    )

    class _FakeRegistry:
        def __init__(self, manifests: dict[str, FaultManifest]) -> None:
            self._m = manifests

        def get(self, name: str) -> FaultManifest | None:
            return self._m.get(name)

    registry = _FakeRegistry(
        {
            parent_manifest.fault_type_name: parent_manifest,
            target_manifest.fault_type_name: target_manifest,
        }
    )

    samples = _samples_for_chain(node_ids)
    # node_ids[2] is admitted at layer 2 (depth-2 caller). Set its
    # error_rate above threshold (0.5) so the hand-off fires there.
    samples[(node_ids[2], FeatureKind.span, Feature.error_rate)] = 0.9

    rctx = ReasoningContext(
        fault_type_name=parent_manifest.fault_type_name,
        manifest=parent_manifest,
        v_root_node_id=v_root,
        t0=900,
        feature_samples=samples,
        registry=registry,
    )
    builder = ManifestAwarePathBuilder(
        graph=g,
        timelines=timelines,
        temporal_validator=TemporalValidator(timelines),
        reasoning_ctx=rctx,
    )
    out = builder.build_all(v_root)

    rule_id_blobs = [rid for path in out.paths for rid in path.rule_ids]
    assert any("HTTPResponseAbort" in rid for rid in rule_id_blobs), (
        "hand-off fork should produce HTTPResponseAbort-tagged rule_ids"
    )


def test_handoff_chain_cap_propagates_into_path_builder() -> None:
    """Verify the path builder enforces the hand-off cap (≤2 hand-offs)."""
    # See test_handoff_chain.py for the unit-level proof. This test asserts
    # the cap logically via construction: 3 hand-offs in a row would need
    # 3 manifests in a chain. We stage that and check rejected_handoffs is
    # populated when the third trigger would fire.
    g, timelines, node_ids = _make_chain(n_spans=6)
    v_root = node_ids[0]

    m_a = _make_manifest(
        fault_type_name="JVMException",
        n_layers=2,
        max_fanout=4,
        handoff_target="HTTPResponseAbort",
        handoff_on_layer=1,
    )
    m_b = _make_manifest(
        fault_type_name="HTTPResponseAbort",
        n_layers=2,
        max_fanout=4,
        handoff_target="MemoryStress",
        handoff_on_layer=1,
    )
    m_c = _make_manifest(
        fault_type_name="MemoryStress",
        n_layers=2,
        max_fanout=4,
        handoff_target="PodKill",
        handoff_on_layer=1,  # third hand-off — should be rejected by cap.
    )
    m_d = _make_manifest(
        fault_type_name="PodKill",
        n_layers=1,
        max_fanout=4,
    )

    class _FakeRegistry:
        def __init__(self, m: dict[str, FaultManifest]) -> None:
            self._m = m

        def get(self, name: str) -> FaultManifest | None:
            return self._m.get(name)

    registry = _FakeRegistry(
        {
            m_a.fault_type_name: m_a,
            m_b.fault_type_name: m_b,
            m_c.fault_type_name: m_c,
            m_d.fault_type_name: m_d,
        }
    )

    samples = _samples_for_chain(node_ids)
    # Hot error_rate at every node so each hand-off trigger fires.
    for nid in node_ids:
        samples[(nid, FeatureKind.span, Feature.error_rate)] = 0.9

    rctx = ReasoningContext(
        fault_type_name=m_a.fault_type_name,
        manifest=m_a,
        v_root_node_id=v_root,
        t0=900,
        feature_samples=samples,
        registry=registry,
    )
    builder = ManifestAwarePathBuilder(
        graph=g,
        timelines=timelines,
        temporal_validator=TemporalValidator(timelines),
        reasoning_ctx=rctx,
    )
    out = builder.build_all(v_root)

    # The third hand-off (to PodKill) must be rejected: chain reached
    # the cap of 2.
    pod_kill_rejects = [nf for nf in out.rejected_handoffs if nf[1] == "PodKill"]
    assert pod_kill_rejects, "expected at least one PodKill rejection when chain hits cap=2"


# ---------------------------------------------------------------------------
# 4. Propagator routing: manifest-driven branch activates when ctx has manifest.
# ---------------------------------------------------------------------------


def test_propagator_routes_to_manifest_driven_branch_when_ctx_present() -> None:
    g, timelines, node_ids = _make_chain(n_spans=3)
    v_root = node_ids[0]
    alarm = node_ids[-1]
    manifest = _make_manifest(n_layers=2, max_fanout=4)
    rctx = ReasoningContext(
        fault_type_name=manifest.fault_type_name,
        manifest=manifest,
        v_root_node_id=v_root,
        t0=900,
        feature_samples=_samples_for_chain(node_ids),
    )
    propagator = FaultPropagator(
        graph=g,
        rules=get_builtin_rules(),
        timelines=timelines,
        max_hops=5,
        injection_window=(900, 5000),
        reasoning_ctx=rctx,
    )
    result = propagator.propagate_from_injection(
        injection_node_ids=[v_root],
        alarm_nodes={alarm},
    )
    # Manifest path produces some paths; rule_ids are the synthetic
    # manifest-tagged form (not generic rule ids).
    assert result.paths, "manifest-driven branch should produce ≥1 path"
    for p in result.paths:
        assert all(rid.startswith("manifest:") for rid in p.rules)


def test_propagator_falls_back_to_generic_when_no_manifest() -> None:
    g, timelines, node_ids = _make_chain(n_spans=2)
    v_root = node_ids[0]
    alarm = node_ids[-1]
    # No reasoning_ctx → generic builder is used.
    propagator = FaultPropagator(
        graph=g,
        rules=get_builtin_rules(),
        timelines=timelines,
        max_hops=3,
    )
    result = propagator.propagate_from_injection(
        injection_node_ids=[v_root],
        alarm_nodes={alarm},
    )
    # Generic rule_ids never start with "manifest:".
    for p in result.paths:
        assert not any(rid.startswith("manifest:") for rid in p.rules)


# ---------------------------------------------------------------------------
# 5. Entry signature short-circuit: failing entry → no paths.
# ---------------------------------------------------------------------------


def test_entry_signature_short_circuits_when_v_root_features_miss() -> None:
    g, timelines, node_ids = _make_chain(n_spans=3)
    v_root = node_ids[0]
    manifest = _make_manifest(n_layers=2, max_fanout=4)
    samples = _samples_for_chain(node_ids)
    # Drop v_root's error_rate well below the entry-signature band [0.05, 1.0].
    samples[(v_root, FeatureKind.span, Feature.error_rate)] = 0.0
    rctx = ReasoningContext(
        fault_type_name=manifest.fault_type_name,
        manifest=manifest,
        v_root_node_id=v_root,
        t0=900,
        feature_samples=samples,
    )
    propagator = FaultPropagator(
        graph=g,
        rules=get_builtin_rules(),
        timelines=timelines,
        max_hops=5,
        injection_window=(900, 5000),
        reasoning_ctx=rctx,
    )
    result = propagator.propagate_from_injection(
        injection_node_ids=[v_root],
        alarm_nodes={node_ids[-1]},
    )
    assert result.paths == []
    assert result.warnings, "entry-signature failure should add a warning"
