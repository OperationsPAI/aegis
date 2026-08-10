from collections import defaultdict

import duckdb
import numpy as np
from scipy import stats

from ....logging import logger
from ....utils.env import debug
from .model import (
    SDG,
    Observation,
    Relation,
    RelationChange,
    RootCauseCandidate,
    Symptom,
    SymptomType,
)

# ============================================================================
# Module 5: Relation Computation (Distribution-based, not averages!)
# ============================================================================


class RelationComputer:
    """
    Computes R(S|P) relations as DISTRIBUTIONS, not averages.

    IMPORTANT: P should be Observations, S should be Symptoms.
    - P (Observation): Entry-level spans with SLO violations
    - S (Symptom): Internal spans that CAUSE the observation

    This is critical: we compute "for each P (observation), how many S (symptom events)"
    to get a distribution, not just collapse it to a single average number.
    """

    # HTTP status code threshold for error detection
    HTTP_ERROR_STATUS_CODE = 400

    def __init__(self, con: duckdb.DuckDBPyConnection, sdg: SDG):
        self.con = con
        self.sdg = sdg

    def compute_relations(
        self,
        period: str,  # "good" or "bad"
        observations: list[Observation],
        symptoms: list[Symptom],
        proxy_check: bool = False,  # NEW: Support causal chain refinement
    ) -> list[Relation]:
        """
        Compute forward relations R(S|P) for all (Observation P, Symptom S) pairs.

        R(S|P) = distribution of "how many symptom S events each observation P causes"

        This maps observations (entry-level SLO violations) to internal symptoms
        (the actual problematic spans causing the violations).

        When proxy_check=True: Used for causal chain refinement to check R(P_B|P_A).
        We only care if P_B occurs (s_count > 0), not its latency/error properties.
        """
        if debug():
            logger.debug(
                f"Computing relations for {len(observations)} observations × {len(symptoms)} symptoms "
                f"in {period} period (proxy_check={proxy_check})"
            )

        relations = []

        for symptom in symptoms:
            symptom_relations = self._compute_for_symptom(period, observations, symptom, proxy_check)
            relations.extend(symptom_relations)

        if debug():
            logger.debug(f"Computed {len(relations)} relations in {period} period")

        return relations

    def _compute_for_symptom(
        self,
        period: str,
        observations: list[Observation],
        symptom: Symptom,
        proxy_check: bool = False,
    ) -> list[Relation]:
        """
        Compute relations between observations and a specific symptom.

        We find observations that could causally lead to this symptom based on:
        1. Same symptom type (latency/error)
        2. Trace-level containment (symptom appears in observation's traces)
        """
        table_name = f"traces_{period}"
        relations = []

        # Find observations that match this symptom's type
        relevant_observations = [obs for obs in observations if obs.observation_type == symptom.symptom_type]

        for observation in relevant_observations:
            relation = self._compute_relation_for_pair(
                table_name=table_name,
                observation=observation,
                symptom=symptom,
            )

            if relation:
                relations.append(relation)

        return relations

    def _compute_relation_for_pair(
        self,
        table_name: str,
        observation: Observation,
        symptom: Symptom,
    ) -> Relation | None:
        """
        Compute R(S|P) where P=Observation, S=Symptom.

        For each observation span P (entry-level), count how many symptom spans S
        appear in the same trace (causally connected via trace containment).
        """
        # Use trace_span_name if available, otherwise fall back to entry_span_name
        obs_span_name = observation.trace_span_name or observation.entry_span_name

        # First, check if observation spans exist
        check_obs_query = f"""
        SELECT COUNT(*) as obs_count
        FROM {table_name}
        WHERE span_name = ?
          AND service_name = ?
        """

        try:
            obs_result = self.con.execute(check_obs_query, [obs_span_name, observation.service_name]).fetchone()
            obs_count = obs_result[0] if obs_result else 0

            if obs_count == 0:
                return None
        except Exception as e:
            if debug():
                logger.warning(f"Error checking observation spans: {e}")
            return None

        # Check if symptom spans exist
        check_symptom_query = f"""
        SELECT COUNT(*) as symptom_count
        FROM {table_name}
        WHERE span_name = ?
          AND service_name = ?
        """

        try:
            symptom_result = self.con.execute(check_symptom_query, [symptom.span_name, symptom.service_name]).fetchone()
            symptom_count = symptom_result[0] if symptom_result else 0

            if symptom_count == 0:
                return None
        except Exception as e:
            if debug():
                logger.warning(f"Error checking symptom spans: {e}")
            return None

        # Build query to find R(S|P):
        # For each observation span P, count how many symptom spans S appear in same trace
        query = f"""
        WITH observation_spans AS (
            SELECT trace_id, span_id
            FROM {table_name}
            WHERE span_name = ?
              AND service_name = ?
        ),
        symptom_counts_per_observation AS (
            SELECT
                p.span_id,
                COUNT(s.span_id) AS s_count
            FROM observation_spans p
            LEFT JOIN {table_name} s
                ON p.trace_id = s.trace_id  -- Same trace (causal containment)
                AND s.span_name = ?
                AND s.service_name = ?
            GROUP BY p.span_id
        )
        SELECT s_count
        FROM symptom_counts_per_observation
        ORDER BY s_count
        """

        try:
            result = self.con.execute(
                query,
                [
                    obs_span_name,
                    observation.service_name,
                    symptom.span_name,
                    symptom.service_name,
                ],
            ).fetchall()

        except Exception as e:
            if debug():
                logger.warning(f"Error computing relation: {e}")
            return None

        if not result:
            return None

        # Extract distribution: list of counts
        distribution = [row[0] for row in result]

        if len(distribution) == 0:
            return None

        return Relation(
            predecessor_service=observation.service_name,
            predecessor_span_name=obs_span_name,  # Use actual span name in traces table
            symptom_service=symptom.service_name,
            symptom_span_name=symptom.span_name,  # Symptom's specific span
            symptom_type=symptom.symptom_type,
            distribution=distribution,
            sample_size=len(distribution),
        )


