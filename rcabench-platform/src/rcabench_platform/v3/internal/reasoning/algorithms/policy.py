"""Temporal-admission policy constants for fault-propagation chains.

Implements the §12.4 budget table from
``docs/reasoning-feature-taxonomy.md`` so the ``TemporalValidator`` can
admit edges with measurement-noise tolerance instead of an exact
``min_start_time`` lower bound.

Per §7.5, the per-edge admission predicate on edge ``(A.s1, B.s2,
edge_kind)`` is::

    onset(A.s1) <= onset(B.s2) + epsilon_eff(s1, s2, edge_kind)

where ``epsilon_eff`` decomposes into a propagation-delay budget for the
channel plus measurement-noise compensation on each onset::

    epsilon_eff = epsilon(edge_kind)
                + onset_resolution(s1)
                + onset_resolution(s2)

The constants below are chosen *deliberately generous* (§7.5): noise paths
joint-survival probability decays as q^N along chain length, so a generous
per-edge tolerance favours recall on real chains without admitting much
composite noise.

The ``DELTA_PRE_SECONDS`` / ``DELTA_POST_SECONDS`` constants delimit the
global window ``[t_inject - delta_pre, t_alarm + delta_post]`` that prunes
pre-existing onsets. They are exported here for consumers but not yet
enforced — global-window pruning is deferred to a later PR because it
needs the full alarm_set onset to compute ``t_alarm``.
"""

from __future__ import annotations

from rcabench_platform.v3.internal.reasoning.models.graph import DepKind

_EDGE_EPSILON_SECONDS: dict[DepKind, int] = {
    DepKind.calls: 5,
    DepKind.includes: 5,
    DepKind.runs: 60,
    DepKind.schedules: 60,
    DepKind.routes_to: 10,
}
"""Per-edge propagation-delay budget epsilon(edge_kind) per §12.4.

Synchronous channels (calls, includes) get 5s. Coarse lifecycle channels
(runs = pod->container, schedules = node->pod) get 60s because container
restart / pod scheduling is observed at minute granularity. Service routing
(routes_to) gets 10s for endpoint-update propagation.
"""

_ONSET_RESOLUTION_SECONDS: dict[str, int] = {
    "erroring": 3,
    "slow": 3,
    "silent": 30,
    "degraded": 5,
    "unavailable": 1,
    "restarting": 1,
    # ``missing`` inherits its onset_resolution from the source state of
    # the rule — handled at the call site (see ``onset_resolution_seconds``).
    # ``healthy`` / ``unknown`` are not anchor states for chains, but may
    # appear at intermediate hops; default them to a generous 5s.
    "healthy": 5,
    "unknown": 5,
}
"""Per-state measurement-noise budget per §12.4.

Onset uncertainty depends on the signal channel: span-level error/latency
detectors fire within seconds (3s); silence is detected only after the
subwindow boundary (30s); pod-level lifecycle events are observed at the
event tick (1s).
"""

_DEFAULT_EDGE_EPSILON_SECONDS: int = 5
_DEFAULT_ONSET_RESOLUTION_SECONDS: int = 5

DELTA_PRE_SECONDS: int = 30
"""Global-window pre-injection slack [t_inject - delta_pre, ...] per §7.5.

Exported for future use; PR#6 does NOT enforce global-window pruning.
The pruning step requires ``t_alarm`` (computed from alarm_set onsets)
which couples to pipeline shape and is therefore scoped out of this PR.
TODO(PR>6): wire this constant into a global pre-pass that drops onsets
outside ``[t_inject - DELTA_PRE_SECONDS, t_alarm + DELTA_POST_SECONDS]``.
"""

DELTA_POST_SECONDS: int = 30
"""Global-window post-alarm slack [..., t_alarm + delta_post] per §7.5.

Equal to ``subwindow_seconds`` (§12.4). See ``DELTA_PRE_SECONDS`` for
the deferred-implementation note.
"""


