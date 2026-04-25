"""Starting point resolver for fault propagation.

This module resolves where propagation should start based on rule semantics.
:class:`InjectionNodeResolver` finds physical injection points; this resolver
determines where propagation actually begins (caller, callee, or injection
point).

Phase 6 of #163: The chaos-tool fault catalog is now an authoritative
contract. Before doing any topology-based inference, this resolver
**first** confirms that the injected fault has a known canonical seed
tier (via ``models/fault_seed.canonical_seed_tier``). Known faults take
the deterministic path; only unknown chaos-tool extensions fall back to
legacy ``fault_category``-driven heuristics.
"""

import logging

from rcabench_platform.v3.internal.reasoning.models.fault_seed import (
    canonical_seed_tier,
)
from rcabench_platform.v3.internal.reasoning.models.graph import DepKind, HyperGraph, PlaceKind
from rcabench_platform.v3.internal.reasoning.models.injection import ResolvedInjection
from rcabench_platform.v3.internal.reasoning.rules.schema import PropagationRule

logger = logging.getLogger(__name__)


class StartingPointResolver:
    """Resolve propagation starting points based on rule semantics.

    After :class:`InjectionNodeResolver` returns physical injection points,
    this resolver determines actual propagation starting points based on
    the rule's ``propagation_source``:

    - ``injection_point``: Use physical injection location directly.
    - ``caller``: For response faults, find caller service (observes the
      delay/error).
    - ``callee``: For request faults, use injection point (processes the
      fault).
    """

    def __init__(self, graph: HyperGraph) -> None:
        """Initialize the resolver.

        Args:
            graph: The HyperGraph to traverse for caller/callee resolution.
        """
        self.graph = graph

    def resolve(
        self,
        physical_node_ids: list[int],
        resolved_injection: ResolvedInjection,
        rules: list[PropagationRule],
    ) -> list[int]:
        """Resolve propagation starting points from physical injection nodes.

        Args:
            physical_node_ids: Node IDs from :class:`InjectionNodeResolver`
                (physical injection points).
            resolved_injection: Injection metadata including ``fault_category``
                and ``fault_type_name``.
            rules: Propagation rules to check for ``propagation_source``.

        Returns:
            List of node IDs where propagation should start.
        """
        # Phase 6: enforce fault_seed mapping as the non-optional first
        # step. Known fault types skip topology-based inference entirely
        # for the "did the IR get a deterministic seed?" question — the
        # answer is unconditionally yes (InjectionAdapter handled it).
        # We still compute propagation_source from the (deterministic)
        # fault_category, because that decides *direction* of propagation,
        # not *whether* the seed exists.
        _tier, is_known_fault = canonical_seed_tier(resolved_injection.fault_type_name)
        if not is_known_fault:
            logger.warning(
                "StartingPointResolver: unknown fault_type_name=%r; "
                "InjectionAdapter has applied default-tier seed but "
                "propagation_source falls back to legacy fault_category=%s heuristics",
                resolved_injection.fault_type_name,
                resolved_injection.fault_category,
            )

        fault_category = resolved_injection.fault_category
        propagation_source = self._determine_propagation_source(fault_category, rules)

        if propagation_source == "injection_point":
            logger.debug(f"Using physical injection points directly: {physical_node_ids}")
            return physical_node_ids

        if propagation_source == "callee":
            # Callee = physical injection point (callee processes the request fault)
            logger.debug(f"Using callee (physical injection points): {physical_node_ids}")
            return physical_node_ids

        if propagation_source == "caller":
            # Find caller service from graph topology
            starting_points = self._resolve_to_caller(physical_node_ids)
            if starting_points:
                logger.info(
                    f"Resolved caller starting points for {fault_category}: "
                    f"{starting_points} (from physical: {physical_node_ids})"
                )
                return starting_points
            # Fallback to physical nodes if no caller found
            logger.warning(f"Could not find caller for {fault_category}, falling back to physical injection points")
            return physical_node_ids

        # Unknown propagation_source, use physical nodes
        logger.warning(f"Unknown propagation_source '{propagation_source}', using physical injection points")
        return physical_node_ids

    def _determine_propagation_source(
        self,
        fault_category: str,
        rules: list[PropagationRule],
    ) -> str:
        """Determine ``propagation_source`` from ``fault_category`` or rules.

        Priority:

        1. Find rules with explicit ``propagation_source`` matching this
           ``fault_category``.
        2. Use ``fault_category`` semantics (``http_response`` -> caller).
        3. Default to ``injection_point``.

        Args:
            fault_category: Granular fault category from
                :class:`ResolvedInjection`.
            rules: Propagation rules to check.

        Returns:
            Propagation source: ``injection_point``, ``caller``, or ``callee``.
        """
        # Check rules for explicit propagation_source
        for rule in rules:
            if rule.propagation_source and rule.propagation_source != "injection_point":
                # This rule has explicit non-default propagation_source
                # For now, use fault_category-based logic as rules aren't yet populated
                pass

        # Use fault_category semantics
        if fault_category == "http_response":
            # HTTP Response faults (AbortResponse, DelayResponse, etc.)
            # The fault effect (delay, error) is observed at the CALLER
            return "caller"

        if fault_category == "http_request":
            # HTTP Request faults (AbortRequest, DelayRequest, etc.)
            # The fault effect is at the CALLEE (processing the request)
            return "callee"

        # Other fault categories: container, jvm, pod, network, dns, etc.
        # Use physical injection point directly
        return "injection_point"

    def _resolve_to_caller(
        self,
        physical_node_ids: list[int],
    ) -> list[int]:
        """Find caller service(s) for the given physical injection nodes.

        For HTTP response faults, the physical injection is at the callee
        (server-side), but propagation should start from the caller service
        that observes the fault.

        Algorithm:

        1. For each physical node (span or service):
           a. If span: find caller spans via 'calls' edges, then their
              services.
           b. If service: find spans calling this service's spans.

        Args:
            physical_node_ids: Physical injection node IDs.

        Returns:
            List of caller service node IDs.
        """
        caller_node_ids: list[int] = []
        seen_callers: set[int] = set()

        for node_id in physical_node_ids:
            node = self.graph.get_node_by_id(node_id)
            if node.kind == PlaceKind.span:
                # Find caller spans via 'calls' edges (caller -> callee)
                callers = self._get_caller_spans(node_id)
                for caller_span_id in callers:
                    # Find service for caller span
                    caller_service_id = self._get_service_for_span(caller_span_id)
                    if caller_service_id and caller_service_id not in seen_callers:
                        caller_node_ids.append(caller_service_id)
                        seen_callers.add(caller_service_id)

            elif node.kind == PlaceKind.service:
                # Service injection: find spans included by this service,
                # then find callers of those spans
                service_spans = self._get_spans_for_service(node_id)
                for span_id in service_spans:
                    callers = self._get_caller_spans(span_id)
                    for caller_span_id in callers:
                        caller_service_id = self._get_service_for_span(caller_span_id)
                        if caller_service_id and caller_service_id not in seen_callers:
                            caller_node_ids.append(caller_service_id)
                            seen_callers.add(caller_service_id)

            else:
                # Other node types (container, pod, etc.) - no caller resolution
                logger.debug(f"Cannot resolve caller for node kind {node.kind}, using physical node")
                if node_id not in seen_callers:
                    caller_node_ids.append(node_id)
                    seen_callers.add(node_id)

        return caller_node_ids

    def _get_caller_spans(self, span_node_id: int) -> list[int]:
        """Find spans that call the given span (incoming 'calls' edges).

        Args:
            span_node_id: The callee span node ID.

        Returns:
            List of caller span node IDs.
        """
        callers: list[int] = []
        # In the graph: caller_span --calls--> callee_span
        # We need in_edges of type 'calls'
        for src_id, _dst_id, edge_key in self.graph._graph.in_edges(span_node_id, keys=True):  # type: ignore[call-arg]
            if edge_key == DepKind.calls:
                callers.append(src_id)
        return callers

    def _get_service_for_span(self, span_node_id: int) -> int | None:
        """Find the service that includes the given span.

        Args:
            span_node_id: The span node ID.

        Returns:
            Service node ID or ``None`` if not found.
        """
        # In the graph: service --includes--> span
        # We need in_edges of type 'includes'
        for src_id, _dst_id, edge_key in self.graph._graph.in_edges(span_node_id, keys=True):  # type: ignore[call-arg]
            if edge_key == DepKind.includes:
                src_node = self.graph.get_node_by_id(src_id)
                if src_node.kind == PlaceKind.service:
                    return int(src_id)
        return None

    def _get_spans_for_service(self, service_node_id: int) -> list[int]:
        """Find all spans included by the given service.

        Args:
            service_node_id: The service node ID.

        Returns:
            List of span node IDs.
        """
        spans: list[int] = []
        # In the graph: service --includes--> span
        # We need out_edges of type 'includes'
        for _src_id, dst_id, edge_key in self.graph._graph.out_edges(service_node_id, keys=True):  # type: ignore[call-arg]
            if edge_key == DepKind.includes:
                dst_node = self.graph.get_node_by_id(dst_id)
                if dst_node.kind == PlaceKind.span:
                    spans.append(dst_id)
        return spans