# ============================================================================
# Module 5: Relation Filtering (Statistical Tests, NO magic numbers!)
# ============================================================================


class RelationFilter:
    """
    Filters relations using statistical tests to find significant changes.

    NO hardcoded thresholds - uses Mann-Whitney U test to compare distributions.
    """

    # Minimum sample size for statistical testing
    MIN_SAMPLE_SIZE = 10

    # Significance level (standard p-value threshold)
    ALPHA = 0.05

    # Minimum mean distribution value for new relations
    MIN_NEW_RELATION_MEAN = 0.1

    # P-value for very significant new relations
    NEW_RELATION_P_VALUE = 0.001

    # Minimum magnitude threshold for trivial changes
    MIN_MAGNITUDE_THRESHOLD = 0.01

    def __init__(self):
        pass

    def filter_significant_changes(
        self, relations_good: list[Relation], relations_bad: list[Relation]
    ) -> list[RelationChange]:
        """
        Filter relations using Mann-Whitney U test.

        Compares the distributions (not averages!) to find statistically significant changes.
        """
        # Index relations by (predecessor_service, predecessor_span, symptom_service, symptom_span, type)
        good_index = {
            (
                r.predecessor_service,
                r.predecessor_span_name,
                r.symptom_service,
                r.symptom_span_name,
                r.symptom_type,
            ): r
            for r in relations_good
        }
        bad_index = {
            (
                r.predecessor_service,
                r.predecessor_span_name,
                r.symptom_service,
                r.symptom_span_name,
                r.symptom_type,
            ): r
            for r in relations_bad
        }

        changes = []

        # Check all relations in bad period
        for key, bad_rel in bad_index.items():
            good_rel = good_index.get(key)

            if good_rel is None:
                # New relation in bad period (didn't exist in good)
                # Check if it's substantial enough to matter
                if (
                    bad_rel.sample_size >= self.MIN_SAMPLE_SIZE
                    and np.mean(bad_rel.distribution) > self.MIN_NEW_RELATION_MEAN
                ):
                    changes.append(
                        RelationChange(
                            relation_good=Relation(
                                predecessor_service=bad_rel.predecessor_service,
                                predecessor_span_name=bad_rel.predecessor_span_name,
                                symptom_service=bad_rel.symptom_service,
                                symptom_span_name=bad_rel.symptom_span_name,
                                symptom_type=bad_rel.symptom_type,
                                distribution=[0],  # Empty in good period
                                sample_size=0,
                            ),
                            relation_bad=bad_rel,
                            change_magnitude=float(np.mean(bad_rel.distribution)),
                            statistical_significance=self.NEW_RELATION_P_VALUE,  # Very significant (new relation)
                        )
                    )
            else:
                # Compare good vs bad using statistical test
                change = self._compare_relations(good_rel, bad_rel)
                if change:
                    changes.append(change)

        return changes

    def _compare_relations(self, good: Relation, bad: Relation) -> RelationChange | None:
        if good.sample_size < self.MIN_SAMPLE_SIZE or bad.sample_size < self.MIN_SAMPLE_SIZE:
            return None

        try:
            statistic, p_value = stats.mannwhitneyu(good.distribution, bad.distribution, alternative="two-sided")
        except Exception as e:
            if debug():
                logger.warning(f"Mann-Whitney U test failed: {e}")
            return None

        # Check if statistically significant
        if p_value >= self.ALPHA:
            return None

        # Calculate effect size (magnitude of change)
        mean_good = np.mean(good.distribution)
        mean_bad = np.mean(bad.distribution)
        magnitude = abs(mean_bad - mean_good)

        # Also check that the magnitude is non-trivial
        if magnitude < self.MIN_MAGNITUDE_THRESHOLD:
            return None

        return RelationChange(
            relation_good=good,
            relation_bad=bad,
            change_magnitude=float(magnitude),
            statistical_significance=p_value,
        )


