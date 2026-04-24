"""State detection for nodes based on metrics and attributes.

All state detection is timeline-based, returning StateWindow objects.
Each window represents a time range with a set of states (supports multi-state).
"""

import numpy as np

from rcabench_platform.v3.internal.reasoning.algorithms.baseline_detector import (
    BaselineAwareDetector,
    BaselineStatistics,
    calculate_series_stats,
    compute_baseline_statistics,
    get_adaptive_threshold,
)
from rcabench_platform.v3.internal.reasoning.models.graph import Node, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.state import (
    ContainerState,
    DaemonSetState,
    DeploymentState,
    MachineState,
    NamespaceState,
    PodState,
    ReplicaSetState,
    SpanState,
    StatefulSetState,
    StateWindow,
)


class WindowSizeConfig:
    """Configurable window sizes per fault type.

    Network faults may need finer granularity (1-2 seconds),
    while resource faults can use coarser windows (5 seconds).
    """

    # Default window size in seconds
    DEFAULT = 5

    # Per-PlaceKind window sizes (in seconds)
    # Network-related entities need finer granularity
    SPAN = 3  # Spans may have short-lived latency spikes
    SERVICE = 3  # Service metrics correlate with spans

    # Resource-related entities can use coarser windows
    POD = 5
    CONTAINER = 5
    MACHINE = 5

    # Kubernetes control plane entities
    DEPLOYMENT = 5
    REPLICA_SET = 5
    STATEFUL_SET = 5
    DAEMON_SET = 5
    NAMESPACE = 5

    @classmethod
    def get_window_size(cls, place_kind: PlaceKind) -> int:
        """Get window size for a specific PlaceKind.

        Args:
            place_kind: The PlaceKind to get window size for

        Returns:
            Window size in seconds
        """
        kind_to_size = {
            PlaceKind.span: cls.SPAN,
            PlaceKind.service: cls.SERVICE,
            PlaceKind.pod: cls.POD,
            PlaceKind.container: cls.CONTAINER,
            PlaceKind.machine: cls.MACHINE,
            PlaceKind.deployment: cls.DEPLOYMENT,
            PlaceKind.replica_set: cls.REPLICA_SET,
            PlaceKind.stateful_set: cls.STATEFUL_SET,
            PlaceKind.daemon_set: cls.DAEMON_SET,
            PlaceKind.namespace: cls.NAMESPACE,
        }
        return kind_to_size.get(place_kind, cls.DEFAULT)


class StateDetectionThresholds:
    # Resource utilization thresholds
    HIGH_CPU = 0.8
    HIGH_MEMORY = 0.8

    # Error rate thresholds
    CRITICAL_ERROR_RATE = 0.3
    UNAVAILABLE_ERROR_RATE = 0.8

    # Latency thresholds (seconds)
    HIGH_P99_LATENCY = 5.0
    CRITICAL_P99_LATENCY = 10.0

    # Relative change thresholds
    LATENCY_MULTIPLIER_HIGH = 3.0
    LATENCY_MULTIPLIER_CRITICAL = 10.0
    ERROR_INCREASE_CRITICAL = 0.3


def _compute_node_baseline_stats(node: Node) -> dict[str, BaselineStatistics]:
    """Compute baseline statistics for all metrics in a node.

    Args:
        node: Node containing baseline metrics

    Returns:
        Dictionary mapping metric names to their baseline statistics
    """
    stats = {}
    for metric_name, (_timestamps, values) in node.baseline_metrics.items():
        if isinstance(values, np.ndarray) and len(values) > 0:
            stats[metric_name] = compute_baseline_statistics(values)
    return stats


def _normalize_timestamp(ts: np.ndarray) -> np.ndarray:
    """Convert timestamps to Unix seconds (int64).

    Handles datetime64, Python datetime objects, and numeric timestamps.
    """
    if len(ts) == 0:
        return np.array([], dtype=np.int64)

    # Check if it's datetime64 type
    if np.issubdtype(ts.dtype, np.datetime64):
        # Convert to seconds since epoch
        return ts.astype("datetime64[s]").astype(np.int64)

    # Check if it's object dtype (might contain Python datetime objects)
    if ts.dtype == object:
        from datetime import datetime

        first_elem = ts[0]
        if isinstance(first_elem, datetime):
            # Convert Python datetime objects to Unix seconds
            return np.array([int(dt.timestamp()) for dt in ts], dtype=np.int64)

    # Already numeric (assume seconds)
    return ts.astype(np.int64)


def _get_all_timestamps(node: Node) -> list[int]:
    """Extract all unique timestamps from node metrics (abnormal period).

    Returns timestamps as Unix seconds (int).
    """
    all_timestamps: set[int] = set()
    for _metric_name, (timestamps, _values) in node.abnormal_metrics.items():
        if isinstance(timestamps, np.ndarray) and len(timestamps) > 0:
            normalized = _normalize_timestamp(timestamps)
            all_timestamps.update(normalized.tolist())
    return sorted(all_timestamps)


