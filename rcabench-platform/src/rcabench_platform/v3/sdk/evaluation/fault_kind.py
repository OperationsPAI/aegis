"""Observable-effect-based fault-kind vocabulary + chaos_type mapping.

Names describe what an SRE / agent sees in telemetry, not the chaos-mesh
implementation. Two implementations that produce the same observable signal
share a fault_kind; two implementations whose signals are distinguishable in
the parquets get separate kinds.

Coverage: every canonical chaos_type from
``rcabench_platform.v3.internal.reasoning.models.injection.FAULT_TYPES``
(0..30) plus chaos-mesh native names used in the new-format injection.json
(``JVMRuntimeMutator``, ``DNSChaos``, ``TimeChaos``).
"""

from __future__ import annotations

from enum import Enum


class FaultKind(str, Enum):
    """Observable-symptom labels for root causes.

    Naming follows SRE / postmortem vocabulary, not chaos-engineering
    mechanism names. Legacy values from earlier SDK versions are still
    accepted on parse via ``_LEGACY_ALIASES`` (see ``_missing_``) so
    historical ``eval_metrics`` rows keep deserializing.
    """

    # ── Pod / container lifecycle ────────────────────────────────────────
    POD_FAILURE = "pod_failure"  # PodKill, ContainerKill — service blips and recovers
    POD_UNAVAILABLE = "pod_unavailable"  # PodFailure — service stays gone for the window

    # ── L3/L4 network (direction is part of the GT) ─────────────────────
    NETWORK_DELAY = "network_delay"
    NETWORK_LOSS = "network_loss"
    NETWORK_PARTITION = "network_partition"
    NETWORK_CORRUPT = "network_corrupt"
    NETWORK_DUPLICATE = "network_duplicate"
    NETWORK_BANDWIDTH_THROTTLED = "network_bandwidth_throttled"

    # ── HTTP layer (no direction — span carries service_name itself) ────
    HTTP_ABORTED = "http_aborted"  # HTTPRequestAbort, HTTPResponseAbort
    HTTP_SLOW = "http_slow"  # HTTPRequestDelay, HTTPResponseDelay
    HTTP_RESPONSE_BODY_CORRUPTED = "http_response_body_corrupted"  # ReplacePath/Method/Body, PatchBody
    HTTP_WRONG_STATUS_CODE = "http_wrong_status_code"  # ReplaceCode

    # ── Resource exhaustion ─────────────────────────────────────────────
    CONTAINER_CPU_SATURATED = "container_cpu_saturated"  # cgroup CPU saturated
    PROCESS_CPU_SATURATED = "process_cpu_saturated"  # in-process (e.g. JVM) CPU pinned
    CONTAINER_MEMORY_SATURATED = "container_memory_saturated"  # cgroup memory saturated
    JVM_HEAP_EXHAUSTED = "jvm_heap_exhausted"  # JVM heap occupancy near limit
    JVM_GC_THRASHING = "jvm_gc_thrashing"  # GC pauses long enough to stall app threads

    # ── Code-level (application / DB driver) ────────────────────────────
    APPLICATION_EXCEPTION = "application_exception"  # JVMException
    DATABASE_CALL_FAILED = "database_call_failed"  # JVMMySQLException
    APPLICATION_METHOD_SLOW = "application_method_slow"  # JVMLatency
    DATABASE_CALL_SLOW = "database_call_slow"  # JVMMySQLLatency
    WRONG_RETURN_VALUE = "wrong_return_value"  # JVMReturn, JVMRuntimeMutator

    # ── DNS / time ──────────────────────────────────────────────────────
    DNS_LOOKUP_FAILED = "dns_lookup_failed"  # DNSError, DNSChaos (alias)
    DNS_RETURNED_WRONG_ADDRESS = "dns_returned_wrong_address"  # DNSRandom
    CLOCK_SKEW = "clock_skew"  # TimeSkew, TimeChaos (alias)

    UNKNOWN = "unknown"

    @classmethod
    def _missing_(cls, value: object) -> "FaultKind | None":
        """Accept legacy string values from earlier SDK versions."""
        if isinstance(value, str):
            new = _LEGACY_ALIASES.get(value)
            if new is not None:
                return cls(new)
        return None


