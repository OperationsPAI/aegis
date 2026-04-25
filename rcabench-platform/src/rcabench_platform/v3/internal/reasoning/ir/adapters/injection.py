"""InjectionAdapter — seed emitter.

Structural-evidence adapter that turns a ``ResolvedInjection`` into one or
more seed ``Transition`` events. This guarantees the IR has at least one
non-UNKNOWN node even on datapacks where signal adapters produce nothing
(the "N2 / N3 / N4" cases from #163).

Phase 1 scope: map ``fault_category`` × ``fault_type`` to a ``to_state``
using the table below. Per-kind specificity is taken from
``ResolvedInjection.start_kind`` so the adapter respects resolver choices
(e.g. ContainerKill may resolve to a container node even though the fault
family is ``pod_lifecycle``).

Mapping:

| fault_category | fault_type_name                              | to_state            |
| -------------- | -------------------------------------------- | ------------------- |
| http_response  | HTTPResponseAbort / ReplaceBody / PatchBody /ReplaceCode | span.erroring |
| http_response  | HTTPResponseDelay                            | span.slow           |
| http_request   | HTTPRequestAbort / ReplacePath / ReplaceMethod | span.erroring     |
| http_request   | HTTPRequestDelay                             | span.slow           |
| container      | MemoryStress / CPUStress                     | <kind>.degraded    |
| pod            | PodKill / PodFailure                         | <kind>.unavailable |
| pod            | ContainerKill                                | <kind>.unavailable |
| jvm            | JVMLatency / JVMGarbageCollector             | <kind>.slow         |
| jvm            | JVMException / JVMReturn / JVMCPU / JVMMemory| <kind>.erroring     |
| jvm_database   | JVMMySQLLatency                              | <kind>.slow         |
| jvm_database   | JVMMySQLException                            | <kind>.erroring     |
| network        | NetworkPartition                             | service.unavailable |
| network        | NetworkDelay / Loss / Duplicate / Corrupt / Bandwidth | service.degraded |
| dns            | DNSError / DNSRandom                         | service.erroring    |
| time           | TimeSkew                                     | pod.degraded        |
| service (fallback) | — (unknown)                              | warn, emit nothing  |

All seed transitions use ``trigger=f"fault:{fault_type_name}"`` and
``level=EvidenceLevel.structural``. ``from_state`` is always ``"unknown"``.
"""

from __future__ import annotations

import logging
from collections.abc import Iterable
from enum import Enum

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.evidence import Evidence, EvidenceLevel
from rcabench_platform.v3.internal.reasoning.ir.states import (
    ContainerStateIR,
    PodStateIR,
    ServiceStateIR,
    SpanStateIR,
)
from rcabench_platform.v3.internal.reasoning.ir.transition import Transition
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import ResolvedInjection

logger = logging.getLogger(__name__)


_HTTP_RESPONSE_ERRORING = {
    "HTTPResponseAbort",
    "HTTPResponseReplaceBody",
    "HTTPResponsePatchBody",
    "HTTPResponseReplaceCode",
}
_HTTP_REQUEST_ERRORING = {
    "HTTPRequestAbort",
    "HTTPRequestReplacePath",
    "HTTPRequestReplaceMethod",
}
_JVM_SLOW = {"JVMLatency", "JVMGarbageCollector"}
_JVM_ERRORING = {"JVMException", "JVMReturn", "JVMCPUStress", "JVMMemoryStress"}
_NETWORK_UNAVAILABLE = {"NetworkPartition"}


_START_KIND_TO_PLACE_KIND = {
    "span": PlaceKind.span,
    "service": PlaceKind.service,
    "pod": PlaceKind.pod,
    "container": PlaceKind.container,
}


def _pick_state(kind: PlaceKind, severity_tier: str) -> str:
    """Pick the enum value on the kind-appropriate IR enum.

    ``severity_tier`` is one of ``erroring`` / ``slow`` / ``degraded`` /
    ``unavailable``. Falls back to the pivot name if the kind doesn't
    define a specialisation (e.g. SpanStateIR has no ``degraded``).
    """
    enum_by_kind: dict[PlaceKind, type[Enum]] = {
        PlaceKind.span: SpanStateIR,
        PlaceKind.service: ServiceStateIR,
        PlaceKind.pod: PodStateIR,
        PlaceKind.container: ContainerStateIR,
    }
    enum_cls = enum_by_kind.get(kind)
    if enum_cls is None:
        return severity_tier
    members = enum_cls.__members__
    key = severity_tier.upper()
    if key in members:
        return str(members[key].value)
    if severity_tier == "degraded" and "SLOW" in members:
        return str(members["SLOW"].value)
    return severity_tier


def _decide_to_state(fault_category: str, fault_type_name: str) -> str | None:
    if fault_category == "http_response":
        if fault_type_name in _HTTP_RESPONSE_ERRORING:
            return "erroring"
        if fault_type_name == "HTTPResponseDelay":
            return "slow"
        return None
    if fault_category == "http_request":
        if fault_type_name in _HTTP_REQUEST_ERRORING:
            return "erroring"
        if fault_type_name == "HTTPRequestDelay":
            return "slow"
        return None
    if fault_category == "container":
        return "degraded"
    if fault_category == "pod":
        return "unavailable"
    if fault_category == "jvm":
        if fault_type_name in _JVM_SLOW:
            return "slow"
        if fault_type_name in _JVM_ERRORING:
            return "erroring"
        return None
    if fault_category == "jvm_database":
        if fault_type_name == "JVMMySQLLatency":
            return "slow"
        if fault_type_name == "JVMMySQLException":
            return "erroring"
        return None
    if fault_category == "network":
        if fault_type_name in _NETWORK_UNAVAILABLE:
            return "unavailable"
        return "degraded"
    if fault_category == "dns":
        return "erroring"
    if fault_category == "time":
        return "degraded"
    return None


class InjectionAdapter:
    """Seed-emitting structural adapter. Always runs."""

    name = "injection"

    def __init__(self, resolved: ResolvedInjection, injection_at: int) -> None:
        self._resolved = resolved
        self._at = injection_at

    def emit(self, ctx: AdapterContext) -> Iterable[Transition]:
        r = self._resolved
        kind = _START_KIND_TO_PLACE_KIND.get(r.start_kind)
        if kind is None:
            logger.warning(
                "InjectionAdapter: unknown start_kind=%r for fault %s; emitting no seed",
                r.start_kind,
                r.fault_type_name,
            )
            return

        severity_tier = _decide_to_state(r.fault_category, r.fault_type_name)
        if severity_tier is None:
            logger.warning(
                "InjectionAdapter: no seed mapping for fault_category=%s fault_type=%s; emitting no seed",
                r.fault_category,
                r.fault_type_name,
            )
            return

        to_state = _pick_state(kind, severity_tier)
        evidence: Evidence = {
            "specialization_labels": frozenset({r.fault_type_name}),
        }

        for node_key in r.injection_nodes:
            yield Transition(
                node_key=node_key,
                kind=kind,
                at=self._at,
                from_state="unknown",
                to_state=to_state,
                trigger=f"fault:{r.fault_type_name}",
                level=EvidenceLevel.structural,
                evidence=evidence,
            )
