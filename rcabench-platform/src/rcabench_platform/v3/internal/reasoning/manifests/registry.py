"""Manifest registry — singleton lookup table over all loaded manifests.

A registry is built once per process from a directory of YAMLs (see
``manifests/fault_types/``). Phase 3 consumers call :meth:`get` to look
up the manifest for the active injection's ``fault_type_name``; a
``None`` return is the documented signal "no manifest exists, fall back
to the generic 18-rule path".

Cross-document validation rules:

- **Rule 3** (target_kind cross-check with ``injection.py``): we map
  each fault type's ``FAULT_TYPE_CATEGORIES`` entry to the expected
  target_kind and warn / error if the manifest disagrees. Because the
  category → target_kind mapping has multiple legitimate outcomes
  (e.g. network faults can manifest at either ``service`` or ``pod``
  scope depending on direction), we permit any kind in the allowed set.
- **Rule 5** (hand-off targets resolve): every ``HandOff.to`` must name
  a fault_type that has its own loaded manifest.
"""

from __future__ import annotations

import logging
from pathlib import Path
from typing import Final

from rcabench_platform.v3.internal.reasoning.manifests.loader import (
    ManifestLoadError,
    load_manifest,
)
from rcabench_platform.v3.internal.reasoning.manifests.schema import (
    FaultManifest,
    TargetKind,
)
from rcabench_platform.v3.internal.reasoning.models.injection import (
    FAULT_TYPE_CATEGORIES,
    FAULT_TYPES,
)

logger = logging.getLogger(__name__)


# Map FAULT_TYPE_CATEGORIES coarse category → set of legitimate target_kinds.
# Multiple kinds are allowed for categories that physically project onto more
# than one node level (a network partition is enforced at pod/service scope;
# a JVM method fault may surface at span or container).
_CATEGORY_TO_TARGET_KINDS: Final[dict[str, frozenset[TargetKind]]] = {
    "pod_lifecycle": frozenset({"pod", "container"}),
    "container_resource": frozenset({"container", "pod"}),
    "http_span": frozenset({"span", "service"}),
    "dns": frozenset({"pod", "service", "span"}),
    "time": frozenset({"pod", "container"}),
    "network": frozenset({"service", "pod"}),
    "jvm_method": frozenset({"span", "container"}),
    "jvm_database": frozenset({"span", "service", "container"}),
}


def _expected_target_kinds(fault_type_name: str) -> frozenset[TargetKind] | None:
    try:
        idx = FAULT_TYPES.index(fault_type_name)
    except ValueError:
        return None
    category = FAULT_TYPE_CATEGORIES.get(idx)
    if category is None:
        return None
    return _CATEGORY_TO_TARGET_KINDS.get(category)


