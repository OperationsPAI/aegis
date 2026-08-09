"""System-specific fingerprints kept out of algorithm code.

The reasoning pipeline is system-agnostic: ``algorithms/``,
``manifests/``, and ``gates/`` never reference a specific demo
system's naming convention. The only place such knowledge belongs is
the injection-resolver, where it is needed to disambiguate the trace
graph's representation of the chaos-mesh injection target. This
module is the single registry for that knowledge.

Two kinds of system-specific data live here:

1. **Service-name prefixes**. Some demos use a project prefix on
   every internal service (``ts-`` for TrainTicket, ``hotel-reserv-``
   for hotel-reservation). When the chaos-mesh ``injection_point.app``
   is ``preserve-service`` and the trace graph carries
   ``ts-preserve-service``, the resolver must accept both as the
   same target. Encoding this as a registry of prefixes keeps the
   resolver code system-agnostic — adding a new system means adding
   one entry here, not editing the resolver.

2. **Sidecar suffixes**. Pods commonly host a sidecar container
   alongside the app (``-mmc`` memcached, ``-envoy`` proxy,
   ``-istio-proxy``, ...). When the engine config provides a
   coarse-grained ``app: profile`` and the graph contains both
   ``hotel-reserv-profile`` and ``hotel-reserv-profile-mmc``, the
   resolver must prefer the shortest match (the app, not the
   sidecar). The suffix list is informational here — the actual
   tie-break logic lives in :func:`shortest_container_match` —
   but we keep the canonical list here so adding a new sidecar
   just means appending.

3. **External service names**. Datastore-style services (mysql,
   redis, ...) are not under chaos and never appear as v_root in
   trace graphs even when they are listed in ground_truth. The
   resolver filters them out when the user-perceptible cause must
   be an internal service.

This module is a pure constant table — no logic, no ``startswith``
calls in the resolver. The resolver iterates the registry and uses
the entries; there is no hard-coded reference to ``"ts-"`` /
``"hotel-"`` anywhere outside this file.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Final


@dataclass(frozen=True, slots=True)
class SystemFingerprint:
    """One known demo system's naming convention.

    Attributes
    ----------
    name:
        Free-form identifier ("trainticket", "hotel-reserv", "otel-demo").
    service_prefixes:
        Strings that may be prepended to a chaos-mesh ``app`` name
        when the trace graph carries the project-prefixed form. Used
        by the resolver to accept both ``preserve-service`` and
        ``ts-preserve-service`` as the same service.
    sidecar_suffixes:
        Suffixes that mark a sidecar container alongside the app
        (``-mmc``, ``-envoy``, ...). Informational; the resolver
        sorts containers by name length so the app (shorter) wins
        over its sidecars (longer).
    """

    name: str
    service_prefixes: frozenset[str] = field(default_factory=frozenset)
    sidecar_suffixes: frozenset[str] = field(default_factory=frozenset)


# Canonical registry. Keep one entry per known system. Adding a new
# system: append a SystemFingerprint here; do not touch resolver code.
SYSTEM_FINGERPRINTS: Final[tuple[SystemFingerprint, ...]] = (
    SystemFingerprint(
        name="trainticket",
        service_prefixes=frozenset({"ts-"}),
        sidecar_suffixes=frozenset(),
    ),
    SystemFingerprint(
        name="hotel-reserv",
        service_prefixes=frozenset({"hotel-reserv-"}),
        sidecar_suffixes=frozenset({"-mmc", "-envoy", "-istio-proxy", "-proxy"}),
    ),
    SystemFingerprint(
        name="otel-demo",
        service_prefixes=frozenset(),
        sidecar_suffixes=frozenset({"-istio-proxy", "-envoy"}),
    ),
)


# Pre-computed flat sets for the resolver's substring checks. These
# are derived once at import time from the fingerprints above; the
# resolver uses them as opaque constants and never references a
# specific system by name.
ALL_SERVICE_PREFIXES: Final[frozenset[str]] = frozenset(p for f in SYSTEM_FINGERPRINTS for p in f.service_prefixes)
ALL_SIDECAR_SUFFIXES: Final[frozenset[str]] = frozenset(s for f in SYSTEM_FINGERPRINTS for s in f.sidecar_suffixes)


# External / infrastructure services. These are not under chaos and
# typically don't appear in the abnormal trace graph as a v_root,
# but they DO appear in chaos-mesh's ``ground_truth`` list (e.g., a
# JVMMySQLException naming both the user service and the mysql
# server). The resolver filters them out when picking the
# user-perceptible cause. Single source of truth.
EXTERNAL_SERVICE_NAMES: Final[frozenset[str]] = frozenset(
    {
        "mysql",
        "redis",
        "postgres",
        "mongodb",
        "kafka",
        "rabbitmq",
        "memcached",
    }
)


def service_name_matches(graph_service_name: str, target_service: str) -> bool:
    """True iff ``graph_service_name`` represents ``target_service``.

    Match conditions (in order):

    1. Exact equality.
    2. ``graph_service_name`` equals one of the registered service
       prefixes appended to ``target_service``
       (``"ts-" + "preserve-service" == graph_service_name``).
    3. Substring match in either direction — the looser fallback for
       systems whose prefix is not known to the registry.

    Used by ``InjectionNodeResolver._span_belongs_to_service`` to
    decide whether a span belongs to the chaos target's service when
    the chaos config and the trace graph use different naming
    conventions.
    """
    if graph_service_name == target_service:
        return True
    for prefix in ALL_SERVICE_PREFIXES:
        if graph_service_name == prefix + target_service:
            return True
    if target_service in graph_service_name or graph_service_name in target_service:
        return True
    return False


__all__ = [
    "ALL_SERVICE_PREFIXES",
    "ALL_SIDECAR_SUFFIXES",
    "EXTERNAL_SERVICE_NAMES",
    "SYSTEM_FINGERPRINTS",
    "SystemFingerprint",
    "service_name_matches",
]
