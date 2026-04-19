import json
import logging

from openai import AsyncOpenAI
from pydantic import BaseModel, Field

from .causal_graph import CausalGraph

logger = logging.getLogger(__name__)


async def states_match(
    states1: frozenset[str],
    states2: frozenset[str],
    llm_client: AsyncOpenAI,
    model: str = "gpt-4o-mini",
) -> tuple[bool, str]:
    if not states1 or not states2:
        return False, "Empty state set"

    if states1 & states2:
        return True, "Exact match"

    prompt = f"""Determine if these two sets of system states describe the same or related abnormal conditions.

Set 1: {list(states1)}
Set 2: {list(states2)}

Consider:
- Different words for the same concept (e.g., "slow" = "high_latency")
- Related symptoms (e.g., "timeout" often accompanies "high_latency")
- Different granularities (e.g., "error" includes "5xx_error")

Respond with JSON: {{"match": true/false, "reason": "brief explanation"}}"""

    response = await llm_client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": prompt}],
        temperature=0,
        max_tokens=100,
        response_format={"type": "json_object"},
    )
    content = response.choices[0].message.content
    if content:
        result = json.loads(content)
        return bool(result.get("match", False)), result.get("reason", "")

    return False, "No response"


async def components_match(
    comp1: str,
    comp2: str,
    llm_client: AsyncOpenAI,
    model: str = "gpt-4o-mini",
) -> tuple[bool, str]:
    if not comp1 or not comp2:
        return False, "Empty component name"

    if comp1 == comp2:
        return True, "Exact match"

    # Normalize and check common variations
    comp1_lower = comp1.lower().replace("-", "_").replace(" ", "_")
    comp2_lower = comp2.lower().replace("-", "_").replace(" ", "_")
    if comp1_lower == comp2_lower:
        return True, "Normalized match"

    prompt = f"""Determine if these two component/service names refer to the same system component.

Component 1: {comp1}
Component 2: {comp2}

Consider:
- Different naming conventions (e.g., "ts-order-service" = "order-service" = "OrderService")
- Abbreviations (e.g., "db" = "database", "svc" = "service")
- Pod/instance suffixes (e.g., "mysql-0" = "mysql")
- Namespace prefixes (e.g., "default/mysql" = "mysql")

Respond with JSON: {{"match": true/false, "reason": "brief explanation"}}"""

    response = await llm_client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": prompt}],
        temperature=0,
        max_tokens=100,
        response_format={"type": "json_object"},
    )
    content = response.choices[0].message.content
    if content:
        result = json.loads(content)
        return bool(result.get("match", False)), result.get("reason", "")

    return False, "No response"


class PrimaryMetrics(BaseModel):
    # Edge metrics (most important - captures causal relationships)
    edge_precision: float = Field(..., description="Agent's edges that are correct: matched / agent_edges")
    edge_recall: float = Field(..., description="GT edges found by agent: matched / gt_edges")
    edge_f1: float = Field(..., description="Harmonic mean of precision and recall")

    # Node metrics
    node_precision: float = Field(..., description="Agent's nodes that are correct")
    node_recall: float = Field(..., description="GT nodes found by agent")
    node_f1: float = Field(..., description="Harmonic mean")

    # Root cause (supports multiple)
    root_cause_precision: float = Field(..., description="Agent's root causes that are correct")
    root_cause_recall: float = Field(..., description="GT root causes found by agent")
    root_cause_f1: float = Field(..., description="Harmonic mean of root cause precision and recall")

    # Path reachability
    path_reachability: bool | None = Field(
        default=None,
        description=(
            "Whether agent's graph contains a valid path from a correct root cause"
            " to a GT alarm node. None if diagnosis was incorrect."
        ),
    )


class SecondaryMetrics(BaseModel):
    # Component-level accuracy (span/pod granularity)
    component_precision: float = Field(default=0.0, description="Fine-grained component precision")
    component_recall: float = Field(default=0.0, description="Fine-grained component recall")
    component_f1: float = Field(default=0.0, description="Fine-grained component F1")

    # State description accuracy (uses LLM for semantic matching)
    # Note: GT states may be incomplete, so we don't penalize agent for identifying extra states
    state_coverage: float = Field(
        default=0.0,
        description="Among components with states in GT, what % did agent identify with matching states",
    )
    extra_states_ratio: float = Field(
        default=0.0,
        description="Ratio of agent components with states not in GT (informational, not penalized)",
    )

    # Temporal accuracy (if timestamps available)
    temporal_score: float | None = Field(default=None, description="Timestamp accuracy score (0-1)")