# -----------------------------------------------------------------------------
# Inject-time admission window — per-path InjectTimeGate constants.
#
# A picked onset must lie within ``[t0, t0 + Δt + INJECT_TIME_TOLERANCE_SECONDS]``
# (with the injection node itself getting an additional pre-grace), where t0
# is the chaos-mesh start time and Δt is the declared injection duration.
#
# Derivation (§12.4 +δ):
#   τ = Δ_clock + Δ_event + max(onset_resolution)
#     ≈ 30s (NTP-bounded clock skew between chaos control plane and trace
#            collectors)
#       + 30s (k8s informer + scrape pipeline propagation lag for pod /
#              container / endpoint events at the chaos start instant)
#       + 30s (slowest onset_resolution across canonical states; ``silent``
#              fires only after a 30s subwindow boundary).
#   Sum bound ≈ 90s but the slowest channel (silent) only matters if a
#   declared latency-or-erroring fault projects into a silent-rooted path —
#   that is rare. Picking 60s lands on the inflection of the empirical
#   "did the cascade actually show up by then" curve while staying inside
#   the conservative ½ × (Δ_clock + Δ_event + onset_resolution) envelope
#   so multi-hop chains do not stack tolerance unboundedly.
#
# Sensitivity: see ``bin/paper_artifacts/inject_time_sensitivity.py`` for the
# τ ∈ {30, 60, 90, 120} sweep across the 500-case dataset; recall plateaus
# at τ ≥ 60s and the marginal label-flip count at τ = 30s vs 60s is the
# dominant calibration evidence reported in the paper appendix.
# -----------------------------------------------------------------------------

INJECT_TIME_TOLERANCE_SECONDS: int = 60
"""Upper-bound slack on a derived path's terminal onset relative to t0 + Δt.

Picked deliberately on the recall plateau identified by the τ ∈ {30, 60,
90, 120} sweep (see derivation above). Increasing further admits more late
onsets at the cost of increasing the joint observation window probability
of an unrelated baseline drift coinciding with the chaos start.
"""

INJECT_NODE_PRE_GRACE_SECONDS: int = 5
"""Pre-injection slack applied **only to the injection node**.

Absorbs NTP-bounded clock skew between the chaos control plane and trace
timestamps for the very first onset on the injection node — the node where
chaos-mesh observed the fault and our pipeline observed the cascade may
disagree by a few seconds even on synchronised infrastructure. Downstream
nodes do not need this grace because their onsets are gated by the source
onset via the temporal admission rule.
"""


def edge_epsilon_seconds(edge_kind: DepKind) -> int:
    """Return epsilon(edge_kind) — per-channel propagation-delay budget."""
    return _EDGE_EPSILON_SECONDS.get(edge_kind, _DEFAULT_EDGE_EPSILON_SECONDS)


def onset_resolution_seconds(
    state: str,
    src_state_for_missing: str | None = None,
) -> int:
    """Return onset_resolution(state) per §12.4.

    ``missing`` inherits its onset_resolution from the source state because
    a node that disappeared has no observed onset of its own — its onset is
    derived from whatever drove the source out of HEALTHY.
    """
    if state == "missing" and src_state_for_missing is not None:
        return onset_resolution_seconds(src_state_for_missing)
    return _ONSET_RESOLUTION_SECONDS.get(state, _DEFAULT_ONSET_RESOLUTION_SECONDS)


def epsilon_eff_seconds(
    src_state: str,
    dst_state: str,
    edge_kind: DepKind,
) -> int:
    """epsilon_eff = epsilon(edge_kind) + onset_resolution(src) + onset_resolution(dst).

    Per §7.5, this is the per-edge tolerance budget. When the destination
    observation lands at most ``epsilon_eff`` seconds before the source
    observation, the chain is still admitted as causal (measurement-noise
    compensation).
    """
    return (
        edge_epsilon_seconds(edge_kind)
        + onset_resolution_seconds(src_state)
        + onset_resolution_seconds(dst_state, src_state_for_missing=src_state)
    )
