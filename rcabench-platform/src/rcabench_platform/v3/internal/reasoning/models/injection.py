"""Injection metadata parsing and node resolution.

This module provides tools to parse injection.json and resolve the true injection
point based on fault type. Different fault types require different granularity:
- HTTP faults: resolve to specific span by route matching
- Container faults: resolve to container node
- Network faults: resolve based on direction
- JVM faults: resolve via mapping file (to be provided)
"""

from __future__ import annotations

import json
import logging
import re
from typing import TYPE_CHECKING

from pydantic import BaseModel, Field

if TYPE_CHECKING:
    from rcabench_platform.v3.internal.reasoning.models.graph import HyperGraph, Node

from rcabench_platform.v3.internal.reasoning.models.graph import PlaceKind
from rcabench_platform.v3.internal.reasoning.models.system_adapters import (
    EXTERNAL_SERVICE_NAMES,
    service_name_matches,
)

logger = logging.getLogger(__name__)


# Fault type names for reference
FAULT_TYPES: list[str] = [
    "PodKill",  # 0
    "PodFailure",  # 1
    "ContainerKill",  # 2
    "MemoryStress",  # 3
    "CPUStress",  # 4
    "HTTPRequestAbort",  # 5
    "HTTPResponseAbort",  # 6
    "HTTPRequestDelay",  # 7
    "HTTPResponseDelay",  # 8
    "HTTPResponseReplaceBody",  # 9
    "HTTPResponsePatchBody",  # 10
    "HTTPRequestReplacePath",  # 11
    "HTTPRequestReplaceMethod",  # 12
    "HTTPResponseReplaceCode",  # 13
    "DNSError",  # 14
    "DNSRandom",  # 15
    "TimeSkew",  # 16
    "NetworkDelay",  # 17
    "NetworkLoss",  # 18
    "NetworkDuplicate",  # 19
    "NetworkCorrupt",  # 20
    "NetworkBandwidth",  # 21
    "NetworkPartition",  # 22
    "JVMLatency",  # 23
    "JVMReturn",  # 24
    "JVMException",  # 25
    "JVMGarbageCollector",  # 26
    "JVMCPUStress",  # 27
    "JVMMemoryStress",  # 28
    "JVMMySQLLatency",  # 29
    "JVMMySQLException",  # 30
    "JVMRuntimeMutator",  # 31
]


# Fault categories that require injection point state enhancement.
# For these fault types, the injection point service's spans may not show detectable
# anomalies in metrics. State enhancement explicitly marks them so propagation rules
# can work correctly.
INJECTION_POINT_ENHANCEMENT_CATEGORIES: set[str] = {
    "container_resource",  # CPU/Memory stress - effects may not appear in spans
    "pod_lifecycle",  # Pod lifecycle faults - may cause silent failures
    "network",  # NetworkDelay, NetworkCorrupt, etc. - affects transport layer
    "dns",  # DNS faults - resolution failures
    "time",  # TimeSkew - clock issues
}


# Mapping fault_type index to category for resolution strategy
FAULT_TYPE_CATEGORIES: dict[int, str] = {
    # Pod/Container lifecycle -> container/pod node
    0: "pod_lifecycle",  # PodKill
    1: "pod_lifecycle",  # PodFailure
    2: "pod_lifecycle",  # ContainerKill
    # Container resource -> container node
    3: "container_resource",  # MemoryStress
    4: "container_resource",  # CPUStress
    # HTTP request faults -> span node (matched by route)
    5: "http_span",  # HTTPRequestAbort
    6: "http_span",  # HTTPResponseAbort
    7: "http_span",  # HTTPRequestDelay
    8: "http_span",  # HTTPResponseDelay
    9: "http_span",  # HTTPResponseReplaceBody
    10: "http_span",  # HTTPResponsePatchBody
    11: "http_span",  # HTTPRequestReplacePath
    12: "http_span",  # HTTPRequestReplaceMethod
    13: "http_span",  # HTTPResponseReplaceCode
    # DNS faults -> service node
    14: "dns",  # DNSError
    15: "dns",  # DNSRandom
    # Time -> pod node
    16: "time",  # TimeSkew
    # Network faults -> service based on direction
    17: "network",  # NetworkDelay
    18: "network",  # NetworkLoss
    19: "network",  # NetworkDuplicate
    20: "network",  # NetworkCorrupt
    21: "network",  # NetworkBandwidth
    22: "network",  # NetworkPartition
    # JVM method faults -> span (via mapping) or container fallback
    23: "jvm_method",  # JVMLatency
    24: "jvm_method",  # JVMReturn
    25: "jvm_method",  # JVMException
    26: "jvm_method",  # JVMGarbageCollector
    27: "jvm_method",  # JVMCPUStress
    28: "jvm_method",  # JVMMemoryStress
    # JVM database faults -> database span
    29: "jvm_database",  # JVMMySQLLatency
    30: "jvm_database",  # JVMMySQLException
    # JVM runtime-level mutator (chaos-mesh JVMRuntimeMutator): rewrites a
    # method's bytecode at runtime — symptomatically the same family as
    # JVMException, so we reuse jvm_method routing.
    31: "jvm_method",  # JVMRuntimeMutator
}