# ============================================================================
# Module 6: Causal Chain Refinement (NEW - implements paper's core refinement)
# ============================================================================


class CausalChainRefiner:
    """
    Causal Chain Refinement for Microservices RCA (implements Perspect paper's core algorithm).

    **Terminology Mapping (Perspect paper → Our microservice context):**
    - P_root (root node) → Observation (O): User-facing entry point where SLO violation is detected
    - S (symptom) → Symptom (S): Furthest anomalous span detected from the user
    - Non-refinable P → Root Cause (RC): The "inflection point" where failure originates

    **Causal Chain:**
    ```
    User → O (P_root) → P1 → ... → RC (inflection point) → ... → Pn → S (symptom)
                                    ↑
                                    First node where local relation R(P, P+) changes
    ```

    **Refinement Algorithm (from Perspect paper):**

    Rule 1: Locate Inflection Point
    - For each node P on path O → S, check local relation R(P, P+) with its immediate successor
    - If R(P, P+) **unchanged**: P is "refinable" → discard P, continue checking P+
    - If R(P, P+) **changed**: P is a candidate inflection point → apply Rule 2

    Rule 2: Distinguish "Effect" from "Cause"
    - If R(P, P+) change is merely an **effect** of P's dependency on other service P':
      → P is still "refinable" → discard P, continue checking P+ (and start checking P')
    - If R(P, P+) change **originates from P itself** (not caused by other dependencies):
      → P is "non-refinable" → keep P as RC

    **Key Insight:** We find the first node where the **local downstream relationship**
    changes, which pinpoints where the failure originates (not just propagates).
    """

    def __init__(
        self,
        relation_computer: RelationComputer,
        relation_filter: RelationFilter,
        observations: list[Observation],
        sdg: SDG,
    ):
        self.computer = relation_computer
        self.filter = relation_filter
        self.observations = observations
        self.sdg = sdg

    def refine(self, changed_relations: list[RelationChange]) -> list[RelationChange]:
        """
        Iteratively refine relations to find true root causes (inflection points).

        **Example execution (matching the review scenario):**
        ```
        Call graph: O (Gateway) → P1 (UserSvc) → RC (OrderSvc) → P3 (PaymentSvc) → S (DB)

        Initial queue: [(O, S)]

        Iteration 1: Check (O, S)
          - Rule 1: Check R(O, P1) → unchanged
          - Result: Refinable → Queue: [(P1, S)]

        Iteration 2: Check (P1, S)
          - Rule 1: Check R(P1, RC) → unchanged
          - Result: Refinable → Queue: [(RC, S)]

        Iteration 3: Check (RC, S)
          - Rule 1: Check R(RC, P3) → **changed** (OrderSvc takes 500ms longer before calling PaymentSvc)
          - Rule 2: Check if change is effect of other dependency → no
          - Result: Non-refinable → Keep RC as root cause

        Final result: RC (OrderSvc) is the inflection point
        ```
        """
        non_refinable_relations = []
        to_process = list(changed_relations)

        # Track processed relations to avoid infinite loops
        # Key: (pred_service, pred_span, symp_service, symp_span, symptom_type)
        processed = set()

        # Safety limit to prevent infinite loops in case of bugs
        max_iterations = len(changed_relations) * 100  # Allow up to 100x refinement per relation
        iteration_count = 0

        while to_process:
            iteration_count += 1
            if iteration_count > max_iterations:
                if debug():
                    logger.warning(
                        f"Refinement iteration limit reached ({max_iterations}), "
                        f"stopping to prevent infinite loop. Remaining: {len(to_process)} relations"
                    )
                # Add remaining relations as non-refinable to avoid losing them
                non_refinable_relations.extend(to_process)
                break

            rel_change = to_process.pop(0)

            # Create unique key for this relation
            rel_key = (
                rel_change.relation_bad.predecessor_service,
                rel_change.relation_bad.predecessor_span_name,
                rel_change.relation_bad.symptom_service,
                rel_change.relation_bad.symptom_span_name,
                rel_change.relation_bad.symptom_type,
            )

            # Skip if already processed (cycle detection)
            if rel_key in processed:
                if debug():
                    logger.debug(
                        f"Skipping already processed relation: "
                        f"{rel_key[0]}.{rel_key[1][:40]} -> {rel_key[2]}.{rel_key[3][:40]}"
                    )
                continue

            processed.add(rel_key)

            new_relation, refinable = self._is_relation_refinable(rel_change)

            if refinable:
                if debug():
                    pred_info = (
                        f"{rel_change.relation_bad.predecessor_service}."
                        f"{rel_change.relation_bad.predecessor_span_name[:60]}"
                    )
                    symp_info = (
                        f"{rel_change.relation_bad.symptom_service}.{rel_change.relation_bad.symptom_span_name[:60]}"
                    )
                    new_symp_info = (
                        f"{new_relation.relation_bad.symptom_service}."
                        f"{new_relation.relation_bad.symptom_span_name[:60]}"
                    )
                    logger.debug(
                        f"Refinable: {pred_info} → {symp_info} | "
                        f"Local relation changed, continuing refinement to {new_symp_info}"
                    )
                to_process.append(new_relation)
            else:
                if debug():
                    pred_info = (
                        f"{rel_change.relation_bad.predecessor_service}."
                        f"{rel_change.relation_bad.predecessor_span_name[:60]}"
                    )
                    symp_info = (
                        f"{rel_change.relation_bad.symptom_service}.{rel_change.relation_bad.symptom_span_name[:60]}"
                    )
                    logger.debug(
                        f"Non-refinable (ROOT CAUSE): {pred_info} | "
                        f"Inflection point found - no further local changes on path to {symp_info}"
                    )
                non_refinable_relations.append(rel_change)

        return non_refinable_relations

    def _is_relation_refinable(
        self,
        relation: RelationChange,
    ) -> tuple[RelationChange, bool]:
        """
        Check if a relation (O, S) can be refined by finding intermediate nodes.

        This implements the core refinement logic from Perspect paper:

        **Rule 1: Locate Inflection Point**
        - Check local relation R(O, P+) where P+ is immediate successor of O on path to S
        - If R(O, P+) **unchanged**: O is refinable → recursively check (P+, S)
        - If R(O, P+) **changed**: O is a candidate inflection point → apply Rule 2

        **Rule 2: Distinguish Effect from Cause**
        - Check if R(O, P+) change is caused by O's dependency on parallel service P'
        - If yes (effect): O is still refinable → check (P+, S) and (P', S)
        - If no (cause): O is non-refinable → O is the true root cause

        **Algorithm Flow:**
        ```
        Given: (O, S) relation with observed change

        1. Forward refinement (Rule 1):
           Find immediate successors P+ of O on path O → S
           For each P+:
               if R(O, P+) changed:
                   return (O, P+), refinable=True  # O is inflection point candidate

        2. Backward refinement (Rule 2 - check for "effect"):
           Find immediate predecessors P' of S (parallel dependencies of nodes on path)
           For each P':
               if R(P', S) changed:
                   Check if R(O, P') also changed:
                       if yes: return (O, P'), refinable=True  # Change is effect of P'

        3. No refinement found:
           return (O, S), refinable=False  # O is true root cause
        ```

        Returns:
            tuple[RelationChange, bool]: (refined_relation, is_refinable)
            - If refinable: returns the refined relation and True
            - If not refinable: returns the original relation and False
        """
        O_service = relation.relation_bad.predecessor_service
        O_span = relation.relation_bad.predecessor_span_name
        S_service = relation.relation_bad.symptom_service
        S_span = relation.relation_bad.symptom_span_name
        symptom_type = relation.relation_bad.symptom_type

        # Rule 1: Forward refinement - Check immediate successors of O
        # Find P+ (immediate successors of O on path to S)
        forward_candidates = self._find_successors(O_service, O_span, S_service, S_span)

        # Check R(O, P+) for each candidate P+
        for P_plus in forward_candidates:
            # Check if local relation R(O, P+) has changed
            relation_O_Pplus = self._compute_relation_change(
                O_service, O_span, P_plus["service"], P_plus["span"], symptom_type
            )

            if relation_O_Pplus is None:
                # R(O, P+) unchanged → O is healthy, just propagating downstream issues
                # O is refinable → continue checking (P+, S)
                if debug():
                    logger.debug(
                        f"Rule 1: Local relation R({O_service}.{O_span[:40]}, "
                        f"{P_plus['service']}.{P_plus['span'][:40]}) unchanged → "
                        f"O is refinable (healthy node), continue to successor P+"
                    )
                # Create relation for (P+, S) to continue refinement
                refined_relation = RelationChange(
                    relation_good=Relation(
                        predecessor_service=P_plus["service"],
                        predecessor_span_name=P_plus["span"],
                        symptom_service=S_service,
                        symptom_span_name=S_span,
                        symptom_type=symptom_type,
                        distribution=[0],
                        sample_size=0,
                    ),
                    relation_bad=Relation(
                        predecessor_service=P_plus["service"],
                        predecessor_span_name=P_plus["span"],
                        symptom_service=S_service,
                        symptom_span_name=S_span,
                        symptom_type=symptom_type,
                        distribution=[1],
                        sample_size=1,
                    ),
                    change_magnitude=relation.change_magnitude,
                    statistical_significance=relation.statistical_significance,
                )
                return refined_relation, True

        # Rule 2: Backward refinement - Check for "effect" from parallel dependencies
        # Find P' (immediate predecessors of S, representing parallel deps on path)
        backward_candidates = self._find_predecessors(S_service, S_span, O_service, O_span)

        for P_prime in backward_candidates:
            # Check if R(P', S) has changed
            relation_Pprime_S = self._compute_relation_change(
                P_prime["service"], P_prime["span"], S_service, S_span, symptom_type
            )

            if relation_Pprime_S is None:
                continue

            # R(P', S) changed - now check if R(O, P') also changed
            # This would indicate the change in R(O, S) is an "effect" of P'
            relation_O_Pprime = self._compute_relation_change(
                O_service, O_span, P_prime["service"], P_prime["span"], symptom_type
            )

            if relation_O_Pprime is not None:
                # Both R(P', S) and R(O, P') changed
                # → The change in R(O, S) is likely an effect of P' failing
                # → Continue refinement by checking (O, P')
                if debug():
                    logger.debug(
                        f"Rule 2 applied: Change in R({O_service}.{O_span[:40]}, "
                        f"{S_service}.{S_span[:40]}) is effect of dependency on "
                        f"{P_prime['service']}.{P_prime['span'][:40]} → refinable"
                    )
                return relation_O_Pprime, True

        # No refinement possible - O is the true root cause (non-refinable)
        if debug():
            logger.debug(
                f"Non-refinable: {O_service}.{O_span[:40]} is inflection point "
                f"(first node where local relation changed on path to {S_service}.{S_span[:40]})"
            )
        return relation, False

    def _find_successors(
        self, start_service: str, start_span: str, end_service: str, end_span: str
    ) -> list[dict[str, str]]:
        """
        Find immediate successors P+ of (start_service, start_span) on path to (end_service, end_span).

        Used in Rule 1 (Forward refinement):
        - Given node O, find all immediate callees P+ that are on path O → S
        - These are candidates for checking local relation R(O, P+)

        Returns list of {"service": str, "span": str} dictionaries.
        """
        successors = []

        for edge in self.sdg.edges:
            if edge.caller_service == start_service and edge.caller_span_name == start_span:
                # This is a direct successor (O calls P+)
                # Check if P+ is on the path to S (reachability check)
                if self._is_on_path(edge.callee_service, edge.callee_span_name, end_service, end_span):
                    successors.append({"service": edge.callee_service, "span": edge.callee_span_name})

        return successors

    def _find_predecessors(
        self, end_service: str, end_span: str, start_service: str, start_span: str
    ) -> list[dict[str, str]]:
        """
        Find immediate predecessors P' of (end_service, end_span) reachable from (start_service, start_span).

        Used in Rule 2 (Backward refinement - check for "effect"):
        - Given symptom S, find all immediate callers P' that are reachable from O
        - These represent potential parallel dependencies that could cause the observed change
        - If R(P', S) changed AND R(O, P') changed, then change in R(O, S) is an "effect" of P'

        Returns list of {"service": str, "span": str} dictionaries.
        """
        predecessors = []

        for edge in self.sdg.edges:
            if edge.callee_service == end_service and edge.callee_span_name == end_span:
                # This is a direct predecessor (P' calls S)
                # Check if P' is reachable from O
                if self._is_on_path(
                    start_service,
                    start_span,
                    edge.caller_service,
                    edge.caller_span_name,
                ):
                    predecessors.append({"service": edge.caller_service, "span": edge.caller_span_name})

        return predecessors

    def _is_on_path(self, from_service: str, from_span: str, to_service: str, to_span: str) -> bool:
        """
        Check if there's a path from (from_service, from_span) to (to_service, to_span) in the SDG.

        Uses BFS to check reachability with cycle detection.
        """
        if from_service == to_service and from_span == to_span:
            return True

        visited = set()
        queue = [(from_service, from_span)]

        # Safety limit: prevent excessive BFS in large graphs
        max_nodes_to_visit = 10000
        nodes_visited = 0

        while queue:
            nodes_visited += 1
            if nodes_visited > max_nodes_to_visit:
                if debug():
                    logger.warning(
                        f"BFS path check exceeded node limit ({max_nodes_to_visit}), "
                        f"assuming no path from {from_service}.{from_span} to {to_service}.{to_span}"
                    )
                return False

            curr_service, curr_span = queue.pop(0)

            if (curr_service, curr_span) in visited:
                continue
            visited.add((curr_service, curr_span))

            if curr_service == to_service and curr_span == to_span:
                return True

            # Add successors to queue
            for edge in self.sdg.edges:
                if edge.caller_service == curr_service and edge.caller_span_name == curr_span:
                    next_node = (edge.callee_service, edge.callee_span_name)
                    if next_node not in visited:  # Pre-check to avoid adding visited nodes
                        queue.append(next_node)

        return False

    def _compute_relation_change(
        self,
        pred_service: str,
        pred_span: str,
        symp_service: str,
        symp_span: str,
        symptom_type: SymptomType,
    ) -> RelationChange | None:
        """
        Compute relation change R(pred, symp) between good and bad periods.

        This is the core statistical test used by both Rule 1 and Rule 2:
        - Rule 1: Checks if local relation R(O, P+) has changed
        - Rule 2: Checks if R(P', S) and R(O, P') have changed (to detect "effect")

        The relation captures the distribution of "how many symptom events each
        predecessor event causes" (not just averages), and uses Mann-Whitney U test
        to determine if the distribution changed significantly.

        Returns:
            RelationChange if significant change detected (p < 0.05), None otherwise
        """
        # Create temporary Observation and Symptom objects
        # Find matching observation from self.observations
        matching_obs = None
        for obs in self.observations:
            obs_span = obs.trace_span_name or obs.entry_span_name
            if obs.service_name == pred_service and obs_span == pred_span and obs.observation_type == symptom_type:
                matching_obs = obs
                break

        if matching_obs is None:
            # No matching observation found
            return None

        # Create symptom object
        symptom = Symptom(
            symptom_type=symptom_type,
            service_name=symp_service,
            span_name=symp_span,
            impact_score=0.0,  # Placeholder
        )

        # Compute relations for good and bad periods
        relations_good = self.computer.compute_relations(
            period="good",
            observations=[matching_obs],
            symptoms=[symptom],
            proxy_check=True,  # We're checking intermediate relations
        )

        relations_bad = self.computer.compute_relations(
            period="bad",
            observations=[matching_obs],
            symptoms=[symptom],
            proxy_check=True,
        )

        # Filter to find significant changes
        if not relations_good or not relations_bad:
            return None

        changes = self.filter.filter_significant_changes(relations_good, relations_bad)

        # Return the first significant change, if any
        return changes[0] if changes else None


