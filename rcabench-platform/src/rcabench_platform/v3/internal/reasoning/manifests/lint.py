"""Manifest linter — ``python -m ...manifests.lint <dir>``.

Exit codes:
- 0: every YAML in <dir> loaded and cross-validated cleanly.
- 1: at least one error.
- 2: usage error (no directory argument, or directory does not exist).

Per-file errors are reported as ``<path>: <detail>``; cross-document
errors (rules 3 / 5) are reported with the offending manifest name in
angle brackets.
"""

from __future__ import annotations

import sys
from pathlib import Path

from rcabench_platform.v3.internal.reasoning.manifests.loader import (
    ManifestLoadError,
    load_manifest,
)
from rcabench_platform.v3.internal.reasoning.manifests.registry import (
    ManifestRegistry,
)


def _print_err(msg: str) -> None:
    print(msg, file=sys.stderr)


def main(argv: list[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    if len(args) != 1:
        _print_err(
            "usage: python -m rcabench_platform.v3.internal.reasoning.manifests.lint "
            "<manifest-dir>"
        )
        return 2
    target = Path(args[0])
    if not target.exists() or not target.is_dir():
        _print_err(f"error: {target} is not a directory")
        return 2

    yaml_paths = sorted(target.glob("*.yaml"))
    if not yaml_paths:
        _print_err(f"warning: no *.yaml files found under {target}")
        return 0

    errors = 0
    valid: dict[str, object] = {}
    for path in yaml_paths:
        try:
            m = load_manifest(path)
        except ManifestLoadError as e:
            _print_err(f"{e.path}: {e.detail}")
            errors += 1
            continue
        if m.fault_type_name in valid:
            _print_err(
                f"{path}: duplicate manifest for {m.fault_type_name!r}"
            )
            errors += 1
            continue
        valid[m.fault_type_name] = m

    # Cross-validate the successfully loaded subset (rules 3, 5).
    if valid:
        registry = ManifestRegistry({k: v for k, v in valid.items()})  # type: ignore[arg-type]
        try:
            registry.cross_validate()
        except ManifestLoadError as e:
            _print_err(f"{e.path}: {e.detail}")
            errors += 1

    if errors:
        _print_err(f"\n{errors} error(s) across {len(yaml_paths)} file(s)")
        return 1
    print(f"OK: {len(yaml_paths)} manifest(s) validated")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