# HTTP Response fault types - where propagation semantically starts from caller
# These affect the response the caller receives, not the callee's processing
HTTP_RESPONSE_FAULT_TYPES: set[int] = {
    6,  # HTTPResponseAbort
    8,  # HTTPResponseDelay
    9,  # HTTPResponseReplaceBody
    10,  # HTTPResponsePatchBody
    13,  # HTTPResponseReplaceCode
}


def _assert_fault_catalog_consistency() -> None:
    """Verify the three fault-type structures cover the same set.

    Three independent structures index the chaos-mesh fault catalog:

    * ``FAULT_TYPES`` (this module) — int → name.
    * ``FAULT_TYPE_CATEGORIES`` (this module) — int → resolution
      category (``container_resource``, ``http_span``, ``network``,
      ...).
    * ``FAULT_TYPE_TO_SEED_TIER`` (``models/fault_seed.py``) — name →
      canonical seed tier (``unavailable`` / ``erroring`` / ``slow``
      / ``degraded`` / ``silent``).

    They MUST cover the same set of fault names. Any drift (e.g., a
    new chaos-mesh fault type added to ``FAULT_TYPES`` without a
    corresponding entry in the other two) is a manifest-load-time
    error, not a silent fallback. Called once at import time so
    misconfiguration surfaces immediately rather than on the first
    run.
    """
    # Local import to dodge the circular import at module init time
    # (fault_seed already imports from this module).
    from rcabench_platform.v3.internal.reasoning.models.fault_seed import (
        FAULT_TYPE_TO_SEED_TIER,
    )
    names_from_types = set(FAULT_TYPES)
    names_from_categories = {FAULT_TYPES[i] for i in FAULT_TYPE_CATEGORIES}
    names_from_seed_tier = set(FAULT_TYPE_TO_SEED_TIER.keys())
    if names_from_types != names_from_categories:
        missing = names_from_types - names_from_categories
        extra = names_from_categories - names_from_types
        raise RuntimeError(
            f"FAULT_TYPES and FAULT_TYPE_CATEGORIES disagree: "
            f"missing-from-categories={sorted(missing)} "
            f"extra-in-categories={sorted(extra)}"
        )
    if names_from_types != names_from_seed_tier:
        missing = names_from_types - names_from_seed_tier
        extra = names_from_seed_tier - names_from_types
        raise RuntimeError(
            f"FAULT_TYPES and FAULT_TYPE_TO_SEED_TIER disagree: "
            f"missing-from-seed-tier={sorted(missing)} "
            f"extra-in-seed-tier={sorted(extra)}"
        )


_assert_fault_catalog_consistency()


def get_fault_category(fault_type: int, category: str) -> str:
    """Get granular fault category for downstream StartingPointResolver.

    Args:
        fault_type: The numeric fault type
        category: The coarse category from FAULT_TYPE_CATEGORIES

    Returns:
        Granular fault category: 'http_response', 'http_request', 'container',
        'jvm', 'pod', 'network', 'dns', or 'service'
    """
    if category == "http_span":
        if fault_type in HTTP_RESPONSE_FAULT_TYPES:
            return "http_response"
        return "http_request"
    elif category == "container_resource":
        return "container"
    elif category == "pod_lifecycle":
        return "pod"
    elif category == "jvm_method":
        return "jvm"
    elif category == "jvm_database":
        return "jvm_database"
    # Keep network, dns, time as-is
    return category


