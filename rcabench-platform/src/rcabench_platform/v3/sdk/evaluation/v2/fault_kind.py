"""Controlled fault-kind vocabulary + chaos_type mapping."""

from __future__ import annotations

from enum import Enum


class FaultKind(str, Enum):
    POD_FAILURE = "pod_failure"
    CPU_STRESS = "cpu_stress"
    MEM_STRESS = "mem_stress"
    NETWORK_DELAY = "network_delay"
    NETWORK_LOSS = "network_loss"
    NETWORK_PARTITION = "network_partition"
    NETWORK_CORRUPT = "network_corrupt"
    NETWORK_DUPLICATE = "network_duplicate"
    JVM_EXCEPTION = "jvm_exception"
    JVM_MUTATOR = "jvm_mutator"
    HTTP_ABORT = "http_abort"
    HTTP_REPLACE = "http_replace"
    DNS = "dns"
    TIME_SKEW = "time_skew"
    UNKNOWN = "unknown"


_CHAOS_TYPE_MAP: dict[str, FaultKind] = {
    "PodFailure": FaultKind.POD_FAILURE,
    "PodKill": FaultKind.POD_FAILURE,
    "ContainerKill": FaultKind.POD_FAILURE,
    "CPUStress": FaultKind.CPU_STRESS,
    "JVMCPUStress": FaultKind.CPU_STRESS,
    "MemoryStress": FaultKind.MEM_STRESS,
    "JVMMemoryStress": FaultKind.MEM_STRESS,
    "NetworkDelay": FaultKind.NETWORK_DELAY,
    "NetworkLoss": FaultKind.NETWORK_LOSS,
    "NetworkPartition": FaultKind.NETWORK_PARTITION,
    "NetworkCorrupt": FaultKind.NETWORK_CORRUPT,
    "NetworkDuplicate": FaultKind.NETWORK_DUPLICATE,
    "JVMException": FaultKind.JVM_EXCEPTION,
    "JVMReturnValue": FaultKind.JVM_MUTATOR,
    "JVMRuntimeMutator": FaultKind.JVM_MUTATOR,
    "HTTPRequestAbort": FaultKind.HTTP_ABORT,
    "HTTPResponseAbort": FaultKind.HTTP_ABORT,
    "HTTPRequestReplaceMethod": FaultKind.HTTP_REPLACE,
    "HTTPResponseReplaceCode": FaultKind.HTTP_REPLACE,
    "HTTPResponseReplaceBody": FaultKind.HTTP_REPLACE,
    "HTTPRequestReplacePath": FaultKind.HTTP_REPLACE,
    "DNSChaos": FaultKind.DNS,
    "DNSError": FaultKind.DNS,
    "DNSRandom": FaultKind.DNS,
    "TimeChaos": FaultKind.TIME_SKEW,
    "TimeSkew": FaultKind.TIME_SKEW,
}


NETWORK_KINDS: frozenset[FaultKind] = frozenset(
    {
        FaultKind.NETWORK_DELAY,
        FaultKind.NETWORK_LOSS,
        FaultKind.NETWORK_PARTITION,
        FaultKind.NETWORK_CORRUPT,
        FaultKind.NETWORK_DUPLICATE,
    }
)

CODE_LEVEL_KINDS: frozenset[FaultKind] = frozenset(
    {
        FaultKind.JVM_EXCEPTION,
        FaultKind.JVM_MUTATOR,
        FaultKind.HTTP_ABORT,
        FaultKind.HTTP_REPLACE,
    }
)


def map_chaos_type(chaos_type: str | None) -> FaultKind:
    """Map a chaos_type string from engine_config to the controlled FaultKind."""
    if not chaos_type:
        return FaultKind.UNKNOWN
    return _CHAOS_TYPE_MAP.get(chaos_type, FaultKind.UNKNOWN)