class ManifestRegistry:
    """Look-up table from ``fault_type_name`` → :class:`FaultManifest`.

    The registry holds *only* successfully validated manifests. Callers
    that hit a missing entry receive ``None`` and are expected to fall
    back to the generic-rules pipeline.
    """

    def __init__(self, manifests: dict[str, FaultManifest]):
        self._manifests: dict[str, FaultManifest] = dict(manifests)

    @classmethod
    def from_directory(
        cls,
        path: Path,
        *,
        strict: bool = True,
    ) -> ManifestRegistry:
        """Load every ``*.yaml`` under ``path`` into a registry.

        ``strict=True`` (default) re-raises the first
        :class:`ManifestLoadError` encountered. ``strict=False`` collects
        errors and skips bad files — used by the lint CLI which wants to
        report all failures, not just the first.
        """
        manifests: dict[str, FaultManifest] = {}
        if not path.exists():
            logger.info("manifest dir %s does not exist; registry empty", path)
            return cls(manifests)

        yaml_files = sorted(path.glob("*.yaml"))
        for yaml_path in yaml_files:
            try:
                m = load_manifest(yaml_path)
            except ManifestLoadError:
                if strict:
                    raise
                continue
            if m.fault_type_name in manifests:
                detail = f"duplicate manifest for {m.fault_type_name!r}: already loaded from another file"
                if strict:
                    raise ManifestLoadError(yaml_path, detail)
                logger.error("%s: %s", yaml_path, detail)
                continue
            manifests[m.fault_type_name] = m

        registry = cls(manifests)
        if strict:
            registry.cross_validate()
        return registry

    def get(self, fault_type_name: str) -> FaultManifest | None:
        """Return the manifest for ``fault_type_name`` or ``None``.

        ``None`` is the documented "fall back to generic rules" signal.
        Callers MUST treat it as a soft miss, not an error.
        """
        return self._manifests.get(fault_type_name)

    def names(self) -> list[str]:
        return sorted(self._manifests.keys())

    def __len__(self) -> int:
        return len(self._manifests)

    def __contains__(self, fault_type_name: object) -> bool:
        return isinstance(fault_type_name, str) and fault_type_name in self._manifests

    # ------------------------------------------------------------------
    # Cross-document validation
    # ------------------------------------------------------------------

    def cross_validate(self) -> None:
        """Run validation rules 3 and 5 across all loaded manifests.

        Raises :class:`ManifestLoadError` on the first failure (with
        ``path = Path("<registry>")`` because the offending fact is
        cross-document, not file-local).
        """

        for name, manifest in self._manifests.items():
            # Rule 3: target_kind cross-check.
            allowed = _expected_target_kinds(name)
            if allowed is not None and manifest.target_kind not in allowed:
                raise ManifestLoadError(
                    Path(f"<registry:{name}>"),
                    f"target_kind {manifest.target_kind!r} disagrees with "
                    f"injection.py category mapping; allowed: "
                    f"{sorted(allowed)}",
                )

            # Rule 5: every hand_off.to must resolve.
            for h in manifest.hand_offs:
                if h.to not in self._manifests:
                    raise ManifestLoadError(
                        Path(f"<registry:{name}>"),
                        f"hand_off target {h.to!r} has no loaded manifest",
                    )


# ---------------------------------------------------------------------------
# Process-wide singleton
# ---------------------------------------------------------------------------

_DEFAULT_REGISTRY: ManifestRegistry | None = None


def set_default_registry(registry: ManifestRegistry) -> None:
    """Install ``registry`` as the process-wide default.

    Called once by ``cli.py`` after parsing ``--manifest-dir``.
    """
    global _DEFAULT_REGISTRY
    _DEFAULT_REGISTRY = registry


def get_default_registry() -> ManifestRegistry:
    """Return the process-wide registry, lazy-loading the bundled manifests.

    When ``set_default_registry`` has not been called explicitly, this
    falls back to loading the package-shipped
    ``manifests/fault_types/`` directory (the same default
    ``cli._init_manifest_registry`` uses). This makes batch drivers that
    invoke :func:`run_single_case` directly, without going through the
    ``forge run`` CLI entry point (e.g. ablation drivers under
    ``bin/paper_artifacts``), get manifest-aware behaviour by default
    rather than silently falling through to generic rules.

    Tests and CLI commands that need an explicitly empty registry must
    call ``set_default_registry(ManifestRegistry({}))`` before any
    consumer hits this function.
    """
    global _DEFAULT_REGISTRY
    if _DEFAULT_REGISTRY is None:
        # Package default: rcabench_platform/v3/internal/reasoning/manifests/fault_types
        default_dir = Path(__file__).resolve().parent / "fault_types"
        if default_dir.exists():
            try:
                _DEFAULT_REGISTRY = ManifestRegistry.from_directory(default_dir, strict=True)
                logger.info(
                    "lazy-loaded %d manifest(s) from default dir %s",
                    len(_DEFAULT_REGISTRY),
                    default_dir,
                )
            except ManifestLoadError:
                # Don't crash on bad manifests at lazy-init time; tests
                # may be running an in-tree subset. Fall back to empty.
                logger.warning(
                    "failed to lazy-load default manifest registry from %s; using empty registry",
                    default_dir,
                )
                _DEFAULT_REGISTRY = ManifestRegistry({})
        else:
            _DEFAULT_REGISTRY = ManifestRegistry({})
    return _DEFAULT_REGISTRY


__all__ = [
    "ManifestRegistry",
    "get_default_registry",
    "set_default_registry",
]