def _get_all_baseline_timestamps(node: Node) -> list[int]:
    """Extract all unique timestamps from node baseline metrics.

    Returns timestamps as Unix seconds (int).
    """
    all_timestamps: set[int] = set()
    for _metric_name, (timestamps, _values) in node.baseline_metrics.items():
        if isinstance(timestamps, np.ndarray) and len(timestamps) > 0:
            normalized = _normalize_timestamp(timestamps)
            all_timestamps.update(normalized.tolist())
    return sorted(all_timestamps)


def _generate_fixed_windows(node: Node, window_size_sec: int | None = None) -> list[tuple[int, int]]:
    """Generate fixed-size time windows from node metrics.

    Args:
        node: Node containing metrics with timestamps
        window_size_sec: Window size in seconds (default: auto-determined by PlaceKind)

    Returns:
        List of (start_time, end_time) tuples in Unix seconds.
        Returns empty list if no timestamps found.
    """
    # Use configurable window size based on node kind if not explicitly provided
    if window_size_sec is None:
        window_size_sec = WindowSizeConfig.get_window_size(node.kind)

    all_timestamps = _get_all_timestamps(node)
    if not all_timestamps:
        return []

    period_start = min(all_timestamps)
    period_end = max(all_timestamps)

    # Generate fixed windows
    windows = []
    current_time = period_start
    while current_time < period_end:
        window_end = current_time + window_size_sec
        windows.append((current_time, window_end))
        current_time = window_end

    # Ensure we cover the last timestamp
    if not windows or windows[-1][1] < period_end:
        windows.append((current_time, current_time + window_size_sec))

    return windows


def _aggregate_metric_in_window(
    node: Node,
    metric_name: str,
    start_time: int,
    end_time: int,
    agg_func: str = "mean",
    use_baseline: bool = False,
) -> float | None:
    """Aggregate metric values within a time window.

    Args:
        node: Node containing metrics
        metric_name: Name of the metric to aggregate
        start_time: Window start (Unix seconds)
        end_time: Window end (Unix seconds)
        agg_func: Aggregation function ("mean", "max", "min", "sum")
        use_baseline: If True, use baseline_metrics; otherwise use abnormal_metrics

    Returns:
        Aggregated value, or None if no data points in window
    """
    metrics_dict = node.baseline_metrics if use_baseline else node.abnormal_metrics

    if metric_name not in metrics_dict:
        return None

    timestamps, values = metrics_dict[metric_name]
    if not isinstance(timestamps, np.ndarray) or not isinstance(values, np.ndarray):
        return None
    if len(timestamps) == 0 or len(values) == 0:
        return None

    # Normalize timestamps to seconds
    ts_seconds = _normalize_timestamp(timestamps)

    # Filter values within window
    mask = (ts_seconds >= start_time) & (ts_seconds < end_time)
    window_values = values[mask]

    if len(window_values) == 0:
        return None

    # Apply aggregation
    if agg_func == "mean":
        return float(np.mean(window_values))
    elif agg_func == "max":
        return float(np.max(window_values))
    elif agg_func == "min":
        return float(np.min(window_values))
    elif agg_func == "sum":
        return float(np.sum(window_values))
    else:
        return float(np.mean(window_values))


def _filter_metrics_by_time_range(
    metrics_dict: dict[str, tuple[np.ndarray, np.ndarray]],
    start_time: int,
    end_time: int,
) -> dict[str, tuple[np.ndarray, np.ndarray]]:
    """Filter metrics to only include data points within time range.

    Args:
        metrics_dict: Dictionary of metric_name -> (timestamps, values)
        start_time: Window start (Unix seconds)
        end_time: Window end (Unix seconds)

    Returns:
        Filtered metrics dictionary
    """
    filtered = {}
    for metric_name, (timestamps, values) in metrics_dict.items():
        if not isinstance(timestamps, np.ndarray) or not isinstance(values, np.ndarray):
            continue
        # Normalize timestamps to seconds
        ts_seconds = _normalize_timestamp(timestamps)
        mask = (ts_seconds >= start_time) & (ts_seconds < end_time)
        filtered_ts = timestamps[mask]
        filtered_vals = values[mask]
        if len(filtered_ts) > 0:
            filtered[metric_name] = (filtered_ts, filtered_vals)
    return filtered


def _merge_consecutive_windows(windows: list[StateWindow]) -> list[StateWindow]:
    if not windows:
        return []

    merged = []
    current = windows[0]

    for next_window in windows[1:]:
        # Merge if states are identical and windows are adjacent
        if current.states == next_window.states and current.end_time >= next_window.start_time:
            current = StateWindow(
                start_time=current.start_time,
                end_time=next_window.end_time,
                states=current.states,
            )
        else:
            merged.append(current)
            current = next_window

    merged.append(current)
    return merged