class DiagnosticInfo(BaseModel):
    matched_service_edges: list[tuple[str, str]] = Field(default_factory=list)
    missed_service_edges: list[tuple[str, str]] = Field(default_factory=list, description="GT has, agent missed")
    hallucinated_service_edges: list[tuple[str, str]] = Field(default_factory=list, description="Agent has, GT doesn't")

    matched_services: list[str] = Field(default_factory=list)
    missed_services: list[str] = Field(default_factory=list)
    hallucinated_services: list[str] = Field(default_factory=list)

    # Component level
    matched_components: list[str] = Field(default_factory=list)
    missed_components: list[str] = Field(default_factory=list)
    hallucinated_components: list[str] = Field(default_factory=list)


class GraphMatchResult(BaseModel):
    primary: PrimaryMetrics
    secondary: SecondaryMetrics
    diagnostic: DiagnosticInfo


def _compute_f1(precision: float, recall: float) -> float:
    if precision + recall == 0:
        return 0.0
    return 2 * precision * recall / (precision + recall)


def _normalize_name(name: str, strip_prefix: bool = True) -> str:
    normalized = name.strip()

    # Strip type prefixes if requested
    if strip_prefix and "|" in normalized:
        normalized = normalized.split("|", 1)[1]

    # Convert to lowercase
    normalized = normalized.lower()

    # Remove 'ts-' prefix
    if normalized.startswith("ts-"):
        normalized = normalized[3:]

    # Remove hyphens
    normalized = normalized.replace("-", "")

    return normalized


def _compute_precision_recall(agent_set: set, gt_set: set) -> tuple[float, float, float, set, set, set]:
    """Compute precision, recall, F1 and matched/missed/hallucinated sets.

    Boundary case: If both sets are empty, return perfect score (1.0, 1.0, 1.0)
    since there's nothing to detect and agent correctly detected nothing.
    """
    if not agent_set and not gt_set:
        return 1.0, 1.0, 1.0, set(), set(), set()  # Both empty = perfect match

    matched = agent_set & gt_set
    missed = gt_set - agent_set
    hallucinated = agent_set - gt_set

    precision = len(matched) / len(agent_set) if agent_set else 0.0
    recall = len(matched) / len(gt_set) if gt_set else 0.0
    f1 = _compute_f1(precision, recall)

    return precision, recall, f1, matched, missed, hallucinated


def compute_path_reachability(
    agent_graph: CausalGraph,
    gt_graph: CausalGraph,
    gt_root_cause_services: set[str] | None = None,
) -> bool | None:
    """Check if the agent's graph has a valid path from a correct root cause to a GT alarm.

    Among correctly identified root causes, checks whether the agent's predicted
    service-level graph contains at least one directed path from a correct root
    cause to any ground-truth alarm/symptom service.

    The loadgenerator service is excluded from GT alarm targets since agents are
    not expected to include it. If all GT alarms map to loadgenerator, the
    predecessors of loadgenerator in the GT graph are used as alarm targets instead.

    Args:
        agent_graph: Agent's output causal graph.
        gt_graph: Ground truth causal graph.
        gt_root_cause_services: Override GT root cause services. If None, falls
            back to gt_graph.get_root_cause_services().

    Returns:
        True if at least one valid path exists, False if correct root causes
        exist but no path reaches a GT alarm, None if no root causes were
        correctly identified (metric not applicable).
    """
    # Get agent and GT root causes (normalized)
    agent_roots = agent_graph.get_root_cause_services()
    gt_roots = gt_root_cause_services if gt_root_cause_services is not None else gt_graph.get_root_cause_services()

    agent_roots_normalized = {_normalize_name(s, strip_prefix=False) for s in agent_roots}
    gt_roots_normalized = {_normalize_name(s, strip_prefix=False) for s in gt_roots}

    # Find correctly identified root causes
    correct_roots = agent_roots_normalized & gt_roots_normalized
    if not correct_roots:
        return None  # Diagnosis was wrong, metric not applicable

    # Get GT alarm services (normalized), excluding loadgenerator
    gt_alarms = gt_graph.get_alarm_services()
    if not gt_alarms:
        logger.warning(
            "GT graph has no alarm_nodes; path_reachability metric not applicable. "
            "This may indicate incomplete ground truth data."
        )
        return None

    gt_alarms_normalized = {_normalize_name(s, strip_prefix=False) for s in gt_alarms}
    gt_alarms_normalized.discard("loadgenerator")

    # If all alarms were loadgenerator, use its predecessors in the GT graph as alarm targets
    if not gt_alarms_normalized:
        gt_service_edges = gt_graph.get_service_edges()
        for src, tgt in gt_service_edges:
            if _normalize_name(tgt, strip_prefix=False) == "loadgenerator":
                gt_alarms_normalized.add(_normalize_name(src, strip_prefix=False))

    if not gt_alarms_normalized:
        logger.warning(
            "GT graph alarm_nodes only contain loadgenerator with no predecessors; "
            "path_reachability metric not applicable."
        )
        return None

    # Build adjacency list from agent's service-level edges (normalized)
    agent_service_edges = agent_graph.get_service_edges()
    adj: dict[str, list[str]] = {}
    for src, tgt in agent_service_edges:
        src_norm = _normalize_name(src, strip_prefix=False)
        tgt_norm = _normalize_name(tgt, strip_prefix=False)
        adj.setdefault(src_norm, []).append(tgt_norm)

    # BFS from each correct root cause to check if any GT alarm is reachable
    for root in correct_roots:
        visited: set[str] = set()
        queue = [root]
        visited.add(root)
        while queue:
            current = queue.pop(0)
            if current in gt_alarms_normalized:
                return True
            for neighbor in adj.get(current, []):
                if neighbor not in visited:
                    visited.add(neighbor)
                    queue.append(neighbor)

    return False


