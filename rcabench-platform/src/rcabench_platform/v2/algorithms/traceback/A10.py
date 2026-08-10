"""
TraceBackA10: Relational Debugging for Microservices (Refactored V2)

Based on the OSDI'23 paper "Relational Debugging" (Perspect), this algorithm implements
a fully data-driven approach to microservice RCA.

Core Concepts (aligned with Perspect paper):
1. Observation (P): Entry-level SLO violations (from conclusion.parquet)
   - These are high-level symptoms visible to end users
   - Example: "GET /api/orders endpoint has high latency"
2. Symptom (S): Internal spans that CAUSE the observation (found via bootstrapping)
   - These are the actual problematic operations inside the system
   - Example: "database query in order-service is slow"
3. Relation R(S|P): Distribution of "how many symptom S events appear per observation P"
   - Maps entry-level violations to internal root causes
   - Distribution captures heterogeneity (not just averages)

Pipeline (strictly following the paper):
1. Load: Load traces, metrics, logs, and conclusion.parquet (observations)
2. Detect Observations: Identify entry-level SLO violations (ObservationDetector)
3. Bootstrap Symptoms: Find internal spans causing the observations (SymptomBootstrapper)
   - For LATENCY: Find spans with max exclusive duration increase
   - For ERROR: Find deepest error-generating spans
4. Compute Relations: Calculate R(S|P) as distributions for both good/bad periods
   - P = Observations (entry-level SLO violations)
   - S = Symptoms (internal problematic spans)
   - Distribution = [# of S events in same trace as each P event]
5. Filter: Use Mann-Whitney U test to find statistically significant relation changes
6. Causal Chain Refinement (NEW): Find inflection points (true roots vs victims)
   - Distinguishes cascading failures from direct failures
   - Eliminates "victim" nodes that only changed because of upstream changes
7. Contextual Refinement: Partition by attributes (static, metrics, logs) using statistical tests
8. Rank: Sort by statistical impact scores (no arbitrary weights)

Key Fix from V1:
- V1 ERROR: Treated entry spans as "symptoms S" → no predecessors found
- V2 FIX: Added SymptomBootstrapper to find INTERNAL symptoms from entry observations
  This aligns with the paper's "symptom bootstrapping" step where we find the
  actual problematic events (like malloc/mark) from high-level observations (like heap_size).

Dynamic Context Refinement (Metrics & Logs as Context):
- Metrics: ASOF JOIN to enrich spans with runtime metrics (CPU, memory)
- Logs (Gold Standard): Direct trace_id/span_id join for causal linking
- Logs (Fallback): Pod-level correlation when trace_id unavailable

Network Blindspot Detection (NEW in V3):
- Silent Callee Pattern: Detects network-layer faults via relational contradiction
  * R(error|P→S) high: P sees many errors when calling S
  * R(error|S) low: S's own spans show few errors
  * Contradiction indicates unobservable network-layer fault (response manipulation, packet loss, etc.)
  * Attribution: Since network is unobservable, attribute to P (last observable point)
- No domain-specific knowledge required - pure relational reasoning from Perspect paper
- Handles: Response Code Manipulation, Timeout Injection, Service Mesh failures, etc.
"""

import json
from collections import defaultdict
from dataclasses import dataclass
from enum import Enum, auto
from pathlib import Path

import duckdb
import numpy as np
from scipy import stats

from ...logging import logger, timeit
from ...utils.env import debug
from ..spec import Algorithm, AlgorithmAnswer, AlgorithmArgs
from .a10.bootstrap import ObservationDetector, SymptomBootstrapper
from .a10.dataloader import DataLoader, SDGBuilder
from .a10.model import RootCauseCandidate
from .a10.refiner import (
    CandidateRanker,
    CausalChainRefiner,
    ContextualRefiner,
    RelationComputer,
    RelationFilter,
)