def detect_deployment_state_timeline(node: Node) -> list[StateWindow]:
    """Detect Deployment state timeline using fixed 5-second windows."""
    time_windows = _generate_fixed_windows(node)
    if not time_windows:
        return []

    windows = []
    for start_time, end_time in time_windows:
        available = _aggregate_metric_in_window(node, "k8s.deployment.available", start_time, end_time, "mean")
        desired = _aggregate_metric_in_window(node, "k8s.deployment.desired", start_time, end_time, "mean")

        # No data in window -> UNKNOWN
        if available is None or desired is None:
            state = DeploymentState.UNKNOWN.value
        elif desired == 0:
            state = DeploymentState.UNKNOWN.value
        elif available == 0:
            state = DeploymentState.FAILED.value
        elif available < desired:
            state = DeploymentState.DEGRADED.value
        else:
            state = DeploymentState.AVAILABLE.value

        windows.append(StateWindow(start_time=start_time, end_time=end_time, states={state}))

    return _merge_consecutive_windows(windows)


def detect_stateful_set_state_timeline(node: Node) -> list[StateWindow]:
    """Detect StatefulSet state timeline using fixed 5-second windows."""
    time_windows = _generate_fixed_windows(node)
    if not time_windows:
        return []

    windows = []
    for start_time, end_time in time_windows:
        ready_pods = _aggregate_metric_in_window(node, "k8s.statefulset.ready_pods", start_time, end_time, "mean")
        desired_pods = _aggregate_metric_in_window(node, "k8s.statefulset.desired_pods", start_time, end_time, "mean")

        # No data in window -> UNKNOWN
        if ready_pods is None or desired_pods is None:
            state = StatefulSetState.UNKNOWN.value
        elif desired_pods == 0:
            state = StatefulSetState.UNKNOWN.value
        elif ready_pods == 0:
            state = StatefulSetState.FAILED.value
        elif ready_pods < desired_pods:
            state = StatefulSetState.DEGRADED.value
        else:
            state = StatefulSetState.READY.value

        windows.append(StateWindow(start_time=start_time, end_time=end_time, states={state}))

    return _merge_consecutive_windows(windows)


def detect_replica_set_state_timeline(node: Node) -> list[StateWindow]:
    """Detect ReplicaSet state timeline using fixed 5-second windows."""
    time_windows = _generate_fixed_windows(node)
    if not time_windows:
        return []

    windows = []
    for start_time, end_time in time_windows:
        available = _aggregate_metric_in_window(node, "k8s.replicaset.available", start_time, end_time, "mean")
        desired = _aggregate_metric_in_window(node, "k8s.replicaset.desired", start_time, end_time, "mean")

        # No data in window -> UNKNOWN
        if available is None or desired is None:
            state = ReplicaSetState.UNKNOWN.value
        elif desired == 0:
            state = ReplicaSetState.UNKNOWN.value
        elif available == 0:
            state = ReplicaSetState.FAILED.value
        elif available < desired:
            state = ReplicaSetState.DEGRADED.value
        else:
            state = ReplicaSetState.AVAILABLE.value

        windows.append(StateWindow(start_time=start_time, end_time=end_time, states={state}))

    return _merge_consecutive_windows(windows)


def _detect_pod_killed(node: Node) -> tuple[bool, int | None, int | None]:
    """Detect if a Pod was killed during the abnormal period.

    Detection strategy: Compare the time coverage of metrics between baseline and abnormal periods.
    If the abnormal period metrics end significantly earlier than baseline metrics coverage would
    suggest, the Pod was likely killed.

    For PodKill scenarios:
    - Old Pod gets killed, metrics stop
    - New Pod is created with different name, so this Pod node has no more data

    Returns:
        Tuple of (was_killed, kill_start_time, kill_end_time)
        - was_killed: True if Pod appears to have been killed
        - kill_start_time: Estimated time when Pod was killed (last metric timestamp)
        - kill_end_time: End of abnormal period (for state window)
    """
    baseline_timestamps = _get_all_baseline_timestamps(node)
    abnormal_timestamps = _get_all_timestamps(node)

    if not baseline_timestamps or not abnormal_timestamps:
        return False, None, None

    # Calculate baseline duration and coverage
    baseline_duration = max(baseline_timestamps) - min(baseline_timestamps)
    if baseline_duration < 30:  # Need at least 30 seconds of baseline data
        return False, None, None

    # Calculate abnormal duration
    abnormal_duration = max(abnormal_timestamps) - min(abnormal_timestamps)

    # If abnormal period is significantly shorter than baseline (less than 50% coverage),
    # the Pod was likely killed
    coverage_ratio = abnormal_duration / baseline_duration if baseline_duration > 0 else 1.0

    if coverage_ratio < 0.5:
        # Pod was likely killed - metrics stopped early
        kill_time = max(abnormal_timestamps)
        # Estimate end of abnormal period based on baseline duration
        estimated_abnormal_end = min(abnormal_timestamps) + baseline_duration
        return True, kill_time, estimated_abnormal_end

    return False, None, None


