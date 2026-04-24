from enum import auto

from pydantic import BaseModel

from rcabench_platform.compat import StrEnum
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind


class StateWindow(BaseModel):
    """Time window with associated states.

    A node can have multiple states simultaneously (e.g., HIGH_CPU + HIGH_MEMORY).
    """

    start_time: int  # Unix timestamp (seconds)
    end_time: int  # Unix timestamp (seconds)
    states: set[str]  # Set of states active during this window

    model_config = {"frozen": True}


class MachineState(StrEnum):
    UNKNOWN = auto()


class NamespaceState(StrEnum):
    ACTIVE = auto()
    UNKNOWN = auto()


class DeploymentState(StrEnum):
    """States for K8s deployments.

    Measurement:
    - AVAILABLE: k8s.deployment.available == k8s.deployment.desired
    - DEGRADED: k8s.deployment.available < k8s.deployment.desired
    - FAILED: k8s.deployment.available == 0 AND k8s.deployment.desired > 0
    - UNKNOWN: Metrics unavailable
    """

    AVAILABLE = auto()
    DEGRADED = auto()
    FAILED = auto()
    UNKNOWN = auto()


class StatefulSetState(StrEnum):
    """States for K8s stateful sets.

    Measurement:
    - READY: k8s.statefulset.ready_pods == k8s.statefulset.desired_pods
    - DEGRADED: k8s.statefulset.ready_pods < k8s.statefulset.desired_pods
    - FAILED: k8s.statefulset.ready_pods == 0 AND k8s.statefulset.desired_pods > 0
    - UNKNOWN: Metrics unavailable
    """

    READY = auto()
    DEGRADED = auto()
    FAILED = auto()
    UNKNOWN = auto()


class ReplicaSetState(StrEnum):
    """States for K8s replica sets.

    Measurement:
    - AVAILABLE: k8s.replicaset.available == k8s.replicaset.desired
    - DEGRADED: 0 < k8s.replicaset.available < k8s.replicaset.desired
    - FAILED: k8s.replicaset.available == 0 AND k8s.replicaset.desired > 0
    - UNKNOWN: Metrics unavailable
    """

    AVAILABLE = auto()
    DEGRADED = auto()
    FAILED = auto()
    UNKNOWN = auto()


class DaemonSetState(StrEnum):
    UNKNOWN = auto()


class PodState(StrEnum):
    HEALTHY = auto()
    UNKNOWN = auto()  # No data available in time window

    KILLED = auto()
    PROCESS_PAUSED = auto()  # e.g., chaos mesh pod-failure (pause container)

    HIGH_CPU = auto()
    HIGH_MEMORY = auto()
    HIGH_DISK_USAGE = auto()  # Detected via k8s.pod.filesystem.usage anomaly
    HIGH_NETWORK_ERRORS = auto()  # Detected via k8s.pod.network.errors anomaly
    HIGH_HTTP_LATENCY = auto()  # Detected via http.server.request.duration anomaly
    HIGH_GC_PRESSURE = auto()  # Detected via jvm.gc.duration anomaly (for Java apps)

    DISK_SLOW = auto()
    DISK_FAULT = auto()  # e.g., I/O errors
    DISK_CORRUPTION = auto()  # e.g., data corruption detected
    DISK_PERMISSION = auto()

    NETWORK_DELAY = auto()
    NETWORK_LOSS = auto()
    NETWORK_DUPLICATION = auto()
    NETWORK_CORRUPTION = auto()
    NETWORK_REORDERING = auto()
    NETWORK_BANDWIDTH_LIMIT = auto()
    NETWORK_PARTITION = auto()
    DNS_ERROR = auto()

    CLOCK_SKEW = auto()

    # === inferred state
    NO_CPU_AVAILABLE = auto()
    NO_MEMORY_AVAILABLE = auto()


class ContainerState(StrEnum):
    HEALTHY = auto()
    UNKNOWN = auto()  # No data available in time window

    KILLED = auto()
    PROCESS_PAUSED = auto()
    RESTARTING = auto()  # Container restart detected via k8s.container.restarts

    HIGH_CPU = auto()
    HIGH_MEMORY = auto()
    HIGH_DISK_USAGE = auto()  # Detected via filesystem usage > 95%

    DISK_SLOW = auto()
    DISK_FAULT = auto()  # e.g., I/O errors
    DISK_CORRUPTION = auto()  # e.g., data corruption detected
    DISK_PERMISSION = auto()

    CLOCK_SKEW = auto()

    # === inferred state
    NO_CPU_AVAILABLE = auto()
    NO_MEMORY_AVAILABLE = auto()


class ServiceState(StrEnum):
    HEALTHY = auto()
    HIGH_ERROR_RATE = auto()  # most of the spans have error status
    HIGH_LATENCY = auto()  # most of the spans have high latency
    UNAVAILABLE = auto()  # all spans are failing or timing out


class SpanState(StrEnum):
    HEALTHY = auto()  # No issues detected
    UNKNOWN = auto()  # No data available in time window
    HIGH_P99_LATENCY = auto()  # Span duration p99 significantly above baseline
    HIGH_AVG_LATENCY = auto()  # Span duration average significantly above baseline
    HIGH_ERROR_RATE = auto()  # Span error rate above baseline, e.g., HTTP 5xx
    TIMEOUT = auto()  # Span duration exceeds timeout threshold
    HIGH_LOG_ERROR = auto()  # High number of error logs within spans compared to baseline
    MISSING_SPAN = auto()  # Request count dropped to 0 while baseline had significant traffic

    CONNECTION_RESET = auto()  # e.g., TCP reset, HTTP abort
    MALFORMED_RESPONSE = auto()  # e.g., body replacement/patching

    # Infrastructure injection state - span affected by infrastructure fault (may not show in metrics)
    INJECTION_AFFECTED = auto()


STATE_ENUM_MAP: dict[PlaceKind, type[StrEnum]] = {
    PlaceKind.machine: MachineState,
    PlaceKind.namespace: NamespaceState,
    PlaceKind.deployment: DeploymentState,
    PlaceKind.stateful_set: StatefulSetState,
    PlaceKind.replica_set: ReplicaSetState,
    PlaceKind.daemon_set: DaemonSetState,
    PlaceKind.pod: PodState,
    PlaceKind.container: ContainerState,
    PlaceKind.service: ServiceState,
    PlaceKind.span: SpanState,
}


def get_state_enum(place_kind: PlaceKind) -> type[StrEnum]:
    return STATE_ENUM_MAP[place_kind]


def get_default_state(place_kind: PlaceKind) -> StrEnum:
    """Get the default (normal) state for a given PlaceKind.

    Args:
        place_kind: The PlaceKind to get default state for

    Returns:
        The default state for the PlaceKind
    """
    get_state_enum(place_kind)
    default_map = {
        PlaceKind.machine: MachineState.UNKNOWN,
        PlaceKind.namespace: NamespaceState.ACTIVE,
        PlaceKind.deployment: DeploymentState.AVAILABLE,
        PlaceKind.stateful_set: StatefulSetState.READY,
        PlaceKind.replica_set: ReplicaSetState.AVAILABLE,
        PlaceKind.daemon_set: DaemonSetState.UNKNOWN,
        PlaceKind.pod: PodState.HEALTHY,
        PlaceKind.container: ContainerState.HEALTHY,
        PlaceKind.service: ServiceState.HEALTHY,
        PlaceKind.span: SpanState.HEALTHY,
    }
    return default_map[place_kind]