def _coalesce_engine_config(ec: dict) -> dict:
    """Lift a single ``engine_config[i]`` entry into ``display_config`` shape.

    AegisLab AutoHarness puts the chaos parameters flat on each
    ``engine_config`` entry. Legacy parsing expects a nested
    ``display_config.injection_point`` structure plus a few top-level
    fields (``namespace``, ``direction``, fault-specific durations).
    This helper builds an equivalent ``display_config`` dict so the
    downstream extraction code can stay schema-agnostic.

    Field mapping (AegisLab → display_config):
      - app                     → injection_point.app_name + injection_point.app_label
      - target_service          → injection_point.target_service
      - source_service          → injection_point.source_service (defaults to ``app``)
      - direction               → top-level ``direction``
      - namespace               → top-level ``namespace``
      - container_name          → injection_point.container_name
      - pod_name                → injection_point.pod_name
      - method / route / port   → injection_point.{method,route,server_port}
      - class_name / method_name (JVM)   → injection_point.{class_name,method_name}
      - db_name / table_name / operation_type → injection_point.{db_name,table_name,operation_type}
      - domain                  → injection_point.domain
      - latency / delay / memory_size → top-level for downstream consumers
    """
    ip: dict = {
        "app_name": ec.get("app"),
        "app_label": ec.get("app"),
        "target_service": ec.get("target_service"),
        "source_service": ec.get("source_service") or ec.get("app"),
        "container_name": ec.get("container_name"),
        "pod_name": ec.get("pod_name"),
        "method": ec.get("method"),
        "route": ec.get("route"),
        "server_address": ec.get("server_address"),
        "server_port": ec.get("server_port"),
        "class_name": ec.get("class_name"),
        "method_name": ec.get("method_name"),
        "db_name": ec.get("db_name"),
        "table_name": ec.get("table_name"),
        "operation_type": ec.get("operation_type"),
        "domain": ec.get("domain"),
    }
    return {
        "injection_point": {k: v for k, v in ip.items() if v is not None},
        "namespace": ec.get("namespace"),
        "direction": ec.get("direction"),
        "latency_ms": ec.get("latency"),
        "delay_duration": ec.get("delay") or ec.get("duration"),
        "latency_duration": ec.get("duration"),
        "memory_size": ec.get("memory_size") or ec.get("memory"),
    }


class InjectionPoint(BaseModel):
    """Parsed injection point from display_config.injection_point."""

    # Common fields
    app_name: str | None = None
    namespace: str | None = None

    # HTTP injection fields (fault_type 5-13)
    method: str | None = None  # GET, POST, etc.
    route: str | None = None  # /api/v1/trainservice/trains/byName/*
    server_address: str | None = None
    server_port: str | None = None

    # Container injection fields (fault_type 3-4)
    container_name: str | None = None
    pod_name: str | None = None
    app_label: str | None = None

    # JVM injection fields (fault_type 23-28)
    class_name: str | None = None
    method_name: str | None = None

    # Database injection fields (fault_type 29-30)
    db_name: str | None = None
    table_name: str | None = None
    operation_type: str | None = None  # SELECT, INSERT, etc.

    # Network injection fields (fault_type 17-22)
    source_service: str | None = None
    target_service: str | None = None
    direction: str | None = None  # to, from, both

    # DNS injection fields (fault_type 14-15)
    domain: str | None = None  # Target domain that fails to resolve


