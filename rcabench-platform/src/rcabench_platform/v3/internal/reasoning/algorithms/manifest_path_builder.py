"""Manifest-driven in-build path expansion (FORGE rework Phase 5).

Phase 3 §3.4 deferred wiring the manifest into ``PathBuilder`` itself.
The post-filter mode that shipped with Phase 3 leaves the generic
:class:`PathBuilder` to enumerate ~hundreds of candidate paths and
relies on :class:`ManifestEntryGate` / :class:`ManifestLayerGate` to
drop the spurious ones at the end. Phase 4 measured the consequences:

* mean 463 paths/case (vs 13 pre-rework, 35× explosion);
* sham FP padded with spurious paths that happen to pass per-edge gates;
* per-family attributed rate 5–10pp short of target on the borderline
  fault types because manifest-driven admission is best-effort.

This module replaces the post-filter mode with **layer-driven
expansion**: when a manifest is registered for the active fault type,
PathBuilder is bypassed and paths are constructed by walking the
manifest's ``derivation_layers`` directly.

Algorithm (per ``docs/forge_rework/tasks/phase3_gates.md`` §3.4 +
``SCHEMA.md``):

1. Run :class:`ManifestEntryGate` once on ``v_root``. If it fails, no
   paths are produced (the entry signature short-circuits everything;
   any downstream cascade we'd find would be coincidental).
2. Frontier ← ``{v_root}``.
3. For each layer ``k`` in ``manifest.derivation_layers``:

   a. For each frontier node ``u``, enumerate edges ``(u, v)`` whose
      ``(kind, direction)`` pair appears in the layer's parallel
      ``edge_kinds`` × ``edge_directions`` arrays.
   b. Sort candidates deterministically by ``(edge.kind, dst_id)`` and
      cap at ``layer.max_fanout`` per frontier node.
   c. For each candidate ``v``, query
      ``ReasoningContext.feature_samples`` against the layer's
      ``expected_features``. Admit ``v`` iff ≥1 expected feature
      matches its band (OR across the layer's expected features, AND
      with the edge-kind/direction admission).
   d. Build the per-edge piece of the :class:`CandidatePath` (picked
      window via :class:`TemporalValidator`, edge desc, and rule_id /
      confidence stub fields needed for gate compatibility).
   e. Push ``v`` onto next frontier.

4. Hand-offs: at each layer ``k``, after admission, check the parent
   manifest's ``hand_offs`` whose ``on_layer == k``. For each admitted
   node ``v`` that satisfies a hand-off ``trigger`` (single-feature
   threshold check via ``rctx.sample``), recurse with the target
   manifest's ``derivation_layers`` rooted at ``v``. The recursion is
   gated by :class:`HandOffChain` (≤2 hand-offs / cycle detection).

5. Each leaf admission emits a :class:`CandidatePath` from ``v_root``
   through the chain it took. Hand-off forks branch the path tree.

Result: at most ``Π_k max_fanout_k`` paths per derivation, multiplied
by hand-off branches. With the SCHEMA.md cap of 5 layers and typical
``max_fanout`` of 8–32, real cases now produce O(10²) candidates at
worst — already 4-5× lower than the post-filter 463/case before any
edge admission filters out non-matching destinations.

Notes on data shape:

* The ``CandidatePath.rule_ids`` / ``rule_confidences`` fields are
  populated with synthetic ``"manifest:{ft}:L{k}"`` entries and
  ``confidence=1.0``. Existing downstream (rule statistics, label
  classifier) treats these as opaque strings; nothing parses them.
* ``picked_states`` / ``all_states`` / ``picked_state_start_times``
  are filled from the destination's TimelineWindow, exactly as
  :meth:`PathBuilder._build_single_hop` does. This keeps
  :class:`TemporalGate` and :class:`InjectTimeGate` happy when the
  resulting paths are evaluated.
* ``edge_descs`` use the same ``"{kind}_{DIRECTION}"`` schema as
  :class:`PathBuilder` so ``ManifestLayerGate._split_edge_desc``
  parses them identically. We still run the layer gate as a defensive
  check — it should always pass under in-build admission, but the
  redundancy catches any future drift between the builder and the
  gate's interpretation of the manifest.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field

from rcabench_platform.v3.internal.reasoning.algorithms.gates.manifest_entry import (
    _band_match,
)
from rcabench_platform.v3.internal.reasoning.algorithms.handoff import HandOffChain
from rcabench_platform.v3.internal.reasoning.algorithms.path_builder import (
    CandidatePath,
)
from rcabench_platform.v3.internal.reasoning.algorithms.temporal_validator import (
    TemporalValidator,
    _effective_onset,
)
from rcabench_platform.v3.internal.reasoning.ir.timeline import StateTimeline
from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
from rcabench_platform.v3.internal.reasoning.manifests.schema import (
    DerivationLayer,
    FaultManifest,
    HandOff,
)
from rcabench_platform.v3.internal.reasoning.models.graph import (
    DepKind,
    Edge,
    HyperGraph,
    PlaceKind,
)
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationDirection

logger = logging.getLogger(__name__)


# Direction strings as used by manifest YAML are lower-case; PathBuilder
# stamps edge_descs with PropagationDirection's enum value (lower-case).
_DIRECTION_FORWARD = "forward"
_DIRECTION_BACKWARD = "backward"


@dataclass(frozen=True, slots=True)
class _Frame:
    """One node in a partially-built derivation chain.

    ``node_id`` — current node.
    ``parent`` — preceding frame (or ``None`` for the seed v_root).
    ``edge_desc`` — ``"{kind}_{direction}"`` for the parent→self hop.
    ``rule_id`` — synthetic per-layer marker.
    ``manifest_name`` — name of the manifest currently driving expansion.
    ``layer_index`` — 0-based index within ``manifest.derivation_layers``
    that admitted this node (``None`` for v_root).
    ``picked_state`` / ``picked_states_all`` / ``picked_time`` —
    timeline-window picks needed by downstream gates.
    ``handoff_chain`` — :class:`HandOffChain` with this branch's history;
    forking copies it.
    """

    node_id: int
    parent: _Frame | None
    edge_desc: str
    rule_id: str
    manifest_name: str
    layer_index: int | None
    picked_state: str
    picked_states_all: tuple[str, ...]
    picked_time: int
    handoff_chain: HandOffChain


@dataclass
class ManifestPathBuildResult:
    """Output bundle from :meth:`ManifestAwarePathBuilder.build_all`.

    ``paths`` — list of materialised :class:`CandidatePath` instances,
    one per leaf admitted at the deepest layer of any (sub)manifest.
    ``visited_nodes`` — every node that ever sat in any frontier.
    ``max_hops_reached`` — longest path's edge count.
    ``rejected_handoffs`` — aggregated cap-hit / cycle warnings across
    all branches, for audit.
    """

    paths: list[CandidatePath] = field(default_factory=list)
    visited_nodes: set[int] = field(default_factory=set)
    max_hops_reached: int = 0
    rejected_handoffs: list[tuple[int, str]] = field(default_factory=list)


class ManifestAwarePathBuilder:
    """In-build manifest-driven expansion replacing post-filter mode.

    Construction is cheap (no work). All work happens in
    :meth:`build_all`, which returns the candidate-path set ready to be
    fed to the remaining gate stack (TemporalGate + InjectTimeGate;
    ManifestLayerGate is run defensively but should always pass).
    """

    def __init__(
        self,
        graph: HyperGraph,
        timelines: dict[str, StateTimeline],
        temporal_validator: TemporalValidator,
        reasoning_ctx: ReasoningContext,
    ) -> None:
        self.graph = graph
        self.timelines = timelines
        self.temporal_validator = temporal_validator
        self.rctx = reasoning_ctx
        if reasoning_ctx.manifest is None:
            raise ValueError(
                "ManifestAwarePathBuilder requires reasoning_ctx.manifest; "
                "callers should fall back to the generic builder when no "
                "manifest is registered."
            )

    # ---------------------------------------------------------------
    # Public entry point
    # ---------------------------------------------------------------

    def build_all(self, v_root_id: int) -> ManifestPathBuildResult:
        """Drive layer-by-layer expansion from ``v_root_id``.

        Returns a :class:`ManifestPathBuildResult` whose ``paths`` are
        :class:`CandidatePath` objects in the same shape as
        :class:`PathBuilder` would produce; downstream
        :class:`TemporalGate` / :class:`InjectTimeGate` consume them
        identically.

        ``ManifestEntryGate`` is **not** invoked here — it remains in
        the gate stack and runs once per case at the propagator level.
        That keeps the responsibility split clear: this builder owns
        the expansion shape, the gate owns the v_root entry-signature
        verdict, and the propagator wires them together.
        """
        manifest = self.rctx.manifest
        assert manifest is not None  # guarded in __init__

        result = ManifestPathBuildResult()

        # Seed frame: v_root carries no parent edge, no rule, and
        # picked-state derived from its own timeline (best-effort —
        # downstream gates only consult the per-edge entries).
        seed_frame = self._make_seed_frame(v_root_id, manifest.fault_type_name)
        result.visited_nodes.add(v_root_id)

        # Structural descent: when v_root's kind doesn't have direct
        # edges of the manifest's layer-1 ``edge_kinds`` (e.g., a
        # service v_root with a manifest whose first layer expects
        # ``calls`` edges, which only exist between spans), descend
        # one structural hop via ``includes``/``runs``/``routes_to``
        # to attach proxy seeds on the spans/pods/containers v_root
        # owns. Paths emitted from these proxies still anchor at
        # v_root through ``_emit_path_to``.
        proxy_seeds = self._descend_proxy_seeds(seed_frame, manifest)
        for proxy in proxy_seeds:
            result.visited_nodes.add(proxy.node_id)
        seed_frames = [seed_frame, *proxy_seeds]

        # Recurse into the seed manifest. Each derivation may emit
        # multiple leaves; hand-offs spawn extra sub-derivations.
        self._expand_manifest(
            manifest,
            seed_frames,
            result,
        )

        return result

    def _make_seed_frame(self, node_id: int, manifest_name: str) -> _Frame:
        """Construct a seed ``_Frame`` rooted at ``node_id``."""
        seed_state, seed_states_all, seed_time = self._pick_root_state(node_id)
        seed_chain = HandOffChain()
        seed_chain.record_seed(node_id, manifest_name)
        return _Frame(
            node_id=node_id,
            parent=None,
            edge_desc="",
            rule_id="",
            manifest_name=manifest_name,
            layer_index=None,
            picked_state=seed_state,
            picked_states_all=seed_states_all,
            picked_time=seed_time,
            handoff_chain=seed_chain,
        )

    def _descend_proxy_seeds(
        self,
        seed_frame: _Frame,
        manifest: FaultManifest,
    ) -> list[_Frame]:
        """Add proxy seeds for v_root's structurally-related neighbors.

        Triggered when v_root's direct edges don't admit any of
        ``layer1.edge_kinds`` (e.g., a service node with a manifest
        whose layer 1 walks ``calls`` — calls edges live between
        spans, not on services; or a container whose only edge is the
        incoming ``runs`` from its pod). We follow one structural hop
        via ``includes``/``runs``/``routes_to`` in **either direction**
        to neighbors whose own edges match layer-1 kinds, and emit one
        seed frame per admitted neighbor.

        Walking incoming edges is required when v_root is structurally
        a leaf (e.g., container has only incoming ``runs``). Without it
        ContainerKill / JVM* manifests root at a container that has
        zero out-edges, the descent finds nothing, and 0 paths come
        out of the builder.
        """
        if not manifest.derivation_layers:
            return []
        layer1_kinds = set(manifest.derivation_layers[0].edge_kinds)
        if self._has_edge_of_kinds(seed_frame.node_id, layer1_kinds):
            return []  # v_root is already on the right plane.
        structural_kinds = {"includes", "runs", "routes_to"}
        proxies: list[_Frame] = []
        seen: set[int] = set()
        # Outgoing structural edges
        for _, dst_id, key in self.graph._graph.out_edges(  # type: ignore[call-arg]
            seed_frame.node_id, keys=True
        ):
            edge_data = self.graph._graph.get_edge_data(
                seed_frame.node_id, dst_id, key
            )
            if not edge_data:
                continue
            edge_ref: Edge | None = edge_data.get("ref")
            if edge_ref is None or edge_ref.kind.value not in structural_kinds:
                continue
            if dst_id in seen:
                continue
            if not self._has_edge_of_kinds(dst_id, layer1_kinds):
                continue
            seen.add(dst_id)
            proxies.append(self._make_seed_frame(dst_id, manifest.fault_type_name))
        # Incoming structural edges (e.g. container ← pod via runs)
        for src_id, _, key in self.graph._graph.in_edges(  # type: ignore[call-arg]
            seed_frame.node_id, keys=True
        ):
            edge_data = self.graph._graph.get_edge_data(
                src_id, seed_frame.node_id, key
            )
            if not edge_data:
                continue
            edge_ref = edge_data.get("ref")
            if edge_ref is None or edge_ref.kind.value not in structural_kinds:
                continue
            if src_id in seen:
                continue
            if not self._has_edge_of_kinds(src_id, layer1_kinds):
                continue
            seen.add(src_id)
            proxies.append(self._make_seed_frame(src_id, manifest.fault_type_name))
        return proxies

    def _has_edge_of_kinds(self, node_id: int, kinds: set[str]) -> bool:
        """True iff ``node_id`` has any in/out edge whose ``kind`` is in ``kinds``."""
        if not kinds:
            return False
        for _, _, _, d in self.graph._graph.out_edges(  # type: ignore[call-arg]
            node_id, keys=True, data=True
        ):
            ref = d.get("ref")
            if ref is not None and ref.kind.value in kinds:
                return True
        for _, _, _, d in self.graph._graph.in_edges(  # type: ignore[call-arg]
            node_id, keys=True, data=True
        ):
            ref = d.get("ref")
            if ref is not None and ref.kind.value in kinds:
                return True
        return False

    # ---------------------------------------------------------------
    # Per-manifest expansion (recursive across hand-offs)
    # ---------------------------------------------------------------

    def _expand_manifest(
        self,
        manifest: FaultManifest,
        seed_frames: list[_Frame] | _Frame,
        result: ManifestPathBuildResult,
    ) -> None:
        """Walk ``manifest.derivation_layers`` rooted at one or more seed frames.

        Multiple seeds occur when ``_descend_proxy_seeds`` adds owned
        descendants of v_root; the layer expansion treats them as a
        wider initial frontier.
        """
        layers = list(manifest.derivation_layers)
        if not layers:
            return

        # Frontier per layer. Each entry: parent _Frame, used to build the
        # next frame and to gate the path back to v_root.
        if isinstance(seed_frames, _Frame):
            frontier: list[_Frame] = [seed_frames]
        else:
            frontier = list(seed_frames)

        # Track per-handoff potential so we don't re-attempt the same
        # hand-off from the same node within this manifest's pass.
        handoffs_by_layer = self._handoffs_by_layer(manifest)

        for k_idx, layer in enumerate(layers):
            next_frontier: list[_Frame] = []
            for parent_frame in frontier:
                admitted_children = self._admit_layer_children(
                    parent_frame=parent_frame,
                    layer=layer,
                    layer_index=k_idx,
                    manifest_name=manifest.fault_type_name,
                )
                for child in admitted_children:
                    next_frontier.append(child)
                    result.visited_nodes.add(child.node_id)

                    # Emit a path now: each layer admission is a
                    # complete causal claim. If the child also extends
                    # to deeper layers we'll emit a longer path on the
                    # next loop iteration; both are valid (they are
                    # different cascade depths from the same seed).
                    self._emit_path_to(child, result)

            # Hand-offs at this layer: any admitted child whose
            # features satisfy a hand-off trigger forks a sub-derivation.
            for h in handoffs_by_layer.get(layer.layer, []):
                target = self._resolve_handoff_target(h)
                if target is None:
                    continue
                for child in next_frontier:
                    if not self._handoff_trigger_fires(child.node_id, h):
                        continue
                    if not child.handoff_chain.can_take_handoff(child.node_id, h.to):
                        if child.handoff_chain.handoff_count >= 2:
                            result.rejected_handoffs.append((child.node_id, h.to))
                        continue
                    # Fork: copy the chain so sibling branches don't
                    # share record_handoff state. record_handoff
                    # returns False on cycle/cap, in which case we
                    # skip the recursion.
                    forked_chain = HandOffChain(
                        visited=set(child.handoff_chain.visited),
                        fault_types=list(child.handoff_chain.fault_types),
                        handoff_count=child.handoff_chain.handoff_count,
                    )
                    if not forked_chain.record_handoff(child.node_id, h.to):
                        result.rejected_handoffs.append((child.node_id, h.to))
                        continue
                    fork_seed = _Frame(
                        node_id=child.node_id,
                        parent=child.parent,  # share path so far
                        edge_desc=child.edge_desc,
                        rule_id=child.rule_id,
                        manifest_name=h.to,
                        layer_index=child.layer_index,
                        picked_state=child.picked_state,
                        picked_states_all=child.picked_states_all,
                        picked_time=child.picked_time,
                        handoff_chain=forked_chain,
                    )
                    self._expand_manifest(target, fork_seed, result)

            if not next_frontier:
                break
            frontier = next_frontier

    # ---------------------------------------------------------------
    # Admission helpers
    # ---------------------------------------------------------------

    def _admit_layer_children(
        self,
        parent_frame: _Frame,
        layer: DerivationLayer,
        layer_index: int,
        manifest_name: str,
    ) -> list[_Frame]:
        """Enumerate ``parent → v`` candidates allowed by ``layer``.

        Order of operations:

        1. Gather candidate edges by walking the multidigraph. Any
           edge whose (kind, direction) pair is in the layer's
           parallel arrays is a candidate.
        2. Sort by ``(kind.value, dst_id)`` for determinism.
        3. Cap at ``layer.max_fanout``.
        4. For each candidate, attempt to admit via
           ``expected_features`` AND a temporal-validator window
           lookup. Drop if either fails.
        """
        # 1. Gather (edge, direction, dst_id) candidates.
        admissible_pairs = list(zip(layer.edge_kinds, layer.edge_directions, strict=False))
        if not admissible_pairs:
            return []

        cand_edges: list[tuple[Edge, str, int]] = []
        # Forward edges out of parent.
        for _, dst_id, key in self.graph._graph.out_edges(  # type: ignore[call-arg]
            parent_frame.node_id, keys=True
        ):
            edge_data = self.graph._graph.get_edge_data(parent_frame.node_id, dst_id, key)
            if not edge_data:
                continue
            edge_ref: Edge | None = edge_data.get("ref")
            if edge_ref is None:
                continue
            kind = edge_ref.kind.value
            if (kind, _DIRECTION_FORWARD) in admissible_pairs:
                cand_edges.append((edge_ref, _DIRECTION_FORWARD, dst_id))
        # Backward edges into parent.
        for src_id, _, key in self.graph._graph.in_edges(  # type: ignore[call-arg]
            parent_frame.node_id, keys=True
        ):
            edge_data = self.graph._graph.get_edge_data(src_id, parent_frame.node_id, key)
            if not edge_data:
                continue
            edge_ref = edge_data.get("ref")
            if edge_ref is None:
                continue
            kind = edge_ref.kind.value
            if (kind, _DIRECTION_BACKWARD) in admissible_pairs:
                cand_edges.append((edge_ref, _DIRECTION_BACKWARD, src_id))

        # 2. Deterministic ordering.
        cand_edges.sort(key=lambda t: (t[0].kind.value, t[1], t[2]))

        # 3. Fanout cap.
        if len(cand_edges) > layer.max_fanout:
            cand_edges = cand_edges[: layer.max_fanout]

        # 4. Per-candidate admission.
        admitted: list[_Frame] = []
        for edge_ref, direction, dst_id in cand_edges:
            if not self._dst_features_match(dst_id, layer):
                continue
            picked = self._pick_dst_window(
                dst_id=dst_id,
                edge_kind=edge_ref.kind,
                src_state=parent_frame.picked_state,
                src_time=parent_frame.picked_time,
            )
            if picked is None:
                continue
            picked_state, picked_states_all, picked_time = picked

            edge_desc = f"{edge_ref.kind.value}_{direction}"
            rule_id = f"manifest:{manifest_name}:L{layer.layer}"
            admitted.append(
                _Frame(
                    node_id=dst_id,
                    parent=parent_frame,
                    edge_desc=edge_desc,
                    rule_id=rule_id,
                    manifest_name=manifest_name,
                    layer_index=layer_index,
                    picked_state=picked_state,
                    picked_states_all=picked_states_all,
                    picked_time=picked_time,
                    handoff_chain=parent_frame.handoff_chain,
                )
            )
        return admitted

    def _dst_features_match(self, dst_id: int, layer: DerivationLayer) -> bool:
        """Layer-feature OR-check against ``ReasoningContext.feature_samples``.

        SCHEMA.md "expected_features": admit iff ≥1 expected feature
        matches. A missing sample (the IR adapter could not extract
        the feature) is "did not match", same convention as
        :class:`ManifestLayerGate`.
        """
        for fm in layer.expected_features:
            value = self.rctx.aggregate_feature(dst_id, fm.kind, fm.feature)
            if _band_match(value, fm):
                return True
        return False

    def _pick_dst_window(
        self,
        dst_id: int,
        edge_kind: DepKind,
        src_state: str,
        src_time: int,
    ) -> tuple[str, tuple[str, ...], int] | None:
        """Resolve a ``(picked_state, all_states, picked_time)`` triple.

        Strategy mirrors :meth:`PathBuilder._build_single_hop`: ask
        :class:`TemporalValidator.find_admissible_window` for the
        earliest causally-valid window over *all* states the
        destination ever exhibits. Falls back to the first window if
        the destination has a timeline at all but no state intersects.

        Returns ``None`` if the destination has no timeline. That
        forces the path to drop, matching the existing builder's
        behaviour for "destination never observed".
        """
        dst_node = self.graph.get_node_by_id(dst_id)
        if dst_node is None:
            return None
        dst_tl = self.timelines.get(dst_node.uniq_name)
        if dst_tl is None or not dst_tl.windows:
            # Structural mediator nodes (pod/replica_set/deployment)
            # legitimately have no timeline — fall back to an
            # "unknown" placeholder pinned at the source onset so
            # downstream gates have a coherent triple to consume.
            if _is_structural_kind(dst_node.kind):
                return ("unknown", ("unknown",), src_time)
            return None

        all_states = tuple(sorted({w.state for w in dst_tl.windows}))
        # Try the §7.5 admissible window first; if no state matches
        # we fall back to the earliest window so structural mediators
        # / unknown-state destinations still produce a path. The
        # downstream :class:`TemporalGate` re-checks with full
        # epsilon semantics.
        admitted = self.temporal_validator.find_admissible_window(
            dst_node.uniq_name,
            src_onset=src_time,
            edge_kind=edge_kind,
            src_state=src_state,
            dst_states=set(all_states),
        )
        if admitted is not None:
            window, eff = admitted
            return (window.state, all_states, eff)
        # Fallback: use first window, clamp onset.
        first = dst_tl.windows[0]
        return (first.state, all_states, _effective_onset(first, src_time))

    # ---------------------------------------------------------------
    # Hand-off helpers
    # ---------------------------------------------------------------

    @staticmethod
    def _handoffs_by_layer(manifest: FaultManifest) -> dict[int, list[HandOff]]:
        out: dict[int, list[HandOff]] = {}
        for h in manifest.hand_offs:
            out.setdefault(h.on_layer, []).append(h)
        return out

    def _resolve_handoff_target(self, handoff: HandOff) -> FaultManifest | None:
        registry = self.rctx.registry
        if registry is None:
            return None
        # ManifestRegistry.get(fault_type_name) -> FaultManifest | None.
        get = getattr(registry, "get", None)
        if get is None:
            return None
        target = get(handoff.to)
        return target if isinstance(target, FaultManifest) else None

    def _handoff_trigger_fires(self, node_id: int, handoff: HandOff) -> bool:
        """Hand-off trigger uses ``threshold`` (>=), not a band."""
        trig = handoff.trigger
        value = self.rctx.aggregate_feature(node_id, trig.kind, trig.feature)
        if value is None:
            return False
        return value >= trig.threshold

    # ---------------------------------------------------------------
    # CandidatePath emission (frame chain → CandidatePath)
    # ---------------------------------------------------------------

    def _emit_path_to(self, leaf: _Frame, result: ManifestPathBuildResult) -> None:
        """Walk ``leaf`` back to its root frame and build a CandidatePath."""
        # Reconstruct ancestor chain.
        chain: list[_Frame] = []
        cur: _Frame | None = leaf
        while cur is not None:
            chain.append(cur)
            cur = cur.parent
        chain.reverse()
        if len(chain) < 2:
            # Single-node "path" — caller asked us to emit at v_root.
            # Skip; PropagationPath requires ≥1 edge.
            return

        node_ids: list[int] = []
        all_states: list[list[str]] = []
        picked_states: list[str] = []
        picked_times: list[int] = []
        edge_descs: list[str] = []
        rule_ids: list[str] = []
        rule_confs: list[float] = []
        delays: list[float] = []

        for idx, frame in enumerate(chain):
            node_ids.append(frame.node_id)
            all_states.append(list(frame.picked_states_all) or ["unknown"])
            picked_states.append(frame.picked_state or "unknown")
            picked_times.append(frame.picked_time)
            if idx == 0:
                continue
            edge_descs.append(frame.edge_desc)
            rule_ids.append(frame.rule_id)
            rule_confs.append(1.0)
            prev_time = chain[idx - 1].picked_time
            delays.append(float(frame.picked_time - prev_time))

        path = CandidatePath(
            node_ids=node_ids,
            all_states=all_states,
            picked_states=picked_states,
            picked_state_start_times=picked_times,
            edge_descs=edge_descs,
            rule_ids=rule_ids,
            rule_confidences=rule_confs,
            propagation_delays=delays,
        )
        result.paths.append(path)
        result.max_hops_reached = max(result.max_hops_reached, len(node_ids) - 1)

    # ---------------------------------------------------------------
    # Internal: best-effort root state pick
    # ---------------------------------------------------------------

    def _pick_root_state(self, node_id: int) -> tuple[str, tuple[str, ...], int]:
        """Pick a (state, all_states, time) triple for v_root.

        Used only to seed the per-edge temporal-validator calls. The
        :class:`ManifestEntryGate` still runs at the propagator level
        for the actual v_root entry-signature verdict.
        """
        node = self.graph.get_node_by_id(node_id)
        t0 = self.rctx.t0 if self.rctx.t0 is not None else 0
        if node is None:
            return ("unknown", ("unknown",), t0)
        tl = self.timelines.get(node.uniq_name)
        if tl is None or not tl.windows:
            return ("unknown", ("unknown",), t0)
        all_states = tuple(sorted({w.state for w in tl.windows}))
        # Prefer the earliest non-healthy window as a best-effort onset.
        for w in tl.windows:
            if w.state not in {"healthy", "unknown"}:
                return (w.state, all_states, w.start)
        first = tl.windows[0]
        return (first.state, all_states, first.start)


def _is_structural_kind(kind: PlaceKind) -> bool:
    return kind in {PlaceKind.pod, PlaceKind.replica_set, PlaceKind.deployment}


__all__ = [
    "ManifestAwarePathBuilder",
    "ManifestPathBuildResult",
    "PropagationDirection",
]
