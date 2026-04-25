"""Phase 6 of #163: deterministic fault_type → canonical seed-state map.

These tests pin the contract between the chaos-tool fault catalog
(``models/injection.FAULT_TYPES``) and the canonical seed tier emitted by
``InjectionAdapter`` for it. Adding a new fault type to ``FAULT_TYPES``
without an entry in ``FAULT_TYPE_TO_SEED_TIER`` MUST fail
``test_every_fault_type_in_catalog_is_mapped``.
"""

from __future__ import annotations

import pytest

from rcabench_platform.v3.internal.reasoning.ir.states import (
    ContainerStateIR,
    PodStateIR,
    ServiceStateIR,
    SpanStateIR,
)
from rcabench_platform.v3.internal.reasoning.models.fault_seed import (
    FAULT_TYPE_TO_SEED_TIER,
    SEED_TIERS,
    UNKNOWN_FAULT_DEFAULT_TIER,
    canonical_seed_tier,
    pick_canonical_state,
)
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import FAULT_TYPES


def test_every_fault_type_in_catalog_is_mapped() -> None:
    """Every chaos-tool fault_type MUST have a deterministic seed tier."""
    missing = [name for name in FAULT_TYPES if name not in FAULT_TYPE_TO_SEED_TIER]
    assert missing == [], f"chaos-tool fault types without canonical seed mapping: {missing}"


def test_every_mapped_tier_is_a_valid_canonical_tier() -> None:
    """Mapping values are all in the closed canonical tier set."""
    for name, tier in FAULT_TYPE_TO_SEED_TIER.items():
        assert tier in SEED_TIERS, f"{name}: tier {tier!r} not in {SEED_TIERS}"


def test_default_tier_for_unknown_is_in_canonical_set() -> None:
    assert UNKNOWN_FAULT_DEFAULT_TIER in SEED_TIERS


@pytest.mark.parametrize("fault_type_name", list(FAULT_TYPES))
def test_canonical_seed_tier_known(fault_type_name: str) -> None:
    """Known fault types resolve to a non-None tier and ``is_known=True``."""
    tier, is_known = canonical_seed_tier(fault_type_name)
    assert is_known is True
    assert tier in SEED_TIERS


def test_canonical_seed_tier_unknown_returns_default_with_warning_flag() -> None:
    tier, is_known = canonical_seed_tier("MadeUpFutureFault")
    assert is_known is False
    assert tier == UNKNOWN_FAULT_DEFAULT_TIER


# Spot-check a handful of explicit tier assignments so that an accidental
# regression (e.g. PodKill → degraded) is caught even if the table-driven
# tests above pass.
@pytest.mark.parametrize(
    "fault_type_name,expected_tier",
    [
        ("PodKill", "unavailable"),
        ("PodFailure", "unavailable"),
        ("ContainerKill", "unavailable"),
        ("CPUStress", "degraded"),
        ("MemoryStress", "degraded"),
        ("HTTPRequestAbort", "erroring"),
        ("HTTPResponseAbort", "erroring"),
        ("HTTPRequestDelay", "slow"),
        ("HTTPResponseDelay", "slow"),
        ("DNSError", "erroring"),
        ("DNSRandom", "erroring"),
        ("TimeSkew", "degraded"),
        ("NetworkPartition", "unavailable"),
        ("NetworkDelay", "slow"),
        ("NetworkLoss", "degraded"),
        ("NetworkBandwidth", "slow"),
        ("JVMException", "erroring"),
        ("JVMLatency", "slow"),
        ("JVMGarbageCollector", "slow"),
        ("JVMMySQLLatency", "slow"),
        ("JVMMySQLException", "erroring"),
    ],
)
def test_explicit_tier_assignments(fault_type_name: str, expected_tier: str) -> None:
    tier, is_known = canonical_seed_tier(fault_type_name)
    assert is_known is True
    assert tier == expected_tier


# pick_canonical_state: kind-aware resolution.
@pytest.mark.parametrize(
    "kind,tier,expected",
    [
        # span has no DEGRADED — collapses to SLOW
        (PlaceKind.span, "degraded", str(SpanStateIR.SLOW.value)),
        (PlaceKind.span, "slow", str(SpanStateIR.SLOW.value)),
        (PlaceKind.span, "erroring", str(SpanStateIR.ERRORING.value)),
        (PlaceKind.span, "unavailable", str(SpanStateIR.UNAVAILABLE.value)),
        # service has DEGRADED
        (PlaceKind.service, "degraded", str(ServiceStateIR.DEGRADED.value)),
        (PlaceKind.service, "slow", str(ServiceStateIR.SLOW.value)),
        (PlaceKind.service, "erroring", str(ServiceStateIR.ERRORING.value)),
        (PlaceKind.service, "unavailable", str(ServiceStateIR.UNAVAILABLE.value)),
        # pod has DEGRADED but no SLOW
        (PlaceKind.pod, "degraded", str(PodStateIR.DEGRADED.value)),
        (PlaceKind.pod, "erroring", str(PodStateIR.ERRORING.value)),
        (PlaceKind.pod, "unavailable", str(PodStateIR.UNAVAILABLE.value)),
        # container has DEGRADED but no SLOW
        (PlaceKind.container, "degraded", str(ContainerStateIR.DEGRADED.value)),
        (PlaceKind.container, "erroring", str(ContainerStateIR.ERRORING.value)),
        (PlaceKind.container, "unavailable", str(ContainerStateIR.UNAVAILABLE.value)),
    ],
)
def test_pick_canonical_state(kind: PlaceKind, tier: str, expected: str) -> None:
    assert pick_canonical_state(kind, tier) == expected