async def evaluate_graphs(
    agent_graph: CausalGraph,
    gt_graph: CausalGraph,
    llm_client: AsyncOpenAI | None = None,
    model: str = "gpt-4o-mini",
    gt_root_cause_services: set[str] | None = None,
) -> GraphMatchResult:
    """Evaluate agent's causal graph against ground truth.

    Primary metrics use service-level matching for unambiguous evaluation.
    Secondary metrics assess fine-grained capabilities (state matching uses LLM).

    Args:
        agent_graph: Agent's output graph
        gt_graph: Ground truth graph
        llm_client: AsyncOpenAI client for semantic state matching (optional)
        model: Model to use for LLM matching
        gt_root_cause_services: Override GT root cause services (e.g. from injection.json).
            If None, falls back to gt_graph.get_root_cause_services().

    Returns:
        GraphMatchResult with all metrics and diagnostics
    """
    # ═══════════════════════════════════════════════════════════════════════════
    # Primary Metrics: Service Level (unambiguous)
    # ═══════════════════════════════════════════════════════════════════════════

    # Service-level edges (with normalization)
    agent_service_edges = agent_graph.get_service_edges()
    gt_service_edges = gt_graph.get_service_edges()

    # Normalize edges for comparison
    agent_edges_normalized = {
        (_normalize_name(s, strip_prefix=False), _normalize_name(t, strip_prefix=False)) for s, t in agent_service_edges
    }
    gt_edges_normalized = {
        (_normalize_name(s, strip_prefix=False), _normalize_name(t, strip_prefix=False)) for s, t in gt_service_edges
    }

    edge_precision, edge_recall, edge_f1, matched_edges, missed_edges, hallucinated_edges = _compute_precision_recall(
        agent_edges_normalized, gt_edges_normalized
    )

    # Service-level nodes (with normalization)
    agent_services = agent_graph.get_service_nodes()
    gt_services = gt_graph.get_service_nodes()

    # Normalize nodes for comparison
    agent_services_normalized = {_normalize_name(s, strip_prefix=False) for s in agent_services}
    gt_services_normalized = {_normalize_name(s, strip_prefix=False) for s in gt_services}

    node_precision, node_recall, node_f1, matched_nodes, missed_nodes, hallucinated_nodes = _compute_precision_recall(
        agent_services_normalized, gt_services_normalized
    )

    # Root cause metrics (supports multiple, with normalization)
    agent_roots = agent_graph.get_root_cause_services()
    gt_roots = gt_root_cause_services if gt_root_cause_services is not None else gt_graph.get_root_cause_services()

    # Normalize root causes for comparison
    agent_roots_normalized = {_normalize_name(s, strip_prefix=False) for s in agent_roots}
    gt_roots_normalized = {_normalize_name(s, strip_prefix=False) for s in gt_roots}

    rc_precision, rc_recall, rc_f1, _, _, _ = _compute_precision_recall(agent_roots_normalized, gt_roots_normalized)

    # Path reachability
    path_reachability = compute_path_reachability(agent_graph, gt_graph, gt_root_cause_services)

    primary = PrimaryMetrics(
        edge_precision=edge_precision,
        edge_recall=edge_recall,
        edge_f1=edge_f1,
        node_precision=node_precision,
        node_recall=node_recall,
        node_f1=node_f1,
        root_cause_precision=rc_precision,
        root_cause_recall=rc_recall,
        root_cause_f1=rc_f1,
        path_reachability=path_reachability,
    )

    # ═══════════════════════════════════════════════════════════════════════════
    # Secondary Metrics: Fine-grained (capability assessment)
    # ═══════════════════════════════════════════════════════════════════════════

    # Component-level matching (uses LLM for semantic matching)
    agent_components = {node.component for node in agent_graph.nodes}
    gt_components = {node.component for node in gt_graph.nodes}

    (
        comp_precision,
        comp_recall,
        comp_f1,
        matched_comps,
        missed_comps,
        hallucinated_comps,
    ) = await _compute_component_metrics(agent_components, gt_components, llm_client, model)

    # State matching using LLM for semantic equivalence
    state_coverage, extra_states_ratio = await _compute_state_metrics(agent_graph, gt_graph, llm_client, model)

    # Temporal score (if timestamps available)
    temporal_score = _compute_temporal_score(agent_graph, gt_graph)

    secondary = SecondaryMetrics(
        component_precision=comp_precision,
        component_recall=comp_recall,
        component_f1=comp_f1,
        state_coverage=state_coverage,
        extra_states_ratio=extra_states_ratio,
        temporal_score=temporal_score,
    )

    # ═══════════════════════════════════════════════════════════════════════════
    # Diagnostic Info
    # ═══════════════════════════════════════════════════════════════════════════

    diagnostic = DiagnosticInfo(
        matched_service_edges=sorted(matched_edges),
        missed_service_edges=sorted(missed_edges),
        hallucinated_service_edges=sorted(hallucinated_edges),
        matched_services=sorted(matched_nodes),
        missed_services=sorted(missed_nodes),
        hallucinated_services=sorted(hallucinated_nodes),
        matched_components=sorted(matched_comps),
        missed_components=sorted(missed_comps),
        hallucinated_components=sorted(hallucinated_comps),
    )

    return GraphMatchResult(
        primary=primary,
        secondary=secondary,
        diagnostic=diagnostic,
    )


