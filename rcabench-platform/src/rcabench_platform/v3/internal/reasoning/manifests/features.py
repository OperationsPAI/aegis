"""Canonical feature vocabulary for fault manifests.

The 14 bootstrap features defined here are the *only* feature names a
manifest YAML may reference. Adding new features requires (a) extending
:data:`FEATURE_METADATA` here and (b) wiring an extraction adapter in the
IR layer so verification can sample the feature on real telemetry.

The feature names are exposed as a string Enum so manifest validators
can coerce / check against them, while still allowing YAML files to
reference them as plain strings.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Final


class FeatureKind(str, Enum):
    """Node-kind a feature can apply to.

    Mirrors the PlaceKind universe used elsewhere in the IR; kept as a
    separate Enum here so the manifests package has zero hard dependency
    on ``models.graph`` (the manifest schema is a pure data contract).
    """

    container = "container"
    pod = "pod"
    service = "service"
    span = "span"


class FeatureValueType(str, Enum):
    """How a feature's measured value is interpreted.

    - ``ratio``     : value / baseline; semi-open band [low, high) is in
                      multiples of baseline.
    - ``absolute``  : value is a direct rate / count; band is in absolute
                      units.
    - ``boolean``   : value is 0/1; band semantics: a band that includes
                      1.0 means "fires when feature is true".
    """

    ratio = "ratio"
    absolute = "absolute"
    boolean = "boolean"


class Feature(str, Enum):
    """Canonical feature names. Manifest YAMLs reference these by string."""

    cpu_throttle_ratio = "cpu_throttle_ratio"
    memory_usage_ratio = "memory_usage_ratio"
    thread_queue_depth = "thread_queue_depth"
    gc_pause_ratio = "gc_pause_ratio"
    restart_count = "restart_count"
    latency_p99_ratio = "latency_p99_ratio"
    latency_p50_ratio = "latency_p50_ratio"
    error_rate = "error_rate"
    request_count_ratio = "request_count_ratio"
    silent = "silent"
    unavailable = "unavailable"
    dns_failure_rate = "dns_failure_rate"
    connection_refused_rate = "connection_refused_rate"
    timeout_rate = "timeout_rate"


@dataclass(frozen=True, slots=True)
class FeatureMeta:
    """Per-feature metadata.

    ``extraction_adapter`` is a free-form string identifying the IR
    adapter responsible for materialising this feature on real telemetry
    (Phase 3 will wire these). It is intentionally a string rather than a
    callable so the manifests package can be imported standalone.
    """

    feature: Feature
    kinds: frozenset[FeatureKind]
    value_type: FeatureValueType
    description: str
    extraction_adapter: str = ""


# Bootstrap vocabulary — exact set from SCHEMA.md "Feature vocabulary".
FEATURE_METADATA: Final[dict[Feature, FeatureMeta]] = {
    Feature.cpu_throttle_ratio: FeatureMeta(
        feature=Feature.cpu_throttle_ratio,
        kinds=frozenset({FeatureKind.container, FeatureKind.pod}),
        value_type=FeatureValueType.ratio,
        description="CPU throttle counter delta / baseline",
        extraction_adapter="k8s_metrics",
    ),
    Feature.memory_usage_ratio: FeatureMeta(
        feature=Feature.memory_usage_ratio,
        kinds=frozenset({FeatureKind.container, FeatureKind.pod}),
        value_type=FeatureValueType.ratio,
        description="Memory bytes / limit",
        extraction_adapter="k8s_metrics",
    ),
    Feature.thread_queue_depth: FeatureMeta(
        feature=Feature.thread_queue_depth,
        kinds=frozenset({FeatureKind.container}),
        value_type=FeatureValueType.ratio,
        description="Queue depth / baseline",
        extraction_adapter="jvm",
    ),
    Feature.gc_pause_ratio: FeatureMeta(
        feature=Feature.gc_pause_ratio,
        kinds=frozenset({FeatureKind.container}),
        value_type=FeatureValueType.ratio,
        description="GC pause time / window time",
        extraction_adapter="jvm",
    ),
    Feature.restart_count: FeatureMeta(
        feature=Feature.restart_count,
        kinds=frozenset({FeatureKind.container, FeatureKind.pod}),
        value_type=FeatureValueType.absolute,
        description="Restart events in window",
        extraction_adapter="k8s_metrics",
    ),
    Feature.latency_p99_ratio: FeatureMeta(
        feature=Feature.latency_p99_ratio,
        kinds=frozenset({FeatureKind.span}),
        value_type=FeatureValueType.ratio,
        description="P99 latency / baseline P99",
        extraction_adapter="trace",
    ),
    Feature.latency_p50_ratio: FeatureMeta(
        feature=Feature.latency_p50_ratio,
        kinds=frozenset({FeatureKind.span}),
        value_type=FeatureValueType.ratio,
        description="P50 latency / baseline P50",
        extraction_adapter="trace",
    ),
    Feature.error_rate: FeatureMeta(
        feature=Feature.error_rate,
        kinds=frozenset({FeatureKind.span}),
        value_type=FeatureValueType.absolute,
        description="Error span count / total span count, in [0, 1]",
        extraction_adapter="trace",
    ),
    Feature.request_count_ratio: FeatureMeta(
        feature=Feature.request_count_ratio,
        kinds=frozenset({FeatureKind.span}),
        value_type=FeatureValueType.ratio,
        description="Span count / baseline count",
        extraction_adapter="trace",
    ),
    Feature.silent: FeatureMeta(
        feature=Feature.silent,
        kinds=frozenset({FeatureKind.span, FeatureKind.service}),
        value_type=FeatureValueType.boolean,
        description="No spans observed when expected",
        extraction_adapter="trace_volume",
    ),
    Feature.unavailable: FeatureMeta(
        feature=Feature.unavailable,
        kinds=frozenset({FeatureKind.pod, FeatureKind.service}),
        value_type=FeatureValueType.boolean,
        description="Endpoint unreachable",
        extraction_adapter="k8s_metrics",
    ),
    Feature.dns_failure_rate: FeatureMeta(
        feature=Feature.dns_failure_rate,
        kinds=frozenset({FeatureKind.span}),
        value_type=FeatureValueType.absolute,
        description="DNS error spans / total in [0, 1]",
        extraction_adapter="trace",
    ),
    Feature.connection_refused_rate: FeatureMeta(
        feature=Feature.connection_refused_rate,
        kinds=frozenset({FeatureKind.span}),
        value_type=FeatureValueType.absolute,
        description="Refused conn rate in [0, 1]",
        extraction_adapter="trace",
    ),
    Feature.timeout_rate: FeatureMeta(
        feature=Feature.timeout_rate,
        kinds=frozenset({FeatureKind.span}),
        value_type=FeatureValueType.absolute,
        description="Timeout rate in [0, 1]",
        extraction_adapter="trace",
    ),
}


# Sanity: every Feature member has metadata.
assert set(FEATURE_METADATA.keys()) == set(Feature), "FEATURE_METADATA must cover every Feature enum member"


def is_known_feature(name: str) -> bool:
    """Return True iff ``name`` is a recognised manifest feature."""
    try:
        Feature(name)
        return True
    except ValueError:
        return False


def feature_supports_kind(feature: Feature | str, kind: FeatureKind | str) -> bool:
    """Return True iff ``feature`` is defined for the given ``kind``."""
    f = Feature(feature) if not isinstance(feature, Feature) else feature
    k = FeatureKind(kind) if not isinstance(kind, FeatureKind) else kind
    return k in FEATURE_METADATA[f].kinds


__all__ = [
    "Feature",
    "FeatureKind",
    "FeatureMeta",
    "FeatureValueType",
    "FEATURE_METADATA",
    "feature_supports_kind",
    "is_known_feature",
]