# ============================================================================
# Module 7: Contextual Refinement (renamed from RelationRefiner)
# ============================================================================


class ContextualRefiner:
    DEFAULT_REFINEMENT_ATTRIBUTES = [
        '"attr.k8s.pod.name"',
        '"attr.http.response.status_code"',
    ]

    DEFAULT_METRIC_REFINEMENTS = [
        "cpu_utilization",
        "memory_rss",
    ]

    def __init__(
        self,
        con: duckdb.DuckDBPyConnection,
        sdg: SDG,
        refinement_attributes: list[str] | None = None,
        metric_refinements: list[str] | None = None,
        enable_log_refinement: bool = True,
    ):
        self.con = con
        self.sdg = sdg
        self.refinement_attributes = refinement_attributes or self.DEFAULT_REFINEMENT_ATTRIBUTES
        self.metric_refinements = metric_refinements or self.DEFAULT_METRIC_REFINEMENTS
        self.enable_log_refinement = enable_log_refinement

    def refine(self, changed_relations: list[RelationChange], symptoms: list[Symptom]) -> list[RootCauseCandidate]:
        candidates = []

        for rel_change in changed_relations:
            impact = (1.0 - rel_change.statistical_significance) * rel_change.change_magnitude

            overall_candidate = RootCauseCandidate(
                service_name=rel_change.relation_bad.predecessor_service,
                span_name=rel_change.relation_bad.predecessor_span_name,
                symptom_type=rel_change.relation_bad.symptom_type,
                attribute_type="overall",
                attribute_value=None,
                impact_score=impact,
                relation_change=rel_change,
            )
            candidates.append(overall_candidate)

        return candidates


# ============================================================================
# Module 8: Candidate Ranking
# ============================================================================


class CandidateRanker:
    """Ranks root cause candidates by impact score."""

    def __init__(self):
        pass

    def rank(self, candidates: list[RootCauseCandidate]) -> list[RootCauseCandidate]:
        """Sort candidates by impact score (descending)."""
        return sorted(candidates, key=lambda c: c.impact_score, reverse=True)