def detect_pod_state_timeline(node: Node) -> list[StateWindow]:
    """Detect Pod state timeline using fixed 5-second windows.

    Supports multiple simultaneous states.
    Detection strategy:
    - Utilization metrics (0-1 range): Use BOTH fixed thresholds AND baseline comparison
    - Absolute/counter metrics: Use baseline comparison with Z-score

    The dual detection approach catches:
    - Fixed threshold: Absolute resource exhaustion (>80% utilization)
    - Baseline comparison: Significant relative increase (e.g., JVMCPUStress causing 20x CPU increase
      even if absolute utilization is below 80%)
    """
    time_windows = _generate_fixed_windows(node)

    # Check for Pod killed (metrics stopped early)
    was_killed, kill_start, kill_end = _detect_pod_killed(node)

    if not time_windows:
        # If no time windows but Pod was killed, create a KILLED state window
        if was_killed and kill_start is not None and kill_end is not None:
            return [StateWindow(start_time=kill_start, end_time=kill_end, states={PodState.KILLED.value})]
        return []

    # Compute baseline statistics for anomaly detection
    baseline_stats = _compute_node_baseline_stats(node)
    detector = BaselineAwareDetector(baseline_stats) if baseline_stats else None

    windows = []
    for start_time, end_time in time_windows:
        states: set[str] = set()

        # === CPU Detection: Fixed threshold OR baseline comparison ===
        cpu_util = _aggregate_metric_in_window(node, "k8s.pod.cpu_limit_utilization", start_time, end_time, "mean")

        # Method 1: Fixed threshold for absolute high CPU (>80%)
        if cpu_util is not None and cpu_util > StateDetectionThresholds.HIGH_CPU:
            states.add(PodState.HIGH_CPU.value)

        # Method 2: Baseline comparison for relative CPU increase
        # This catches cases like JVMCPUStress where CPU increases 20x but stays below 80%
        if detector and PodState.HIGH_CPU.value not in states:
            # Check cpu_limit_utilization against baseline
            if "k8s.pod.cpu_limit_utilization" in baseline_stats:
                if cpu_util is not None and detector.is_critical_anomaly("k8s.pod.cpu_limit_utilization", cpu_util):
                    states.add(PodState.HIGH_CPU.value)

            # Also check absolute CPU usage (k8s.pod.cpu.usage) - useful for JVM stress faults
            if "k8s.pod.cpu.usage" in baseline_stats and PodState.HIGH_CPU.value not in states:
                cpu_usage = _aggregate_metric_in_window(node, "k8s.pod.cpu.usage", start_time, end_time, "mean")
                if cpu_usage is not None and detector.is_critical_anomaly("k8s.pod.cpu.usage", cpu_usage):
                    states.add(PodState.HIGH_CPU.value)

            # Check JVM CPU utilization - catches JVMCPUStress specifically
            if "jvm.cpu.recent_utilization" in baseline_stats and PodState.HIGH_CPU.value not in states:
                jvm_cpu = _aggregate_metric_in_window(node, "jvm.cpu.recent_utilization", start_time, end_time, "mean")
                if jvm_cpu is not None and detector.is_critical_anomaly("jvm.cpu.recent_utilization", jvm_cpu):
                    states.add(PodState.HIGH_CPU.value)

        # === Memory Detection: Fixed threshold OR baseline comparison ===
        memory_util = _aggregate_metric_in_window(
            node, "k8s.pod.memory_limit_utilization", start_time, end_time, "mean"
        )

        # Method 1: Fixed threshold for absolute high memory (>80%)
        if memory_util is not None and memory_util > StateDetectionThresholds.HIGH_MEMORY:
            states.add(PodState.HIGH_MEMORY.value)

        # Method 2: Baseline comparison for relative memory increase
        if detector and PodState.HIGH_MEMORY.value not in states:
            if "k8s.pod.memory_limit_utilization" in baseline_stats:
                if memory_util is not None and detector.is_critical_anomaly(
                    "k8s.pod.memory_limit_utilization", memory_util
                ):
                    states.add(PodState.HIGH_MEMORY.value)

            # Also check absolute memory usage
            if "k8s.pod.memory.working_set" in baseline_stats and PodState.HIGH_MEMORY.value not in states:
                mem_ws = _aggregate_metric_in_window(node, "k8s.pod.memory.working_set", start_time, end_time, "mean")
                if mem_ws is not None and detector.is_critical_anomaly("k8s.pod.memory.working_set", mem_ws):
                    states.add(PodState.HIGH_MEMORY.value)

        # Filesystem usage - use fixed threshold for utilization (has natural 0-1 boundary)
        fs_usage = _aggregate_metric_in_window(node, "k8s.pod.filesystem.usage", start_time, end_time, "mean")
        fs_capacity = _aggregate_metric_in_window(node, "k8s.pod.filesystem.capacity", start_time, end_time, "mean")
        if fs_usage is not None and fs_capacity is not None and fs_capacity > 0:
            fs_utilization = fs_usage / fs_capacity
            if fs_utilization > 0.95:  # Disk usage > 95%
                states.add(PodState.HIGH_DISK_USAGE.value)

        # === Absolute/counter metrics: Use BASELINE COMPARISON (no fixed upper bound) ===
        if detector:
            # Network errors - counter metric, compare increase against baseline
            if "k8s.pod.network.errors" in baseline_stats:
                net_errors = _aggregate_metric_in_window(node, "k8s.pod.network.errors", start_time, end_time, "mean")
                if net_errors is not None and detector.is_critical_anomaly("k8s.pod.network.errors", net_errors):
                    states.add(PodState.HIGH_NETWORK_ERRORS.value)

            # HTTP latency - absolute duration varies by service type
            if "http.server.request.duration.sum" in baseline_stats:
                http_sum = _aggregate_metric_in_window(
                    node, "http.server.request.duration.sum", start_time, end_time, "mean"
                )
                if http_sum is not None and detector.is_critical_anomaly("http.server.request.duration.sum", http_sum):
                    states.add(PodState.HIGH_HTTP_LATENCY.value)

            # JVM GC pressure - absolute duration varies by application
            if "jvm.gc.duration.sum" in baseline_stats:
                gc_sum = _aggregate_metric_in_window(node, "jvm.gc.duration.sum", start_time, end_time, "mean")
                if gc_sum is not None and detector.is_critical_anomaly("jvm.gc.duration.sum", gc_sum):
                    states.add(PodState.HIGH_GC_PRESSURE.value)

        # No data or no anomalies -> check if we have any metrics at all
        if not states:
            # Check if any metric has data in this window
            has_data = any(
                _aggregate_metric_in_window(node, m, start_time, end_time, "mean") is not None
                for m in node.abnormal_metrics
            )
            if has_data:
                states.add(PodState.HEALTHY.value)
            else:
                states.add(PodState.UNKNOWN.value)

        windows.append(StateWindow(start_time=start_time, end_time=end_time, states=states))

    # If Pod was killed, add KILLED state to windows after the kill time
    # Also extend the timeline to cover the period after metrics stopped
    if was_killed and kill_start is not None and kill_end is not None:
        updated_windows: list[StateWindow] = []
        for window in windows:
            if window.start_time >= kill_start:
                # This window is after the kill - add KILLED state
                new_states = window.states | {PodState.KILLED.value}
                updated_windows.append(
                    StateWindow(start_time=window.start_time, end_time=window.end_time, states=new_states)
                )
            else:
                updated_windows.append(window)

        # If there's a gap between last window and estimated end, add a KILLED window
        if updated_windows and updated_windows[-1].end_time < kill_end:
            updated_windows.append(
                StateWindow(
                    start_time=updated_windows[-1].end_time,
                    end_time=kill_end,
                    states={PodState.KILLED.value},
                )
            )
        windows = updated_windows

    return _merge_consecutive_windows(windows)