class TraceBackA10(Algorithm):
    # CPU configuration
    DEFAULT_CPU_COUNT = 8

    # Debug output limits
    DEBUG_TOP_ANSWERS_COUNT = 10
    DEBUG_TOP_CANDIDATES_COUNT = 10

    # Services to exclude from root cause analysis (load generators, test clients, etc.)
    EXCLUDED_SERVICES = {"loadgenerator"}

    def needs_cpu_count(self) -> int | None:
        return self.DEFAULT_CPU_COUNT

    def __call__(self, args: AlgorithmArgs) -> list[AlgorithmAnswer]:
        logger.info(f"Processing datapack: {args.datapack}")

        # Initialize DuckDB connection
        con = duckdb.connect(":memory:")

        try:
            # Step 1: Load data into DuckDB
            loader = DataLoader(args.input_folder, con)
            loader.load_all()

            # Step 2: Build Service Dependency Graph (SDG) from traces
            sdg_builder = SDGBuilder(con)
            sdg = sdg_builder.build()

            if debug():
                logger.debug(f"SDG: {len(sdg.services)} services, {len(sdg.edges)} edges")

            # Step 3: Detect observations (entry-level SLO violations)
            observation_detector = ObservationDetector(con)
            observations = observation_detector.detect()

            if not observations:
                logger.warning("No observations detected from conclusion.parquet")
                return []

            if debug():
                logger.debug(f"Detected {len(observations)} observations (entry-level SLO violations)")

            symptom_bootstrapper = SymptomBootstrapper(con, sdg)
            symptoms = symptom_bootstrapper.bootstrap(observations)

            if not symptoms:
                logger.warning("No internal symptoms found via bootstrapping")
                return []

            if debug():
                logger.debug(f"Bootstrapped {len(symptoms)} internal symptoms")
                for s in symptoms[:5]:
                    logger.debug(
                        f"  Symptom: {s.service_name}.{s.span_name[:60]} "
                        f"({s.symptom_type.name}, impact={s.impact_score:.3f})"
                    )

            # Step 5: Compute relations R(S|P) for both periods
            # P = Observations (entry-level SLO violations)
            # S = Symptoms (internal spans causing the violations)
            relation_computer = RelationComputer(con, sdg)
            relations_good = relation_computer.compute_relations(
                period="good", observations=observations, symptoms=symptoms
            )
            relations_bad = relation_computer.compute_relations(
                period="bad", observations=observations, symptoms=symptoms
            )

            # Step 6: Filter relations with significant changes
            relation_filter = RelationFilter()
            changed_relations = relation_filter.filter_significant_changes(relations_good, relations_bad)

            if debug():
                logger.debug(f"Found {len(changed_relations)} significantly changed relations")

            # Step 7: Causal Chain Refinement (NEW - filters victims, keeps inflection points)
            # This distinguishes cascading failures from direct failures
            chain_refiner = CausalChainRefiner(relation_computer, relation_filter, observations, sdg)
            inflection_point_relations = chain_refiner.refine(changed_relations)

            if debug():
                logger.debug(f"Found {len(inflection_point_relations)} causal inflection points (true sources)")

            # Step 8: Convert inflection point relations to root cause candidates
            root_cause_candidates = []
            for rel_change in inflection_point_relations:
                impact = (1.0 - rel_change.statistical_significance) * rel_change.change_magnitude
                candidate = RootCauseCandidate(
                    service_name=rel_change.relation_bad.predecessor_service,
                    span_name=rel_change.relation_bad.predecessor_span_name,
                    symptom_type=rel_change.relation_bad.symptom_type,
                    attribute_type="overall",
                    attribute_value=None,
                    impact_score=impact,
                    relation_change=rel_change,
                )
                root_cause_candidates.append(candidate)

            # Step 9: Rank root cause candidates
            ranker = CandidateRanker()
            ranked_candidates = ranker.rank(root_cause_candidates)

            # Step 10: Convert to service-level answers
            answers = self._to_answers(ranked_candidates)

            for ans in answers[: self.DEBUG_TOP_ANSWERS_COUNT]:
                logger.debug(f"Rank {ans.rank}: {ans.level}.{ans.name}")

            return answers

        finally:
            con.close()

    def _to_answers(self, candidates: list["RootCauseCandidate"]) -> list[AlgorithmAnswer]:
        service_scores = defaultdict(float)

        for candidate in candidates:
            if candidate.service_name in self.EXCLUDED_SERVICES:
                continue

            service_scores[candidate.service_name] += candidate.impact_score

        sorted_services = sorted(service_scores.items(), key=lambda x: x[1], reverse=True)

        if debug():
            logger.debug("Top root cause candidates:")
            for service_name, score in sorted_services[: self.DEBUG_TOP_CANDIDATES_COUNT]:
                logger.debug(f"  {service_name}: score={score:.4f}")

        answers = []
        for rank, (service_name, _score) in enumerate(sorted_services, start=1):
            answers.append(AlgorithmAnswer(level="service", name=service_name, rank=rank))

        return answers
