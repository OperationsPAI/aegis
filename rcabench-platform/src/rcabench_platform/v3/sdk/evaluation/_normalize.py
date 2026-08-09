"""Shared name-normalization helpers used by set_match and graph_match."""

from __future__ import annotations


def norm(name: str | None) -> str:
    if not name:
        return ""
    return name.strip().lower().replace("-", "").replace("_", "")


def service_eq(a: str | None, b: str | None) -> bool:
    return norm(a) == norm(b) and bool(norm(a))


# Synthetic traffic sources excluded from graph metrics: never GT root causes
# (we don't inject into them), filtered out of GT alarm_nodes upstream, and
# the right "user-visible" boundary is the topmost non-loadgen span. Whether
# the agent mentions loadgen or not should not move node_f1 / edge_f1 /
# path_reachability.
LOADGEN_NORM: frozenset[str] = frozenset(
    norm(s)
    for s in (
        "loadgenerator",
        "load-generator",
        "locust",
        "wrk2",
        "dsb-wrk2",
        "k6",
    )
)