def _detect_container_restart(node: Node) -> tuple[bool, int | None, int | None]:
    """Detect if container has restarted during abnormal period.

    Uses k8s.container.restarts counter metric. Detects the time window when
    the restart count increases (from 0->1, 1->2, etc.).

    Returns:
        Tuple of (has_restarted, restart_start_time, restart_end_time)
        - has_restarted: True if a restart was detected
        - restart_start_time: Unix timestamp when restart counter increased (in seconds)
        - restart_end_time: Unix timestamp of next data point after restart (in seconds)
    """
    restart_metric = "k8s.container.restarts"

    # Get baseline max to compare against
    baseline_max = 0.0
    if restart_metric in node.baseline_metrics:
        _, baseline_values = node.baseline_metrics[restart_metric]
        if isinstance(baseline_values, np.ndarray) and len(baseline_values) > 0:
            baseline_max = float(np.max(baseline_values))

    # Check abnormal period for restart
    if restart_metric not in node.abnormal_metrics:
        return False, None, None

    timestamps, values = node.abnormal_metrics[restart_metric]
    if not isinstance(values, np.ndarray) or len(values) == 0:
        return False, None, None

    # Find the first point where value increases above baseline
    restart_start_time = None
    restart_end_time = None

    for i in range(len(values)):
        current_value = float(values[i])
        if current_value > baseline_max:
            # Found restart - the restart happened between previous and current timestamp
            if i > 0:
                # Restart window starts at previous timestamp (last known good state)
                ts = timestamps[i - 1]
                restart_start_time = int(ts.timestamp()) if hasattr(ts, "timestamp") else int(ts)
            else:
                # Restart at the very beginning
                ts = timestamps[i]
                restart_start_time = int(ts.timestamp()) if hasattr(ts, "timestamp") else int(ts)

            # Restart window ends at current timestamp (container is back up)
            ts = timestamps[i]
            restart_end_time = int(ts.timestamp()) if hasattr(ts, "timestamp") else int(ts)
            break

    if restart_start_time is not None:
        return True, restart_start_time, restart_end_time

    return False, None, None