class InjectionMetadata(BaseModel):
    """Full injection metadata parsed from injection.json."""

    fault_type: int
    injection_point: InjectionPoint
    fault_type_name: str = ""

    # Fault-specific parameters
    delay_duration: int | None = None  # For delay faults (ms)
    latency_ms: int | None = None  # For latency faults
    latency_duration: int | None = None  # Alternative latency field
    memory_size: int | None = None  # For memory faults (MB)

    # Ground truth (for fallback)
    ground_truth_services: list[str] = Field(default_factory=list)
    ground_truth_containers: list[str] = Field(default_factory=list)
    ground_truth_pods: list[str] = Field(default_factory=list)

    @classmethod
    def from_injection_json(cls, data: dict) -> InjectionMetadata:
        """Parse injection.json dict into InjectionMetadata.

        Two schemas in the wild:

        - **Legacy rca_label**: top-level ``fault_type`` is an int index into
          ``FAULT_TYPES``; injection details live under
          ``display_config`` (JSON string) with a nested ``injection_point``.
        - **AegisLab AutoHarness**: top-level ``fault_type`` is a string —
          either a ``FAULT_TYPES`` member like ``"PodKill"`` or the literal
          ``"hybrid"`` for batches with multiple faults; injection details
          live under ``engine_config`` (list[dict]; first entry is the
          canonical signal for batch evaluation, mirroring the ground-truth
          ordering convention).

        We try the legacy schema first, then fall back to ``engine_config[0]``.
        """
        # Step 1: parse top-level fault_type (int / FAULT_TYPES string).
        # This is overridden below by ``engine_config[0].chaos_type`` when
        # the top-level is unknown ("hybrid", -1, missing).
        raw = data.get("fault_type", -1)
        if isinstance(raw, str):
            try:
                fault_type = FAULT_TYPES.index(raw)
            except ValueError:
                fault_type = -1
        else:
            try:
                fault_type = int(raw)
            except (TypeError, ValueError):
                fault_type = -1

        # Step 2: locate the canonical fault descriptor — display_config
        # (legacy) preferred, else engine_config[0] (AegisLab).
        display_config: dict = {}
        display_config_str = data.get("display_config", "")
        if isinstance(display_config_str, str) and display_config_str.strip():
            try:
                display_config = json.loads(display_config_str)
            except json.JSONDecodeError:
                logger.warning(f"Failed to parse display_config: {display_config_str[:100]}")
        elif isinstance(display_config_str, dict):
            display_config = display_config_str

        engine_first: dict = {}
        engine_config = data.get("engine_config")
        if isinstance(engine_config, list) and engine_config:
            head = engine_config[0]
            if isinstance(head, dict):
                engine_first = head

        if not display_config and engine_first:
            display_config = _coalesce_engine_config(engine_first)
            # If top-level fault_type was unknown, refine from chaos_type.
            if not (0 <= fault_type < len(FAULT_TYPES)):
                chaos_type = engine_first.get("chaos_type")
                if isinstance(chaos_type, str):
                    try:
                        fault_type = FAULT_TYPES.index(chaos_type)
                    except ValueError:
                        pass

        fault_type_name = FAULT_TYPES[fault_type] if 0 <= fault_type < len(FAULT_TYPES) else "Unknown"

        injection_point_data = display_config.get("injection_point", {})
        injection_point = InjectionPoint(
            app_name=injection_point_data.get("app_name"),
            namespace=display_config.get("namespace"),
            method=injection_point_data.get("method"),
            route=injection_point_data.get("route"),
            server_address=injection_point_data.get("server_address"),
            server_port=injection_point_data.get("server_port"),
            container_name=injection_point_data.get("container_name"),
            pod_name=injection_point_data.get("pod_name"),
            app_label=injection_point_data.get("app_label"),
            class_name=injection_point_data.get("class_name"),
            method_name=injection_point_data.get("method_name"),
            db_name=injection_point_data.get("db_name"),
            table_name=injection_point_data.get("table_name"),
            operation_type=injection_point_data.get("operation_type"),
            source_service=injection_point_data.get("source_service"),
            target_service=injection_point_data.get("target_service"),
            direction=display_config.get("direction"),
            domain=injection_point_data.get("domain"),
        )

        # Extract ground_truth for fallback. AegisLab emits a list[dict] (one entry per
        # fault in a batch); rca_label legacy datapacks emit a single dict.
        ground_truth = data.get("ground_truth", {})
        gt_services: list[str] = []
        gt_containers: list[str] = []
        gt_pods: list[str] = []
        gt_entries = ground_truth if isinstance(ground_truth, list) else [ground_truth]
        for entry in gt_entries:
            if not isinstance(entry, dict):
                continue
            gt_services.extend(entry.get("service", []) or [])
            gt_containers.extend(entry.get("container", []) or [])
            gt_pods.extend(entry.get("pod", []) or [])

        return cls(
            fault_type=fault_type,
            fault_type_name=fault_type_name,
            injection_point=injection_point,
            delay_duration=display_config.get("delay_duration"),
            latency_ms=display_config.get("latency_ms"),
            latency_duration=display_config.get("latency_duration"),
            memory_size=display_config.get("memory_size"),
            ground_truth_services=gt_services,
            ground_truth_containers=gt_containers,
            ground_truth_pods=gt_pods,
        )


class ResolvedInjection(BaseModel):
    """Result of injection node resolution."""

    injection_nodes: list[str]  # List of node names (e.g., "span|GET /api/...")
    start_kind: str  # "span", "container", "pod", or "service"
    category: str  # Fault category from FAULT_TYPE_CATEGORIES
    fault_category: str  # More granular category for downstream StartingPointResolver
    # e.g., 'http_response', 'http_request', 'container', 'jvm', 'pod', 'network', 'dns'
    fault_type_name: str  # Human-readable fault type name
    resolution_method: str  # How the resolution was done (for debugging)
    injection_point: InjectionPoint | None = None  # Original injection point config for downstream use


