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
    # ── Pod / container lifecycle ────────────────────────────────────────
    POD_FAILURE = "pod_failure"  # PodKill, ContainerKill — service blips and recovers
    POD_UNAVAILABLE = "pod_unavailable"  # PodFailure — service stays gone for the window

    # ── L3/L4 network (direction is part of the GT) ─────────────────────
    NETWORK_DELAY = "network_delay"
    NETWORK_LOSS = "network_loss"
    NETWORK_PARTITION = "network_partition"
    NETWORK_CORRUPT = "network_corrupt"
    NETWORK_DUPLICATE = "network_duplicate"
    NETWORK_BANDWIDTH_LIMIT = "network_bandwidth_limit"

    # ── HTTP layer (no direction — span carries service_name itself) ────
    HTTP_ABORTED = "http_aborted"  # HTTPRequestAbort, HTTPResponseAbort
    HTTP_SLOW = "http_slow"  # HTTPRequestDelay, HTTPResponseDelay
    HTTP_PAYLOAD_MODIFIED = "http_payload_modified"  # ReplacePath/Method/Body, PatchBody
    HTTP_RESPONSE_STATUS_MODIFIED = "http_response_status_modified"  # ReplaceCode

    # ── Resource exhaustion ─────────────────────────────────────────────
    CPU_STRESS = "cpu_stress"  # container.cpu.usage saturated
    JVM_THREAD_CPU_STRESS = "jvm_thread_cpu_stress"  # jvm.cpu.* high specifically
    MEM_STRESS = "mem_stress"  # container.memory.usage saturated
    JVM_HEAP_STRESS = "jvm_heap_stress"  # jvm.memory.used → limit
    JVM_GC_PRESSURE = "jvm_gc_pressure"  # jvm.gc.duration histogram spikes

    # ── Code-level JVM ──────────────────────────────────────────────────
    JVM_METHOD_EXCEPTION = "jvm_method_exception"  # JVMException
    JVM_JDBC_EXCEPTION = "jvm_jdbc_exception"  # JVMMySQLException
    JVM_METHOD_LATENCY = "jvm_method_latency"  # JVMLatency
    JVM_JDBC_LATENCY = "jvm_jdbc_latency"  # JVMMySQLLatency
    JVM_METHOD_MUTATED = "jvm_method_mutated"  # JVMReturn, JVMRuntimeMutator

    # ── DNS / time ──────────────────────────────────────────────────────
    DNS_RESOLUTION_FAILED = "dns_resolution_failed"  # DNSError, DNSChaos (alias)
    DNS_RESOLUTION_WRONG = "dns_resolution_wrong"  # DNSRandom
    CLOCK_SKEW = "clock_skew"  # TimeSkew, TimeChaos (alias)

    UNKNOWN = "unknown"


# chaos_type → FaultKind
# Covers the 31 canonical FAULT_TYPES from rcabench's injection model plus
# chaos-mesh native names that show up in new-format injection.json.
_CHAOS_TYPE_MAP: dict[str, FaultKind] = {
    # Pod lifecycle
    "PodKill": FaultKind.POD_FAILURE,
    "PodFailure": FaultKind.POD_UNAVAILABLE,
    "ContainerKill": FaultKind.POD_FAILURE,
    # Resource (pod-level)
    "MemoryStress": FaultKind.MEM_STRESS,
    "CPUStress": FaultKind.CPU_STRESS,
    # HTTP
    "HTTPRequestAbort": FaultKind.HTTP_ABORTED,
    "HTTPResponseAbort": FaultKind.HTTP_ABORTED,
    "HTTPRequestDelay": FaultKind.HTTP_SLOW,
    "HTTPResponseDelay": FaultKind.HTTP_SLOW,
    "HTTPResponseReplaceBody": FaultKind.HTTP_PAYLOAD_MODIFIED,
    "HTTPResponsePatchBody": FaultKind.HTTP_PAYLOAD_MODIFIED,
    "HTTPRequestReplacePath": FaultKind.HTTP_PAYLOAD_MODIFIED,
    "HTTPRequestReplaceMethod": FaultKind.HTTP_PAYLOAD_MODIFIED,
    "HTTPResponseReplaceCode": FaultKind.HTTP_RESPONSE_STATUS_MODIFIED,
    # DNS
    "DNSError": FaultKind.DNS_RESOLUTION_FAILED,
    "DNSChaos": FaultKind.DNS_RESOLUTION_FAILED,  # chaos-mesh native alias
    "DNSRandom": FaultKind.DNS_RESOLUTION_WRONG,
    # Time
    "TimeSkew": FaultKind.CLOCK_SKEW,
    "TimeChaos": FaultKind.CLOCK_SKEW,  # chaos-mesh native alias
    # Network
    "NetworkDelay": FaultKind.NETWORK_DELAY,
    "NetworkLoss": FaultKind.NETWORK_LOSS,
    "NetworkDuplicate": FaultKind.NETWORK_DUPLICATE,
    "NetworkCorrupt": FaultKind.NETWORK_CORRUPT,
    "NetworkBandwidth": FaultKind.NETWORK_BANDWIDTH_LIMIT,
    "NetworkPartition": FaultKind.NETWORK_PARTITION,
    # JVM
    "JVMLatency": FaultKind.JVM_METHOD_LATENCY,
    "JVMReturn": FaultKind.JVM_METHOD_MUTATED,
    "JVMRuntimeMutator": FaultKind.JVM_METHOD_MUTATED,  # chaos-mesh native alias
    "JVMException": FaultKind.JVM_METHOD_EXCEPTION,
    "JVMGarbageCollector": FaultKind.JVM_GC_PRESSURE,
    "JVMCPUStress": FaultKind.JVM_THREAD_CPU_STRESS,
    "JVMMemoryStress": FaultKind.JVM_HEAP_STRESS,
    "JVMMySQLLatency": FaultKind.JVM_JDBC_LATENCY,
    "JVMMySQLException": FaultKind.JVM_JDBC_EXCEPTION,
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
        FaultKind.NETWORK_BANDWIDTH_LIMIT,
    }
)

# Kinds where `class.method` is meaningful (used by GT extractor to pull
# leaf.class + leaf.method into GTFault.method, and by the matcher to set
# `method_match` as a diagnostic bit).
METHOD_RELEVANT_KINDS: frozenset[FaultKind] = frozenset(
    {
        FaultKind.JVM_METHOD_EXCEPTION,
        FaultKind.JVM_JDBC_EXCEPTION,
        FaultKind.JVM_METHOD_LATENCY,
        FaultKind.JVM_JDBC_LATENCY,
        FaultKind.JVM_METHOD_MUTATED,
        FaultKind.HTTP_ABORTED,
        FaultKind.HTTP_SLOW,
        FaultKind.HTTP_PAYLOAD_MODIFIED,
        FaultKind.HTTP_RESPONSE_STATUS_MODIFIED,
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