def detect_container_state_timeline(node: Node) -> list[StateWindow]:
    """Detect Container state timeline using fixed 5-second windows.

    Supports multiple simultaneous states.
    Uses baseline comparison with Z-score for absolute usage metrics.
    Container metrics are absolute values (bytes, cores) that vary by container size.
    """
    time_windows = _generate_fixed_windows(node)
    if not time_windows:
        return []

    # Compute baseline statistics for anomaly detection
    baseline_stats = _compute_node_baseline_stats(node)
    detector = BaselineAwareDetector(baseline_stats) if baseline_stats else None

    # Detect container restart and get the precise restart time window
    has_restarted, restart_start, restart_end = _detect_container_restart(node)

    windows = []
    for start_time, end_time in time_windows:
        states: set[str] = set()

        # Add RESTARTING state only if this window overlaps with the restart period
        if has_restarted and restart_start is not None and restart_end is not None:
            # Check if this window overlaps with restart period
            if start_time <= restart_end and end_time >= restart_start:
                states.add(ContainerState.RESTARTING.value)

        # Container metrics are absolute usage values, use baseline comparison
        if detector and "container.memory.working_set" in baseline_stats:
            memory_usage = _aggregate_metric_in_window(
                node, "container.memory.working_set", start_time, end_time, "mean"
            )
            if memory_usage is not None and detector.is_critical_anomaly("container.memory.working_set", memory_usage):
                states.add(ContainerState.HIGH_MEMORY.value)

        if detector and "container.cpu.usage" in baseline_stats:
            cpu_usage = _aggregate_metric_in_window(node, "container.cpu.usage", start_time, end_time, "mean")
            if cpu_usage is not None and detector.is_critical_anomaly("container.cpu.usage", cpu_usage):
                states.add(ContainerState.HIGH_CPU.value)

        # Filesystem usage - use fixed threshold for utilization
        fs_usage = _aggregate_metric_in_window(node, "container.filesystem.usage", start_time, end_time, "mean")
        fs_capacity = _aggregate_metric_in_window(node, "container.filesystem.capacity", start_time, end_time, "mean")
        if fs_usage is not None and fs_capacity is not None and fs_capacity > 0:
            fs_utilization = fs_usage / fs_capacity
            if fs_utilization > 0.95:  # Disk usage > 95%
                states.add(ContainerState.HIGH_DISK_USAGE.value)

        # No anomalies -> check if we have any metrics at all
        if not states:
            has_data = any(
                _aggregate_metric_in_window(node, m, start_time, end_time, "mean") is not None
                for m in node.abnormal_metrics
            )
            if has_data:
                states.add(ContainerState.HEALTHY.value)
            else:
                states.add(ContainerState.UNKNOWN.value)

        windows.append(StateWindow(start_time=start_time, end_time=end_time, states=states))

    return _merge_consecutive_windows(windows)


def detect_service_state_timeline(node: Node) -> list[StateWindow]:
    return []


def detect_span_state_timeline(node: Node) -> list[StateWindow]:
    time_windows = _generate_fixed_windows(node)

    # If no abnormal data, check for MISSING_SPAN as fallback
    if not time_windows:
        if _detect_missing_span(node):
            # For missing spans, we need to determine when the span became missing.
            # Since there's no abnormal data, use a time range that covers the abnormal period.
            # We use the baseline's time range as a reference, but shift it forward to represent
            # that the span is missing during the abnormal period (which comes after baseline).
            baseline_timestamps = _get_all_baseline_timestamps(node)
            if baseline_timestamps:
                # The abnormal period typically starts right after the baseline period ends.
                # Use the baseline end time as the start of the missing span window.
                # This represents: "span was present in baseline, but missing in abnormal period"
                baseline_end = max(baseline_timestamps)
                # Use a generous end time to cover the abnormal period
                # Add a buffer to ensure it covers the injection time
                start_time = baseline_end
                end_time = baseline_end + 600  # 10 minutes should cover most abnormal periods
            else:
                # Fallback to 0 if no baseline data (shouldn't happen for missing spans)
                start_time = 0
                end_time = 0
            return [StateWindow(start_time=start_time, end_time=end_time, states={SpanState.MISSING_SPAN.value})]
        return []

    # First pass: detect all states except MISSING_SPAN
    windows = []
    has_anomalous_state = False

    for start_time, end_time in time_windows:
        states = _detect_span_states(node, start_time, end_time)

        # Check if any anomalous (non-HEALTHY, non-UNKNOWN) state was detected
        anomalous_states = states - {SpanState.HEALTHY.value, SpanState.UNKNOWN.value}
        if anomalous_states:
            has_anomalous_state = True

        if not states:
            has_data = any(
                _aggregate_metric_in_window(node, m, start_time, end_time, "mean") is not None
                for m in node.abnormal_metrics
            )
            if has_data:
                states = {SpanState.HEALTHY.value}
            else:
                states = {SpanState.UNKNOWN.value}

        windows.append(StateWindow(start_time=start_time, end_time=end_time, states=states))

    # Second pass: only check MISSING_SPAN if no other anomalous states detected
    if not has_anomalous_state and _detect_missing_span(node):
        windows = [
            StateWindow(
                start_time=w.start_time,
                end_time=w.end_time,
                states=w.states | {SpanState.MISSING_SPAN.value},
            )
            for w in windows
        ]

    return _merge_consecutive_windows(windows)