class InjectionNodeResolver:
    """Resolve injection.json to appropriate graph node(s).

    This resolver uses the fault type and injection_point metadata to find
    the true injection point at the appropriate granularity level.
    """

    def __init__(
        self,
        graph: HyperGraph,
        jvm_method_mapping: dict[str, str] | None = None,
    ):
        """Initialize the resolver.

        Args:
            graph: The HyperGraph to resolve nodes against
            jvm_method_mapping: Optional mapping from Java method (class.method)
                               to span name. Will be loaded later if not provided.
        """
        self.graph = graph
        self.jvm_method_mapping = jvm_method_mapping or {}

    def resolve(self, injection_data: dict) -> ResolvedInjection:
        """Resolve injection metadata to graph node names.

        Args:
            injection_data: Raw injection.json dict

        Returns:
            ResolvedInjection with injection nodes and metadata
        """
        metadata = InjectionMetadata.from_injection_json(injection_data)
        fault_type = metadata.fault_type
        category = FAULT_TYPE_CATEGORIES.get(fault_type, "service")
        fault_category = get_fault_category(fault_type, category)
        point = metadata.injection_point

        logger.debug(f"Resolving fault type {fault_type} ({metadata.fault_type_name}), category: {category}")

        if category == "http_span":
            nodes, method = self._resolve_http_span(point, metadata)
            return ResolvedInjection(
                injection_nodes=nodes,
                start_kind="span" if nodes and nodes[0].startswith("span|") else "service",
                category=category,
                fault_category=fault_category,
                fault_type_name=metadata.fault_type_name,
                resolution_method=method,
                injection_point=point,
            )

        elif category == "container_resource":
            nodes, method = self._resolve_container(point, metadata)
            return ResolvedInjection(
                injection_nodes=nodes,
                start_kind="container" if nodes and nodes[0].startswith("container|") else "service",
                category=category,
                fault_category=fault_category,
                fault_type_name=metadata.fault_type_name,
                resolution_method=method,
                injection_point=point,
            )

        elif category == "pod_lifecycle":
            nodes, method = self._resolve_pod_lifecycle(point, metadata)
            kind = "pod" if nodes and nodes[0].startswith("pod|") else "service"
            if nodes and nodes[0].startswith("container|"):
                kind = "container"
            return ResolvedInjection(
                injection_nodes=nodes,
                start_kind=kind,
                category=category,
                fault_category=fault_category,
                fault_type_name=metadata.fault_type_name,
                resolution_method=method,
                injection_point=point,
            )

        elif category == "jvm_method":
            nodes, method = self._resolve_jvm_method(point, metadata)
            kind = "span" if nodes and nodes[0].startswith("span|") else "service"
            if nodes and nodes[0].startswith("container|"):
                kind = "container"
            return ResolvedInjection(
                injection_nodes=nodes,
                start_kind=kind,
                category=category,
                fault_category=fault_category,
                fault_type_name=metadata.fault_type_name,
                resolution_method=method,
                injection_point=point,
            )

        elif category == "jvm_database":
            nodes, method = self._resolve_database_span(point, metadata)
            return ResolvedInjection(
                injection_nodes=nodes,
                start_kind="span" if nodes and nodes[0].startswith("span|") else "service",
                category=category,
                fault_category=fault_category,
                fault_type_name=metadata.fault_type_name,
                resolution_method=method,
                injection_point=point,
            )

        elif category == "network":
            nodes, method = self._resolve_network(point, metadata)
            return ResolvedInjection(
                injection_nodes=nodes,
                start_kind="service",
                category=category,
                fault_category=fault_category,
                fault_type_name=metadata.fault_type_name,
                resolution_method=method,
                injection_point=point,
            )

        elif category == "dns":
            nodes, method = self._resolve_dns(point, metadata)
            return ResolvedInjection(
                injection_nodes=nodes,
                start_kind="service",
                category=category,
                fault_category=fault_category,
                fault_type_name=metadata.fault_type_name,
                resolution_method=method,
                injection_point=point,
            )

        elif category == "time":
            nodes, method = self._resolve_time(point, metadata)
            return ResolvedInjection(
                injection_nodes=nodes,
                start_kind="pod" if nodes and nodes[0].startswith("pod|") else "service",
                category=category,
                fault_category=fault_category,
                fault_type_name=metadata.fault_type_name,
                resolution_method=method,
                injection_point=point,
            )

        else:
            # Default fallback to service
            nodes, method = self._resolve_service_fallback(metadata)
            return ResolvedInjection(
                injection_nodes=nodes,
                start_kind="service",
                category=category,
                fault_category=fault_category,
                fault_type_name=metadata.fault_type_name,
                resolution_method=method,
                injection_point=point,
            )

    def _resolve_http_span(
        self,
        point: InjectionPoint,
        metadata: InjectionMetadata,
    ) -> tuple[list[str], str]:
        """Match HTTP route to span node belonging to app_name service.

        This resolver finds spans that:
        1. Match the route pattern
        2. Belong to the app_name service (the caller that initiates the request)

        For HTTP faults, app_name is the service where the fault is injected
        (the caller making the request). We should match spans belonging to
        this service, not the target service (server_address).

        Span patterns to match:
        - 'GET /api/v1/trainservice/trains/byName/{id}'
        - 'HTTP GET http://ts-train-service:8080/api/v1/...'
        """
        route = point.route
        method = point.method
        server = point.server_address
        caller = point.app_name

        # Try to match spans by route if route info is available
        if not route or not method:
            # Fallback to service (prefer caller, then server)
            fallback_service = caller or server
            if not fallback_service and metadata.ground_truth_services:
                fallback_service = metadata.ground_truth_services[0]
            if fallback_service:
                return [f"service|{fallback_service}"], "http_span_fallback_no_route"
            return [], "http_span_no_route_no_service"

        # Normalize route: replace wildcards with regex pattern
        # /api/v1/trains/byName/* -> /api/v1/trains/byName/.*
        route_pattern = re.escape(route).replace(r"\*", ".*")

        # Try to match spans by route pattern, filtered by app_name service
        matched_spans: list[str] = []
        for node in self.graph.get_nodes_by_kind(PlaceKind.span):
            span_name = node.self_name

            # Check if span belongs to the caller service (app_name)
            if caller and not self._span_belongs_to_service(node, caller):
                continue

            # Pattern 1: "{METHOD} /api/..."
            if span_name.startswith(f"{method} "):
                path_part = span_name[len(method) + 1 :]
                if re.match(route_pattern, path_part):
                    matched_spans.append(node.uniq_name)
                    continue

            # Pattern 2: "HTTP {METHOD} http://{server}:port/api/..."
            if span_name.startswith(f"HTTP {method} http://"):
                # Extract path from full URL
                url_match = re.match(r"HTTP \w+ http://[^/]+(.+)", span_name)
                if url_match:
                    path_part = url_match.group(1)
                    if re.match(route_pattern, path_part):
                        matched_spans.append(node.uniq_name)
                        continue

        if matched_spans:
            logger.debug(f"Matched {len(matched_spans)} spans for route {route} in service {caller}")
            return matched_spans, "http_span_caller_service"

        # Fallback: service of the caller (app_name)
        fallback_service = caller or server
        if not fallback_service and metadata.ground_truth_services:
            fallback_service = metadata.ground_truth_services[0]

        if fallback_service:
            logger.debug(f"No span match for route {route}, falling back to service|{fallback_service}")
            return [f"service|{fallback_service}"], "http_span_fallback_to_service"

        return [], "http_span_no_match"

    def _span_belongs_to_service(self, span_node: Node, service_name: str) -> bool:
        """Check if a span belongs to the given service.

        Uses the 'includes' edge: service --includes--> span

        Args:
            span_node: The span node to check
            service_name: The service name to match (e.g., 'ts-preserve-service')

        Returns:
            True if the span belongs to the service
        """
        from rcabench_platform.v3.internal.reasoning.models.graph import DepKind

        if span_node.id is None:
            return False

        # Find service that includes this span. ``service_name_matches``
        # consults ``models/system_adapters.SYSTEM_FINGERPRINTS`` to
        # accept project-prefixed forms (``ts-`` for TrainTicket,
        # ``hotel-reserv-`` for hotel-reservation) without baking those
        # strings into the resolver itself.
        for src_id, _dst_id, edge_key in self.graph._graph.in_edges(span_node.id, keys=True):  # type: ignore[call-arg]
            if edge_key == DepKind.includes:
                src_node = self.graph.get_node_by_id(src_id)
                if src_node and src_node.kind == PlaceKind.service:
                    if service_name_matches(src_node.self_name, service_name):
                        return True
        return False

    def _resolve_container(
        self,
        point: InjectionPoint,
        metadata: InjectionMetadata,
    ) -> tuple[list[str], str]:
        """Resolve container fault to container node.

        Resolution priority (most authoritative first):

        1. ``point.container_name`` — explicit canonical name from
           legacy ``display_config.injection_point``.
        2. ``metadata.ground_truth_containers`` — the AegisLab schema
           encodes the precise container in ``ground_truth[i].container``;
           it is authoritative for the kill target.
        3. ``point.app_label`` / ``point.app_name`` — substring heuristic
           from ``engine_config[i].app``; used only when neither of the
           above is available.

        Why this order matters: AegisLab pods (hotel-reserv, otel-demo,
        ...) commonly host multiple containers per pod (the app plus a
        memcached sidecar, an envoy sidecar, etc). When the engine
        config only carries ``app: "profile"``, the substring heuristic
        non-deterministically picks ``hotel-reserv-profile-mmc`` (the
        memcached sidecar) over ``hotel-reserv-profile`` (the actual
        app container). The ground-truth list pinpoints the exact
        container chaos-mesh kills, so we use it whenever it's present.
        """
        # Build candidate list in priority order. Each candidate is a
        # tuple ``(name, source)`` so the resolution_method label tells
        # us which source won.
        candidates: list[tuple[str, str]] = []
        if point.container_name:
            candidates.append((point.container_name, "container_name"))
        if metadata.ground_truth_containers:
            internal = [c for c in metadata.ground_truth_containers if c not in EXTERNAL_SERVICE_NAMES]
            for c in internal or metadata.ground_truth_containers:
                if (c, "ground_truth") not in candidates:
                    candidates.append((c, "ground_truth"))
        if point.app_label and not any(n == point.app_label for n, _ in candidates):
            candidates.append((point.app_label, "app_label"))
        if point.app_name and not any(n == point.app_name for n, _ in candidates):
            candidates.append((point.app_name, "app_name"))

        if not candidates:
            return self._resolve_service_fallback(metadata)

        # Try exact match against each candidate, in priority order.
        for name, source in candidates:
            node = self.graph.get_node_by_name(f"container|{name}")
            if node:
                return [node.uniq_name], f"exact_container_match[{source}]"

        # No exact match: partial-match against the highest-priority
        # candidate. To avoid the sidecar-vs-app ambiguity, sort
        # matches by name length and pick the shortest — sidecar names
        # almost always carry a suffix (``-mmc``, ``-envoy``, ``-proxy``)
        # that makes them strictly longer than the app container.
        primary, primary_source = candidates[0]
        matches = [n for n in self.graph.get_nodes_by_kind(PlaceKind.container) if primary in n.self_name]
        if matches:
            matches.sort(key=lambda n: (len(n.self_name), n.self_name))
            return [matches[0].uniq_name], f"partial_container_match[{primary_source}]"

        # Fallback to service
        logger.debug(f"No container match for {primary}, falling back to service")
        return [f"service|{primary}"], "fallback_to_service"

    def _resolve_pod_lifecycle(
        self,
        point: InjectionPoint,
        metadata: InjectionMetadata,
    ) -> tuple[list[str], str]:
        """Resolve pod lifecycle fault (PodKill, PodFailure, ContainerKill)."""
        # For ContainerKill (fault_type 2), prefer container node
        if metadata.fault_type == 2:
            nodes, method = self._resolve_container(point, metadata)
            if nodes:
                return nodes, f"container_kill_{method}"

        # For PodKill/PodFailure, try pod node
        pod_name = point.pod_name
        if not pod_name and metadata.ground_truth_pods:
            pod_name = metadata.ground_truth_pods[0]

        if pod_name:
            pod_node = self.graph.get_node_by_name(f"pod|{pod_name}")
            if pod_node:
                return [pod_node.uniq_name], "exact_pod_match"

            # Try partial match
            for node in self.graph.get_nodes_by_kind(PlaceKind.pod):
                if pod_name in node.self_name:
                    return [node.uniq_name], "partial_pod_match"

        # Fallback to container
        nodes, method = self._resolve_container(point, metadata)
        if nodes:
            return nodes, f"fallback_{method}"

        # Final fallback to service (prefer internal services)
        return self._resolve_service_fallback(metadata)

    def _resolve_jvm_method(
        self,
        point: InjectionPoint,
        metadata: InjectionMetadata,
    ) -> tuple[list[str], str]:
        """Resolve JVM method fault to span via mapping.

        JVM method: assurance.controller.AssuranceController.modifyAssurance
        May map to span: "AssuranceController.modifyAssurance" or HTTP endpoint
        """
        if not point.class_name or not point.method_name:
            # No method info, fall back to container
            return self._resolve_container(point, metadata)

        full_method = f"{point.class_name}.{point.method_name}"

        # Use provided mapping if available
        if full_method in self.jvm_method_mapping:
            span_name = self.jvm_method_mapping[full_method]
            span_node = self.graph.get_node_by_name(f"span|{span_name}")
            if span_node:
                return [span_node.uniq_name], "jvm_mapping"

        # Try direct match - some spans are named like "Controller.method"
        simple_class_name = point.class_name.split(".")[-1]  # Get just "AssuranceController"
        direct_match_name = f"{simple_class_name}.{point.method_name}"

        for node in self.graph.get_nodes_by_kind(PlaceKind.span):
            if node.self_name == direct_match_name:
                return [node.uniq_name], "direct_span_match"

        # Fallback to container (JVM faults affect the whole container)
        logger.debug(f"No span match for JVM method {full_method}, falling back to container")
        nodes, method = self._resolve_container(point, metadata)
        return nodes, f"jvm_fallback_{method}"

    def _resolve_database_span(
        self,
        point: InjectionPoint,
        metadata: InjectionMetadata,
    ) -> tuple[list[str], str]:
        """Resolve JVM database fault to database span.

        Database spans are named like: "SELECT ts.trip2"
        """
        operation = point.operation_type  # SELECT, INSERT, etc.
        db_name = point.db_name
        table_name = point.table_name

        if operation and db_name and table_name:
            # Pattern: "SELECT ts.trip2"
            span_name_pattern = f"{operation} {db_name}.{table_name}"

            for node in self.graph.get_nodes_by_kind(PlaceKind.span):
                if node.self_name == span_name_pattern:
                    return [node.uniq_name], "exact_db_span_match"

                # Partial match
                if node.self_name.startswith(f"{operation} ") and table_name in node.self_name:
                    return [node.uniq_name], "partial_db_span_match"

        # Fallback to service (prefer app service over external mysql)
        if metadata.ground_truth_services:
            app_services = [s for s in metadata.ground_truth_services if s not in EXTERNAL_SERVICE_NAMES]
            if app_services:
                return [f"service|{app_services[0]}"], "fallback_to_app_service"
            return [f"service|{metadata.ground_truth_services[0]}"], "fallback_to_service"

        return self._resolve_container(point, metadata)

    def _resolve_network(
        self,
        point: InjectionPoint,
        metadata: InjectionMetadata,
    ) -> tuple[list[str], str]:
        """Resolve network fault based on direction.

        Network faults affect traffic between services.
        User perceives fault at the caller side.

        For external services (mysql, redis, etc.) that don't appear in traces,
        we need to use the internal service that calls them.
        """
        source = point.source_service
        target = point.target_service
        direction = point.direction

        # When A calls B and there's network issue, A perceives the problem
        # For 'to' direction: fault on outgoing from source -> source perceives
        # For 'from' direction: fault on incoming to source -> source perceives
        # For 'both': both directions affected -> source perceives

        # Network faults frequently involve more than one service in the GT
        # (partition, asymmetric loss between two endpoints, …). When the GT
        # lists multiple services and all of them resolve in the graph, take
        # them all as the injection_set — any single endpoint can leave the
        # corridor's forward BFS stranded on whichever side has fewer
        # outgoing edges (e.g. a callee whose primary out-edges are k8s
        # structural rather than ``calls``). The other end provides the
        # caller-perceptible expansion surface.
        gt_services = [s for s in metadata.ground_truth_services if self.graph.get_node_by_name(f"service|{s}")]
        if len(gt_services) >= 2:
            return (
                [f"service|{s}" for s in gt_services],
                f"network_ground_truth_{direction or 'unknown'}",
            )

        # Check if source exists in graph
        if source:
            source_node = self.graph.get_node_by_name(f"service|{source}")
            if source_node:
                return [f"service|{source}"], f"network_source_{direction or 'unknown'}"

            # Source not in graph (e.g., external mysql/redis)
            # Use target instead since it's the internal service affected
            logger.debug(f"Source service {source} not found in graph, using target service {target}")
            if target:
                target_node = self.graph.get_node_by_name(f"service|{target}")
                if target_node:
                    return [f"service|{target}"], f"network_target_{direction or 'unknown'}_source_external"

        # Fallback to ground_truth services, excluding external services
        if metadata.ground_truth_services:
            internal_services = [s for s in metadata.ground_truth_services if s not in EXTERNAL_SERVICE_NAMES]
            if internal_services:
                return [f"service|{internal_services[0]}"], "fallback_to_internal_service"
            # If only external services exist, use the first one anyway
            return [f"service|{metadata.ground_truth_services[0]}"], "fallback_to_service"

        return [], "no_network_source"

    def _resolve_dns(
        self,
        point: InjectionPoint,
        metadata: InjectionMetadata,
    ) -> tuple[list[str], str]:
        """Resolve DNS fault to service.

        DNS faults affect service discovery, so we start from service level.
        """
        app_name = point.app_name
        if app_name:
            return [f"service|{app_name}"], "dns_app"

        if metadata.ground_truth_services:
            return [f"service|{metadata.ground_truth_services[0]}"], "dns_fallback_service"

        return [], "no_dns_target"

    def _resolve_time(
        self,
        point: InjectionPoint,
        metadata: InjectionMetadata,
    ) -> tuple[list[str], str]:
        """Resolve time skew fault to pod.

        Time skew affects system clock at pod level.
        """
        pod_name = point.pod_name
        if pod_name:
            pod_node = self.graph.get_node_by_name(f"pod|{pod_name}")
            if pod_node:
                return [pod_node.uniq_name], "time_pod"

        # Fallback to service
        if metadata.ground_truth_services:
            return [f"service|{metadata.ground_truth_services[0]}"], "time_fallback_service"

        return [], "no_time_target"

    def _resolve_service_fallback(
        self,
        metadata: InjectionMetadata,
    ) -> tuple[list[str], str]:
        """Fallback resolution using ground_truth services.

        Prioritizes internal services over external services (mysql, redis, etc.).
        """
        if metadata.ground_truth_services:
            internal_services = [s for s in metadata.ground_truth_services if s not in EXTERNAL_SERVICE_NAMES]
            if internal_services:
                return [f"service|{internal_services[0]}"], "service_fallback_internal"
            # If only external services exist, use the first one anyway
            return [f"service|{metadata.ground_truth_services[0]}"], "service_fallback_all"

        return [], "no_service_fallback"


def resolve_injection_nodes(
    injection_data: dict,
    graph: HyperGraph,
    jvm_method_mapping: dict[str, str] | None = None,
) -> tuple[list[str], str]:
    """Convenience function to resolve injection nodes.

    Args:
        injection_data: Raw injection.json dict
        graph: The HyperGraph to resolve against
        jvm_method_mapping: Optional JVM method to span mapping

    Returns:
        Tuple of (injection_node_names, start_kind)
    """
    resolver = InjectionNodeResolver(graph, jvm_method_mapping)
    result = resolver.resolve(injection_data)
    return result.injection_nodes, result.start_kind
