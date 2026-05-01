"""ReasoningContext — runtime carrier for the active fault manifest.

A minimal dataclass exposed by the IR runner to downstream consumers
(Phase 3 ``ManifestEntryGate`` / ``ManifestLayerGate``). Phase 1 only
populates :attr:`manifest` and :attr:`fault_type_name`; later phases
extend with whatever extra runtime state the gates need.

Design choice: keep this struct tiny and additive rather than retrofit
an existing context object. The IR pipeline currently has no end-to-end
"reasoning context" abstraction (the closest is :class:`AdapterContext`
in ``ir/adapter.py``, scoped to the IR builder phase). Rather than
overload that, we introduce ``ReasoningContext`` here so the manifest
package owns its own contract; Phase 3 wires the existing pipeline to
construct one and pass it into the new gates.
"""

from __future__ import annotations

from dataclasses import dataclass

from rcabench_platform.v3.internal.reasoning.manifests.schema import FaultManifest


@dataclass(frozen=True, slots=True)
class ReasoningContext:
    """Per-case manifest binding handed to manifest-aware components.

    Both fields are nullable: missing ``manifest`` means "fall back to
    generic rules"; missing ``fault_type_name`` means the IR runner
    could not resolve a fault type at all (a sham / null injection
    case).
    """

    fault_type_name: str | None = None
    manifest: FaultManifest | None = None


__all__ = ["ReasoningContext"]