def _detect_missing_span(node: Node) -> bool:
    """Detect if span is missing during abnormal period.

    Returns True if:
    - Baseline has meaningful traffic (request_count >= 1.0 mean, or total requests >= 3)
    - AND abnormal period has no data points OR significantly reduced traffic (<10% of baseline)
    """

    def get_series(metrics_dict: dict, key: str) -> np.ndarray:
        if key not in metrics_dict:
            return np.array([])
        _ts, values = metrics_dict[key]
        if isinstance(values, np.ndarray):
            return values
        return np.array([])

    baseline_request_count_vals = get_series(node.baseline_metrics, "request_count")
    abnormal_request_count_vals = get_series(node.abnormal_metrics, "request_count")

    b_request_mean, _, _ = calculate_series_stats(baseline_request_count_vals)

    # If baseline has no meaningful traffic, cannot detect missing span
    # Use >= 1.0 to include spans with exactly 1 request per window
    # Also check total requests (sum) to catch low-frequency but consistent traffic
    b_total_requests = baseline_request_count_vals.sum() if len(baseline_request_count_vals) > 0 else 0
    if b_request_mean < 1.0 and b_total_requests < 3:
        return False

    # Case 1: Abnormal period has no data points at all
    if len(abnormal_request_count_vals) == 0:
        return True

    # Case 2: Abnormal period has data but traffic dropped significantly
    a_request_mean, _, _ = calculate_series_stats(abnormal_request_count_vals)
    missing_threshold = 0.1
    return a_request_mean < b_request_mean * missing_threshold


def _detect_span_states(
    node: Node,
    start_time: int,
    end_time: int,
) -> set[str]:
    """Detect span states from trace metrics within time range.

    Detection principle: An anomaly is only flagged if the abnormal value exceeds
    the baseline's normal range (P95). If baseline itself has similar high values,
    the abnormal window should be considered healthy.

    Args:
        node: Node with baseline and abnormal metrics
        start_time: Window start (Unix seconds)
        end_time: Window end (Unix seconds)

    Returns:
        Set of detected state strings
    """
    states: set[str] = set()

    # Filter abnormal metrics by time range
    abnormal_metrics = _filter_metrics_by_time_range(node.abnormal_metrics, start_time, end_time)

    def get_series(metrics_dict: dict, key: str) -> np.ndarray:
        if key not in metrics_dict:
            return np.array([])
        _ts, values = metrics_dict[key]
        if isinstance(values, np.ndarray):
            return values
        return np.array([])

    # Get baseline and abnormal values
    baseline_error_rate_vals = get_series(node.baseline_metrics, "error_rate")
    abnormal_error_rate_vals = get_series(abnormal_metrics, "error_rate")

    baseline_p99_vals = get_series(node.baseline_metrics, "p99_duration")
    abnormal_p99_vals = get_series(abnormal_metrics, "p99_duration")

    baseline_avg_vals = get_series(node.baseline_metrics, "avg_duration")
    abnormal_avg_vals = get_series(abnormal_metrics, "avg_duration")

    # Calculate stats
    b_err_mean, _, _ = calculate_series_stats(baseline_error_rate_vals)
    a_err_mean, _, _ = calculate_series_stats(abnormal_error_rate_vals)

    b_p99_mean, b_p99_std, b_p99_cv = calculate_series_stats(baseline_p99_vals)
    a_p99_mean, a_p99_std, _ = calculate_series_stats(abnormal_p99_vals)
    # For P99, use P75 of abnormal values to catch mixed distributions
    a_p99_representative = float(np.percentile(abnormal_p99_vals, 75)) if len(abnormal_p99_vals) > 0 else 0.0
    # Baseline upper bound: P95 of baseline values (normal fluctuation range)
    b_p99_upper = float(np.percentile(baseline_p99_vals, 95)) if len(baseline_p99_vals) > 0 else 0.0

    b_avg_mean, b_avg_std, b_avg_cv = calculate_series_stats(baseline_avg_vals)
    a_avg_mean, a_avg_std, _ = calculate_series_stats(abnormal_avg_vals)
    # For avg latency, use mean of abnormal values (window is short)
    a_avg_representative = a_avg_mean
    # Baseline upper bound: P95 of baseline values
    b_avg_upper = float(np.percentile(baseline_avg_vals, 95)) if len(baseline_avg_vals) > 0 else 0.0

    # Check if span name contains "error" (case-insensitive)
    if "error" in node.self_name.lower():
        states.add(SpanState.HIGH_ERROR_RATE.value)

    # Detect HIGH_ERROR_RATE
    # Only flag if abnormal error rate exceeds baseline's normal range
    b_err_upper = float(np.percentile(baseline_error_rate_vals, 95)) if len(baseline_error_rate_vals) > 0 else 0.0
    if a_err_mean > max(b_err_upper, b_err_mean * 1.5) and a_err_mean > 0.1:
        states.add(SpanState.HIGH_ERROR_RATE.value)

    # Detect HIGH_P99_LATENCY or TIMEOUT
    # Must exceed BOTH: adaptive threshold AND baseline's normal upper bound (P95)
    if b_p99_mean > 0 and len(abnormal_p99_vals) > 0:
        p99_threshold = get_adaptive_threshold(b_p99_mean, b_p99_cv)
        adaptive_limit = b_p99_mean * p99_threshold

        # Only flag as anomaly if it exceeds baseline's P95 (normal fluctuation range)
        # This prevents false positives when baseline itself has occasional high values
        exceeds_baseline_range = a_p99_representative > b_p99_upper * 2

        # Also check against adaptive threshold
        exceeds_adaptive = a_p99_representative > adaptive_limit or a_p99_mean > adaptive_limit

        if exceeds_baseline_range and exceeds_adaptive:
            # If P99 latency exceeds 20 seconds, classify as TIMEOUT
            if a_p99_representative > 20.0:
                states.add(SpanState.TIMEOUT.value)
            else:
                states.add(SpanState.HIGH_P99_LATENCY.value)
    elif a_p99_representative > 20.0:  # Direct timeout for zero baseline (>20s)
        states.add(SpanState.TIMEOUT.value)
    elif a_p99_representative > 8.0:  # Fallback for zero baseline
        states.add(SpanState.HIGH_P99_LATENCY.value)

    # Detect HIGH_AVG_LATENCY or TIMEOUT
    # Must exceed BOTH: adaptive threshold AND baseline's normal upper bound (P95)
    if b_avg_mean > 0 and len(abnormal_avg_vals) > 0:
        avg_threshold = get_adaptive_threshold(b_avg_mean, b_avg_cv)
        adaptive_limit = b_avg_mean * avg_threshold

        # Only flag as anomaly if it exceeds baseline's P95
        exceeds_baseline_range = a_avg_representative > b_avg_upper * 2

        # Also check against adaptive threshold
        exceeds_adaptive = a_avg_representative > adaptive_limit or a_avg_mean > adaptive_limit

        if exceeds_baseline_range and exceeds_adaptive:
            # If latency exceeds 20 seconds, classify as TIMEOUT instead of high latency
            # This distinguishes actual timeouts from just slow responses
            if a_avg_representative > 20.0:
                states.add(SpanState.TIMEOUT.value)
            else:
                states.add(SpanState.HIGH_AVG_LATENCY.value)
    elif a_avg_representative > 20.0:  # Direct timeout for zero baseline (>20s)
        states.add(SpanState.TIMEOUT.value)
    elif a_avg_representative > 5.0:  # Fallback for zero baseline
        states.add(SpanState.HIGH_AVG_LATENCY.value)

    return states


