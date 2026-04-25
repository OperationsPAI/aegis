"""StateAdapter protocol + module-level registry.

Adapters are signature-driven and stateful: they own their own windowing,
hysteresis, and threshold logic. The IR core only consumes ``Transition``
events and never inspects raw metrics/traces.

The registry is a simple module-level dict populated by the
``@register_adapter`` decorator. Phase 3 wires it into ``run_synth``;
tests can bypass it by importing adapter classes directly.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
from pathlib import Path
from typing import Protocol, runtime_checkable

from rcabench_platform.v3.internal.reasoning.ir.transition import Transition


@dataclass(frozen=True, slots=True)
class AdapterContext:
    """Inputs available to every adapter.

    Intentionally minimal in Phase 1 — adapters that need a HyperGraph,
    parquet loader, or baselines declare those in their own constructor
    and receive them from the Phase 3 wiring layer, not via this context.
    """

    datapack_dir: Path
    case_name: str


@runtime_checkable
class StateAdapter(Protocol):
    name: str

    def emit(self, ctx: AdapterContext) -> Iterable[Transition]: ...


_REGISTRY: dict[str, type[StateAdapter]] = {}


def register_adapter(cls: type[StateAdapter]) -> type[StateAdapter]:
    """Decorator: add adapter class to the module-level registry.

    Duplicate registration raises — re-registering silently masks bugs
    where two adapters pick the same ``name``.
    """
    name = getattr(cls, "name", None)
    if not name:
        raise ValueError(f"{cls.__name__} must define class attribute 'name'")
    if name in _REGISTRY:
        raise ValueError(f"adapter name '{name}' already registered by {_REGISTRY[name].__name__}")
    _REGISTRY[name] = cls
    return cls


def get_registered_adapters() -> dict[str, type[StateAdapter]]:
    return dict(_REGISTRY)


def _clear_registry_for_tests() -> None:
    _REGISTRY.clear()