# Legacy fault_kind strings → current canonical value. Keeps historical
# ``eval_metrics`` JSON and any in-flight agent outputs parseable after the
# SRE-vocabulary rename. Resolved by ``FaultKind._missing_``.
_LEGACY_ALIASES: dict[str, str] = {
    "network_bandwidth_limit": "network_bandwidth_throttled",
    "http_payload_modified": "http_response_body_corrupted",
    "http_response_status_modified": "http_wrong_status_code",
    "cpu_stress": "container_cpu_saturated",
    "jvm_thread_cpu_stress": "process_cpu_saturated",
    "mem_stress": "container_memory_saturated",
    "jvm_heap_stress": "jvm_heap_exhausted",
    "jvm_gc_pressure": "jvm_gc_thrashing",
    "jvm_method_exception": "application_exception",
    "jvm_jdbc_exception": "database_call_failed",
    "jvm_method_latency": "application_method_slow",
    "jvm_jdbc_latency": "database_call_slow",
    "jvm_method_mutated": "wrong_return_value",
    "dns_resolution_failed": "dns_lookup_failed",
    "dns_resolution_wrong": "dns_returned_wrong_address",
}


# chaos_type → FaultKind
# Covers the 31 canonical FAULT_TYPES from rcabench's injection model plus
# chaos-mesh native names that show up in new-format injection.json.
_CHAOS_TYPE_MAP: dict[str, FaultKind] = {
    # Pod lifecycle
    "PodKill": FaultKind.POD_FAILURE,
    "PodFailure": FaultKind.POD_UNAVAILABLE,
    "ContainerKill": FaultKind.POD_FAILURE,
    # Resource (pod-level)
    "MemoryStress": FaultKind.CONTAINER_MEMORY_SATURATED,
    "CPUStress": FaultKind.CONTAINER_CPU_SATURATED,
    # HTTP
    "HTTPRequestAbort": FaultKind.HTTP_ABORTED,
    "HTTPResponseAbort": FaultKind.HTTP_ABORTED,
    "HTTPRequestDelay": FaultKind.HTTP_SLOW,
    "HTTPResponseDelay": FaultKind.HTTP_SLOW,
    "HTTPResponseReplaceBody": FaultKind.HTTP_RESPONSE_BODY_CORRUPTED,
    "HTTPResponsePatchBody": FaultKind.HTTP_RESPONSE_BODY_CORRUPTED,
    "HTTPRequestReplacePath": FaultKind.HTTP_RESPONSE_BODY_CORRUPTED,
    "HTTPRequestReplaceMethod": FaultKind.HTTP_RESPONSE_BODY_CORRUPTED,
    "HTTPResponseReplaceCode": FaultKind.HTTP_WRONG_STATUS_CODE,
    # DNS
    "DNSError": FaultKind.DNS_LOOKUP_FAILED,
    "DNSChaos": FaultKind.DNS_LOOKUP_FAILED,  # chaos-mesh native alias
    "DNSRandom": FaultKind.DNS_RETURNED_WRONG_ADDRESS,
    # Time
    "TimeSkew": FaultKind.CLOCK_SKEW,
    "TimeChaos": FaultKind.CLOCK_SKEW,  # chaos-mesh native alias
    # Network
    "NetworkDelay": FaultKind.NETWORK_DELAY,
    "NetworkLoss": FaultKind.NETWORK_LOSS,
    "NetworkDuplicate": FaultKind.NETWORK_DUPLICATE,
    "NetworkCorrupt": FaultKind.NETWORK_CORRUPT,
    "NetworkBandwidth": FaultKind.NETWORK_BANDWIDTH_THROTTLED,
    "NetworkPartition": FaultKind.NETWORK_PARTITION,
    # JVM
    "JVMLatency": FaultKind.APPLICATION_METHOD_SLOW,
    "JVMReturn": FaultKind.WRONG_RETURN_VALUE,
    "JVMRuntimeMutator": FaultKind.WRONG_RETURN_VALUE,  # chaos-mesh native alias
    "JVMException": FaultKind.APPLICATION_EXCEPTION,
    "JVMGarbageCollector": FaultKind.JVM_GC_THRASHING,
    "JVMCPUStress": FaultKind.PROCESS_CPU_SATURATED,
    "JVMMemoryStress": FaultKind.JVM_HEAP_EXHAUSTED,
    "JVMMySQLLatency": FaultKind.DATABASE_CALL_SLOW,
    "JVMMySQLException": FaultKind.DATABASE_CALL_FAILED,
}


