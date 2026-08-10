"""Feature-extraction layer that materialises canonical manifest features.

Phase 3.5 wiring: bridges the existing IR adapters
(``K8sMetricsAdapter`` / ``TraceStateAdapter`` / ``TraceVolumeAdapter`` /
``JvmAugmenterAdapter``) into the typed
``(node_id, FeatureKind, Feature) -> float`` map that
``ManifestEntryGate`` and ``ManifestLayerGate`` read from
``ReasoningContext.feature_samples``.

The 14 canonical features are produced by these adapters:

* k8s_metrics:        cpu_throttle_ratio, memory_usage_ratio,
                      restart_count, unavailable (pod)
* trace:              latency_p99_ratio, latency_p50_ratio, error_rate,
                      request_count_ratio, dns_failure_rate,
                      connection_refused_rate, timeout_rate
* trace_volume:       silent (service), unavailable (service)
* jvm:                gc_pause_ratio, thread_queue_depth

Features whose underlying telemetry is absent on a given case emit no
entry — the gates already treat a missing sample as "feature did not
match", so defaulting to ``0.0`` would be the wrong polarity.
"""

from rcabench_platform.v3.internal.reasoning.manifests.extractors.feature_extractor import (
    extract_feature_samples,
)

__all__ = ["extract_feature_samples"]
