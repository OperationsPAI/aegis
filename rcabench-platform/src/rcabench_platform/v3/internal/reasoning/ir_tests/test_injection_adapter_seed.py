"""Phase 6: feed every fault_type into InjectionAdapter and verify the
emitted seed Transition matches the canonical mapping.

These tests are the end-to-end contract: chaos-tool fault_type → seed
state on the IR. They make the mapping discoverable from the consumer
side (the InjectionAdapter) so a regression to the seed pipeline can't
hide behind passing unit tests on ``fault_seed`` alone.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.injection import InjectionAdapter
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.models.fault_seed import (
    FAULT_TYPE_TO_SEED_TIER,
    UNKNOWN_FAULT_DEFAULT_TIER,
    pick_canonical_state,
)
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import ResolvedInjection

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="phase6")


# fault_type → typical (start_kind, fault_category) pairing the resolver
# emits. We use one representative tuple per fault_type so the test exercises
# the *seed* enforcement, not resolver internals.
_FAULT_DEFAULT_KIND_AND_CATEGORY: dict[str, tuple[str, str]] = {
    "PodKill": ("pod", "pod"),
    "PodFailure": ("pod", "pod"),
    "ContainerKill": ("container", "pod"),
    "MemoryStress": ("container", "container"),
    "CPUStress": ("container", "container"),
    "HTTPRequestAbort": ("span", "http_request"),
    "HTTPRequestDelay": ("span", "http_request"),
    "HTTPRequestReplacePath": ("span", "http_request"),
    "HTTPRequestReplaceMethod": ("span", "http_request"),
    "HTTPResponseAbort": ("span", "http_response"),
    "HTTPResponseDelay": ("span", "http_response"),
    "HTTPResponseReplaceBody": ("span", "http_response"),
    "HTTPResponsePatchBody": ("span", "http_response"),
    "HTTPResponseReplaceCode": ("span", "http_response"),
    "DNSError": ("service", "dns"),
    "DNSRandom": ("service", "dns"),
    "TimeSkew": ("pod", "time"),
    "NetworkDelay": ("service", "network"),
    "NetworkLoss": ("service", "network"),
    "NetworkDuplicate": ("service", "network"),
    "NetworkCorrupt": ("service", "network"),
    "NetworkBandwidth": ("service", "network"),
    "NetworkPartition": ("service", "network"),
    "JVMLatency": ("span", "jvm"),
    "JVMReturn": ("span", "jvm"),
    "JVMException": ("span", "jvm"),
    "JVMGarbageCollector": ("span", "jvm"),
    "JVMCPUStress": ("container", "jvm"),
    "JVMMemoryStress": ("container", "jvm"),
    "JVMMySQLLatency": ("span", "jvm_database"),
    "JVMMySQLException": ("span", "jvm_database"),
}


def _resolve(
    nodes: list[str],
    start_kind: str,
    fault_category: str,
    fault_type_name: str,
) -> ResolvedInjection:
    return ResolvedInjection(
        injection_nodes=nodes,
        start_kind=start_kind,
        category=fault_category,  # exact category granularity isn't used downstream
        fault_category=fault_category,
        fault_type_name=fault_type_name,
        resolution_method="phase6-test",
    )


@pytest.mark.parametrize("fault_type_name", list(FAULT_TYPE_TO_SEED_TIER.keys()))
def test_seed_for_every_known_fault_type(fault_type_name: str) -> None:
    """Every chaos-tool fault_type emits exactly one seed Transition with
    the canonical state tier from the mapping."""
    start_kind, fault_category = _FAULT_DEFAULT_KIND_AND_CATEGORY[fault_type_name]
    node_key = f"{start_kind}|fixture-{fault_type_name}"
    r = _resolve([node_key], start_kind, fault_category, fault_type_name)

    events = list(InjectionAdapter(r, injection_at=42).emit(CTX))
    assert len(events) == 1, f"{fault_type_name}: expected exactly 1 seed transition"

    t = events[0]
    place_kind = {
        "span": PlaceKind.span,
        "service": PlaceKind.service,
        "pod": PlaceKind.pod,
        "container": PlaceKind.container,
    }[start_kind]

    expected_tier = FAULT_TYPE_TO_SEED_TIER[fault_type_name]
    expected_state = pick_canonical_state(place_kind, expected_tier)

    assert t.kind is place_kind
    assert t.to_state == expected_state, (
        f"{fault_type_name}: expected to_state={expected_state} "
        f"(tier={expected_tier} on kind={place_kind.name}), got {t.to_state}"
    )
    assert t.level == EvidenceLevel.structural
    assert t.from_state == "unknown"
    assert t.trigger == f"fault:{fault_type_name}"
    assert t.evidence.get("specialization_labels") == frozenset({fault_type_name})
    assert t.at == 42
    assert t.node_key == node_key


def test_unknown_fault_type_falls_back_to_default_tier(caplog: pytest.LogCaptureFixture) -> None:
    """Unknown fault types must seed the default tier and log a warning."""
    r = _resolve(
        ["service|something-internal"],
        "service",
        "weird-category",
        "BrandNewChaosFutureFault",
    )
    with caplog.at_level("WARNING"):
        events = list(InjectionAdapter(r, injection_at=99).emit(CTX))

    assert len(events) == 1, "unknown fault_type must still seed (Phase 6)"
    t = events[0]
    expected = pick_canonical_state(PlaceKind.service, UNKNOWN_FAULT_DEFAULT_TIER)
    assert t.to_state == expected
    assert t.kind is PlaceKind.service
    # The warning must mention the fault name so it is grep-able.
    assert any(
        "BrandNewChaosFutureFault" in rec.message and "unknown fault_type_name" in rec.message for rec in caplog.records
    ), f"expected unknown-fault warning, got: {[r.message for r in caplog.records]}"


def test_unknown_start_kind_still_emits_nothing() -> None:
    """Unrecognised start_kind has no canonical PlaceKind -> can't seed."""
    r = _resolve(["function|handler"], "function", "dns", "DNSError")
    events = list(InjectionAdapter(r, injection_at=0).emit(CTX))
    assert events == []


def test_multiple_injection_nodes_each_get_canonical_seed() -> None:
    r = _resolve(
        ["span|GET /a", "span|GET /b"],
        "span",
        "http_response",
        "HTTPResponseAbort",
    )
    events = list(InjectionAdapter(r, injection_at=7).emit(CTX))
    assert len(events) == 2
    assert {e.node_key for e in events} == {"span|GET /a", "span|GET /b"}
    for e in events:
        assert e.to_state == pick_canonical_state(PlaceKind.span, "erroring")
