"""InjectionAdapter: one case per fault_category family."""

from __future__ import annotations

from pathlib import Path

import pytest

from rcabench_platform.v3.internal.reasoning.ir.adapter import AdapterContext
from rcabench_platform.v3.internal.reasoning.ir.adapters.injection import InjectionAdapter
from rcabench_platform.v3.internal.reasoning.ir.evidence import EvidenceLevel
from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import ResolvedInjection

CTX = AdapterContext(datapack_dir=Path("/tmp/not-used"), case_name="fixture")


def _resolve(
    nodes: list[str],
    start_kind: str,
    category: str,
    fault_category: str,
    fault_type_name: str,
) -> ResolvedInjection:
    return ResolvedInjection(
        injection_nodes=nodes,
        start_kind=start_kind,
        category=category,
        fault_category=fault_category,
        fault_type_name=fault_type_name,
        resolution_method="test",
    )


def _emit_single(resolved: ResolvedInjection, at: int = 100) -> list:
    events = list(InjectionAdapter(resolved, injection_at=at).emit(CTX))
    return events


@pytest.mark.parametrize(
    "fault_type_name,expected_state",
    [
        ("HTTPResponseAbort", "erroring"),
        ("HTTPResponseReplaceCode", "erroring"),
        ("HTTPResponseDelay", "slow"),
    ],
)
def test_http_response(fault_type_name: str, expected_state: str) -> None:
    r = _resolve(
        ["span|GET /api/v1/foo"],
        "span",
        "http_span",
        "http_response",
        fault_type_name,
    )
    events = _emit_single(r)
    assert len(events) == 1
    t = events[0]
    assert t.kind == PlaceKind.span
    assert t.to_state == expected_state
    assert t.level == EvidenceLevel.structural
    assert t.trigger == f"fault:{fault_type_name}"
    assert t.evidence.get("specialization_labels") == frozenset({fault_type_name})


@pytest.mark.parametrize(
    "fault_type_name,expected_state",
    [
        ("HTTPRequestAbort", "erroring"),
        ("HTTPRequestDelay", "slow"),
    ],
)
def test_http_request(fault_type_name: str, expected_state: str) -> None:
    r = _resolve(["span|POST /bar"], "span", "http_span", "http_request", fault_type_name)
    events = _emit_single(r)
    assert len(events) == 1
    assert events[0].to_state == expected_state


def test_container_stress() -> None:
    r = _resolve(
        ["container|ts-order-service"],
        "container",
        "container_resource",
        "container",
        "CPUStress",
    )
    events = _emit_single(r)
    assert len(events) == 1
    assert events[0].kind == PlaceKind.container
    assert events[0].to_state == "degraded"


def test_pod_lifecycle_pod_kill() -> None:
    r = _resolve(["pod|ts-order-abc123"], "pod", "pod_lifecycle", "pod", "PodKill")
    events = _emit_single(r)
    assert events[0].kind == PlaceKind.pod
    assert events[0].to_state == "unavailable"


def test_pod_lifecycle_container_kill_resolves_to_container() -> None:
    r = _resolve(
        ["container|ts-order-service"],
        "container",
        "pod_lifecycle",
        "pod",
        "ContainerKill",
    )
    events = _emit_single(r)
    assert events[0].kind == PlaceKind.container
    assert events[0].to_state == "unavailable"


@pytest.mark.parametrize(
    "fault_type_name,expected_state",
    [("JVMException", "erroring"), ("JVMLatency", "slow")],
)
def test_jvm_method(fault_type_name: str, expected_state: str) -> None:
    r = _resolve(
        ["container|ts-assurance-service"],
        "container",
        "jvm_method",
        "jvm",
        fault_type_name,
    )
    events = _emit_single(r)
    assert events[0].to_state == expected_state


@pytest.mark.parametrize(
    "fault_type_name,expected_state",
    [("JVMMySQLLatency", "slow"), ("JVMMySQLException", "erroring")],
)
def test_jvm_database(fault_type_name: str, expected_state: str) -> None:
    r = _resolve(
        ["span|SELECT ts.trip2"],
        "span",
        "jvm_database",
        "jvm_database",
        fault_type_name,
    )
    events = _emit_single(r)
    assert events[0].to_state == expected_state


def test_network_delay_slow() -> None:
    """Phase 6: NetworkDelay is a latency-style fault → ``slow``.

    Pre-Phase-6 the fault_category-based logic collapsed every non-partition
    network fault to ``degraded``; the chaos-tool contract is finer-grained.
    """
    r = _resolve(["service|ts-order"], "service", "network", "network", "NetworkDelay")
    events = _emit_single(r)
    assert events[0].kind == PlaceKind.service
    assert events[0].to_state == "slow"


def test_network_loss_degraded() -> None:
    """Network loss / corrupt / duplicate stay at ``degraded``."""
    r = _resolve(["service|ts-order"], "service", "network", "network", "NetworkLoss")
    events = _emit_single(r)
    assert events[0].to_state == "degraded"


def test_network_partition_unavailable() -> None:
    r = _resolve(["service|ts-order"], "service", "network", "network", "NetworkPartition")
    events = _emit_single(r)
    assert events[0].to_state == "unavailable"


def test_dns_erroring() -> None:
    r = _resolve(["service|ts-order"], "service", "dns", "dns", "DNSError")
    events = _emit_single(r)
    assert events[0].to_state == "erroring"


def test_time_skew_degraded() -> None:
    r = _resolve(["pod|ts-order-abc"], "pod", "time", "time", "TimeSkew")
    events = _emit_single(r)
    assert events[0].kind == PlaceKind.pod
    assert events[0].to_state == "degraded"


def test_unknown_fault_type_emits_default_degraded_seed(caplog: pytest.LogCaptureFixture) -> None:
    """Phase 6: unknown fault types must NOT silently emit nothing.

    They should warn-log and seed the documented default tier so
    propagation always has a starting point.
    """
    r = _resolve(["service|ts-order"], "service", "weird", "weird", "UnknownFault")
    with caplog.at_level("WARNING"):
        events = _emit_single(r)
    assert len(events) == 1
    t = events[0]
    assert t.kind == PlaceKind.service
    # service.degraded is a real ServiceStateIR member -> stays "degraded".
    assert t.to_state == "degraded"
    assert t.level == EvidenceLevel.structural
    assert t.trigger == "fault:UnknownFault"
    assert any("unknown fault_type_name" in rec.message for rec in caplog.records)


def test_unknown_start_kind_emits_nothing() -> None:
    r = _resolve(["function|handler"], "function", "dns", "dns", "DNSError")
    events = _emit_single(r)
    assert events == []


def test_multiple_injection_nodes_each_get_seed() -> None:
    r = _resolve(
        ["span|GET /a", "span|GET /b"],
        "span",
        "http_span",
        "http_response",
        "HTTPResponseAbort",
    )
    events = _emit_single(r)
    assert len(events) == 2
    assert {e.node_key for e in events} == {"span|GET /a", "span|GET /b"}