async def _compute_state_metrics(
    agent_graph: CausalGraph,
    gt_graph: CausalGraph,
    llm_client: AsyncOpenAI | None,
    model: str,
) -> tuple[float, float]:
    """Compute state coverage and extra states ratio using LLM semantic matching.

    Since GT states may be incomplete, we focus on:
    - Coverage: Among GT components with states, how many did agent identify correctly?
    - Extra ratio: How many agent components with states are not in GT? (informational only)

    We don't penalize agent for identifying additional states beyond GT.

    Returns:
        Tuple of (state_coverage, extra_states_ratio)
    """
    # Build normalized component -> state mappings
    agent_states: dict[str, frozenset[str]] = {}
    for node in agent_graph.nodes:
        if node.state:
            normalized_comp = _normalize_name(node.component, strip_prefix=True)
            agent_states[normalized_comp] = node.state

    gt_states: dict[str, frozenset[str]] = {}
    for node in gt_graph.nodes:
        if node.state:
            normalized_comp = _normalize_name(node.component, strip_prefix=True)
            gt_states[normalized_comp] = node.state

    # Boundary cases
    if not agent_states and not gt_states:
        return 1.0, 0.0  # Both empty: perfect coverage, no extra states
    if not gt_states:
        # GT has no states to cover, agent has some
        # Coverage undefined (use 1.0 as "nothing to miss"), all agent states are "extra"
        return 1.0, 1.0
    if not agent_states:
        # Agent found nothing, GT has states: 0% coverage, no extra states
        return 0.0, 0.0

    # Find common components
    common_components = set(agent_states.keys()) & set(gt_states.keys())

    # For each common component, check if states match
    matched_count = 0
    for comp in common_components:
        agent_s = agent_states[comp]
        gt_s = gt_states[comp]

        if llm_client:
            match, _ = await states_match(agent_s, gt_s, llm_client, model)
        else:
            match = bool(agent_s & gt_s)

        if match:
            matched_count += 1

    # Coverage: Among GT components with states, what % did agent identify correctly?
    coverage = matched_count / len(gt_states)

    # Extra states ratio: What % of agent's state identifications are not in GT?
    # (Informational only - GT may be incomplete, so this isn't necessarily wrong)
    agent_only_components = len(agent_states) - matched_count
    extra_ratio = agent_only_components / len(agent_states) if agent_states else 0.0

    return coverage, extra_ratio


