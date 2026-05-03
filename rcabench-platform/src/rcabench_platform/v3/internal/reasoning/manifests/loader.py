"""YAML → :class:`FaultManifest` loader.

Single public entry point :func:`load_manifest` that reads a YAML file
and returns a validated :class:`FaultManifest`. The 8 schema-level
validation rules are split between :mod:`schema` (rules 1, 2, 4, 6, 7,
8 — pure model construction) and :mod:`registry` (rules 3, 5 —
cross-document). This loader covers the file-reading + YAML-parsing
boilerplate and surfaces a uniform :class:`ManifestLoadError`.

Validation rule 7 (entry_window_sec ≤ 60) is enforced inline by the
``EntrySignature.entry_window_sec`` Field constraint.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml
from pydantic import ValidationError

from rcabench_platform.v3.internal.reasoning.manifests.schema import FaultManifest


class ManifestLoadError(Exception):
    """Raised when a manifest YAML fails to load or validate.

    The :attr:`path` and :attr:`detail` attributes are kept structured so
    the lint CLI can format diagnostics cleanly.
    """

    def __init__(self, path: Path, detail: str, cause: Exception | None = None):
        self.path = path
        self.detail = detail
        self.__cause__ = cause
        super().__init__(f"{path}: {detail}")


def _load_yaml(path: Path) -> Any:
    try:
        with path.open("r", encoding="utf-8") as f:
            return yaml.safe_load(f)
    except FileNotFoundError as e:
        raise ManifestLoadError(path, "file not found", e) from e
    except yaml.YAMLError as e:
        raise ManifestLoadError(path, f"YAML parse error: {e}", e) from e


def load_manifest(path: Path) -> FaultManifest:
    """Load and validate a single fault manifest YAML file.

    Raises :class:`ManifestLoadError` on any failure: missing file,
    YAML parse error, or schema validation failure.
    """

    raw = _load_yaml(path)
    if not isinstance(raw, dict):
        raise ManifestLoadError(
            path,
            f"top-level YAML must be a mapping; got {type(raw).__name__}",
        )

    try:
        return FaultManifest.model_validate(raw)
    except ValidationError as e:
        # Render the first error with its location for actionable output.
        first = e.errors()[0]
        loc = ".".join(str(p) for p in first["loc"])
        msg = first["msg"]
        raise ManifestLoadError(path, f"{loc}: {msg}", e) from e


__all__ = [
    "ManifestLoadError",
    "load_manifest",
]