def detect_machine_state_timeline(node: Node) -> list[StateWindow]:
    """Detect Machine state timeline using fixed 5-second windows."""
    time_windows = _generate_fixed_windows(node)
    if not time_windows:
        return []

    windows = []
    for start_time, end_time in time_windows:
        windows.append(StateWindow(start_time=start_time, end_time=end_time, states={MachineState.UNKNOWN.value}))

    return _merge_consecutive_windows(windows)


def detect_namespace_state_timeline(node: Node) -> list[StateWindow]:
    """Detect Namespace state timeline using fixed 5-second windows."""
    time_windows = _generate_fixed_windows(node)
    if not time_windows:
        return []

    windows = []
    for start_time, end_time in time_windows:
        windows.append(StateWindow(start_time=start_time, end_time=end_time, states={NamespaceState.ACTIVE.value}))

    return _merge_consecutive_windows(windows)


def detect_daemon_set_state_timeline(node: Node) -> list[StateWindow]:
    """Detect DaemonSet state timeline using fixed 5-second windows."""
    time_windows = _generate_fixed_windows(node)
    if not time_windows:
        return []

    windows = []
    for start_time, end_time in time_windows:
        windows.append(StateWindow(start_time=start_time, end_time=end_time, states={DaemonSetState.UNKNOWN.value}))

    return _merge_consecutive_windows(windows)


def detect_state_timeline(node: Node) -> list[StateWindow]:
    """Detect state timeline for any node based on its PlaceKind.

    Returns time windows with states that can vary over time.
    Nodes can have multiple simultaneous states.

    Args:
        node: Node to detect state timeline for

    Returns:
        List of StateWindow objects representing state changes over time
    """
    if node.kind == PlaceKind.machine:
        return detect_machine_state_timeline(node)
    elif node.kind == PlaceKind.namespace:
        return detect_namespace_state_timeline(node)
    elif node.kind == PlaceKind.deployment:
        return detect_deployment_state_timeline(node)
    elif node.kind == PlaceKind.stateful_set:
        return detect_stateful_set_state_timeline(node)
    elif node.kind == PlaceKind.replica_set:
        return detect_replica_set_state_timeline(node)
    elif node.kind == PlaceKind.daemon_set:
        return detect_daemon_set_state_timeline(node)
    elif node.kind == PlaceKind.pod:
        return detect_pod_state_timeline(node)
    elif node.kind == PlaceKind.container:
        return detect_container_state_timeline(node)
    elif node.kind == PlaceKind.service:
        return detect_service_state_timeline(node)
    elif node.kind == PlaceKind.span:
        return detect_span_state_timeline(node)
    else:
        return []