async def _compute_component_metrics(
    agent_components: set[str],
    gt_components: set[str],
    llm_client: AsyncOpenAI | None,
    model: str,
) -> tuple[float, float, float, set[str], set[str], set[str]]:
    """Compute component precision, recall, F1 using LLM semantic matching.

    Args:
        agent_components: Set of component names from agent graph
        gt_components: Set of component names from ground truth graph
        llm_client: AsyncOpenAI client for semantic matching (optional)
        model: Model to use for LLM matching

    Returns:
        Tuple of (precision, recall, f1, matched, missed, hallucinated)
    """
    if not agent_components and not gt_components:
        return 1.0, 1.0, 1.0, set(), set(), set()
    if not agent_components:
        return 0.0, 0.0, 0.0, set(), gt_components.copy(), set()
    if not gt_components:
        return 0.0, 1.0, 0.0, set(), set(), agent_components.copy()

    # Normalize component names to handle format differences (service|, span|, etc.)
    agent_normalized = {_normalize_name(c, strip_prefix=True): c for c in agent_components}
    gt_normalized = {_normalize_name(c, strip_prefix=True): c for c in gt_components}

    # First do exact matching on normalized names
    exact_matched_normalized = set(agent_normalized.keys()) & set(gt_normalized.keys())
    exact_matched = {agent_normalized[n] for n in exact_matched_normalized}

    remaining_agent_normalized = set(agent_normalized.keys()) - exact_matched_normalized
    remaining_gt_normalized = set(gt_normalized.keys()) - exact_matched_normalized

    remaining_agent = {agent_normalized[n] for n in remaining_agent_normalized}
    remaining_gt = {gt_normalized[n] for n in remaining_gt_normalized}

    # Then do semantic matching for remaining components
    semantic_matched_agent: set[str] = set()
    semantic_matched_gt: set[str] = set()

    if llm_client and remaining_agent and remaining_gt:
        for agent_comp in remaining_agent:
            for gt_comp in remaining_gt:
                if gt_comp in semantic_matched_gt:
                    continue
                match, _ = await components_match(agent_comp, gt_comp, llm_client, model)
                if match:
                    semantic_matched_agent.add(agent_comp)
                    semantic_matched_gt.add(gt_comp)
                    break

    # Combine results
    matched = exact_matched | semantic_matched_agent
    missed = remaining_gt - semantic_matched_gt
    hallucinated = remaining_agent - semantic_matched_agent

    precision = len(matched) / len(agent_components)
    recall = len(matched) / len(gt_components)
    f1 = _compute_f1(precision, recall)

    return precision, recall, f1, matched, missed, hallucinated


def _compute_temporal_score(agent_graph: CausalGraph, gt_graph: CausalGraph) -> float | None:
    """Compute temporal accuracy score.

    For matching components, compute how close the timestamps are.
    Returns None if no timestamps available.
    """
    # Build component -> timestamp maps
    agent_timestamps: dict[str, int] = {}
    for node in agent_graph.nodes:
        if node.timestamp is not None:
            agent_timestamps[node.component] = node.timestamp

    gt_timestamps: dict[str, int] = {}
    for node in gt_graph.nodes:
        if node.timestamp is not None:
            gt_timestamps[node.component] = node.timestamp

    if not agent_timestamps or not gt_timestamps:
        return None

    # Find common components with timestamps
    common_components = set(agent_timestamps.keys()) & set(gt_timestamps.keys())
    if not common_components:
        return None

    # Compute relative errors
    errors = []
    for comp in common_components:
        agent_ts = agent_timestamps[comp]
        gt_ts = gt_timestamps[comp]
        if gt_ts != 0:
            relative_error = abs(agent_ts - gt_ts) / abs(gt_ts)
            errors.append(min(relative_error, 1.0))  # Cap at 1.0

    if not errors:
        return None

    # Score = 1 - average_error
    avg_error = sum(errors) / len(errors)
    return 1.0 - avg_error
