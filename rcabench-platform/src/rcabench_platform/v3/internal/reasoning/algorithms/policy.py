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
