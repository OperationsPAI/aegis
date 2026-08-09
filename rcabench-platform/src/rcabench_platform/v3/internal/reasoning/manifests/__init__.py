"""Fault-type-conditioned manifest runtime (FORGE rework, Phase 1).

This package implements the manifest *plumbing*: schema, loader,
registry, and runtime carrier. The actual manifest-aware verification
gates live under ``algorithms.gates`` (stubs in Phase 1, populated in
Phase 3). Manifest YAMLs themselves live under ``fault_types/`` and are
authored by Phase 2 agents.
"""

from rcabench_platform.v3.internal.reasoning.manifests.context import ReasoningContext
from rcabench_platform.v3.internal.reasoning.manifests.features import (
    FEATURE_METADATA,
    Feature,
    FeatureKind,
    FeatureMeta,
    FeatureValueType,
    feature_supports_kind,
    is_known_feature,
)
from rcabench_platform.v3.internal.reasoning.manifests.loader import (
    ManifestLoadError,
    load_manifest,
)
from rcabench_platform.v3.internal.reasoning.manifests.registry import (
    ManifestRegistry,
    get_default_registry,
    set_default_registry,
)
from rcabench_platform.v3.internal.reasoning.manifests.schema import (
    DerivationLayer,
    EntrySignature,
    FaultManifest,
    FeatureMatch,
    HandOff,
    HandOffTrigger,
)

__all__ = [
    "DerivationLayer",
    "EntrySignature",
    "FEATURE_METADATA",
    "FaultManifest",
    "Feature",
    "FeatureKind",
    "FeatureMatch",
    "FeatureMeta",
    "FeatureValueType",
    "HandOff",
    "HandOffTrigger",
    "ManifestLoadError",
    "ManifestRegistry",
    "ReasoningContext",
    "feature_supports_kind",
    "get_default_registry",
    "is_known_feature",
    "load_manifest",
    "set_default_registry",
]