# Canonical FAULT_TYPES list, indexed 0..30 — used to decode old-format
# injection.json's numeric `fault_type` field. Mirrors the list defined in
# ``rcabench_platform.v3.internal.reasoning.models.injection.FAULT_TYPES``.
# Duplicated here to avoid circular imports (the reasoning module pulls in
# heavy graph deps; the eval module needs to stay light).
CANONICAL_FAULT_TYPES: tuple[str, ...] = (
    "PodKill",
    "PodFailure",
    "ContainerKill",
    "MemoryStress",
    "CPUStress",
    "HTTPRequestAbort",
    "HTTPResponseAbort",
    "HTTPRequestDelay",
    "HTTPResponseDelay",
    "HTTPResponseReplaceBody",
    "HTTPResponsePatchBody",
    "HTTPRequestReplacePath",
    "HTTPRequestReplaceMethod",
    "HTTPResponseReplaceCode",
    "DNSError",
    "DNSRandom",
    "TimeSkew",
    "NetworkDelay",
    "NetworkLoss",
    "NetworkDuplicate",
    "NetworkCorrupt",
    "NetworkBandwidth",
    "NetworkPartition",
    "JVMLatency",
    "JVMReturn",
    "JVMException",
    "JVMGarbageCollector",
    "JVMCPUStress",
    "JVMMemoryStress",
    "JVMMySQLLatency",
    "JVMMySQLException",
)


# Kinds that need direction.src/dst in the agent answer (and that GT extracts
# from injection.json's target_service / direction fields).
NETWORK_KINDS: frozenset[FaultKind] = frozenset(
    {
        FaultKind.NETWORK_DELAY,
        FaultKind.NETWORK_LOSS,
        FaultKind.NETWORK_PARTITION,
        FaultKind.NETWORK_CORRUPT,
        FaultKind.NETWORK_DUPLICATE,
        FaultKind.NETWORK_BANDWIDTH_THROTTLED,
    }
)

# Kinds where `class.method` is meaningful (used by GT extractor to pull
# leaf.class + leaf.method into GTFault.method, and by the matcher to set
# `method_match` as a diagnostic bit).
METHOD_RELEVANT_KINDS: frozenset[FaultKind] = frozenset(
    {
        FaultKind.APPLICATION_EXCEPTION,
        FaultKind.DATABASE_CALL_FAILED,
        FaultKind.APPLICATION_METHOD_SLOW,
        FaultKind.DATABASE_CALL_SLOW,
        FaultKind.WRONG_RETURN_VALUE,
        FaultKind.HTTP_ABORTED,
        FaultKind.HTTP_SLOW,
        FaultKind.HTTP_RESPONSE_BODY_CORRUPTED,
        FaultKind.HTTP_WRONG_STATUS_CODE,
    }
)


def map_chaos_type(chaos_type: str | None) -> FaultKind:
    """Map a chaos_type string from engine_config (or a numeric old-format
    fault_type already decoded) to the controlled FaultKind. Unknown strings
    return ``FaultKind.UNKNOWN`` (do NOT raise — the eval needs to keep going)."""
    if not chaos_type:
        return FaultKind.UNKNOWN
    return _CHAOS_TYPE_MAP.get(chaos_type, FaultKind.UNKNOWN)


def chaos_type_from_index(idx: int | str | None) -> str | None:
    """Decode the numeric ``fault_type`` field used by old-format injection.json
    into its canonical string name. Returns ``None`` for out-of-range indices.
    """
    if idx is None:
        return None
    try:
        i = int(idx)
    except (TypeError, ValueError):
        return None
    if 0 <= i < len(CANONICAL_FAULT_TYPES):
        return CANONICAL_FAULT_TYPES[i]
    return None
