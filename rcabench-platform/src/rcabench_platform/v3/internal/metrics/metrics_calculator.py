import os
import statistics
from typing import Any

import networkx as nx
from rcabench.openapi import (
    ApiClient,
    InjectionsApi,
    LabelItem,
    ManageInjectionLabelReq,
)

from ...sdk.datasets.spec import DatasetAnalyzer
from ...sdk.logging import logger
from ..clients.rcabench_ import get_rcabench_client


# TODO 多注入情况下的处理
class DatasetMetricsCalculator:
    def __init__(self, loader: DatasetAnalyzer):
        self.loader = loader
        self.graph = loader.get_service_dependency_graph()
        self.root_services = loader.get_root_services()[0]
        self.services = loader.get_all_services()

        assert self.root_services in self.graph, f"Service '{self.root_services}' not found in graph"

    def compute_sdd(self, k: int = 1) -> tuple[float, dict]:
        """
        Compute Service Distance to root cause (SDD@k).

        This metric measures the shortest path distance from the top-k services
        with the largest anomaly magnitude to the root cause services.
        The minimum distance among top-k services to any root cause is selected.

        Formula:
        $$SDD@k = \\min_{s \\in T_k} \\min_{r \\in R} d(s, r)$$

        Where:
        - $R$ = set of root cause services
        - $T_k$ = top-k services ranked by anomaly magnitude $\\Delta_s$
        - $d(s, r)$ = shortest path distance from service $s$ to root cause $r$
        - $\\Delta_s = \\sum_{m \\in M_s} \\frac{|\\bar{m}_{abnormal} - \\bar{m}_{normal}|}{|\\bar{m}_{normal}|}$
        - $M_s$ = set of golden signal metrics for service $s$
        - $\\bar{m}_{normal/abnormal}$ = mean value of metric $m$ in normal/abnormal period

        Interpretation:
        - SDD@k = 0: Root cause service is among the top-k services
        - SDD@k > 0: Root cause service is not among the top-k services

        Args:
            k (int): Number of top services to consider

        Returns:
            tuple[float, dict]: Distance(s) to root cause service(s), or
                tuple of (distance, details) if return_details=True
        """
        if not self.root_services or not self.graph:
            raise

        # Calculate anomaly magnitude for each service with detailed tracking
        service_deltas = {}
        service_metric_contributions = {}  # Track metric contributions for each service

        for service in self.services:
            normal_metrics = self.loader.get_service_metrics(service, abnormal=False)
            abnormal_metrics = self.loader.get_service_metrics(service, abnormal=True)

            delta = 0.0
            metric_contributions = []  # List of (metric_name, contribution) tuples

            for metric in abnormal_metrics:
                if metric in normal_metrics:
                    v1 = normal_metrics[metric]
                    v2 = abnormal_metrics[metric]
                    if v1 and v2:
                        # Combine normal and abnormal values for min-max normalization
                        all_values = v1 + v2
                        min_val = min(all_values)
                        max_val = max(all_values)

                        # Perform min-max normalization if there's variance in the data
                        if max_val != min_val:
                            normalized_v1 = [(x - min_val) / (max_val - min_val) for x in v1]
                            normalized_v2 = [(x - min_val) / (max_val - min_val) for x in v2]
                        else:
                            # If all values are the same, no normalization needed
                            normalized_v1 = v1
                            normalized_v2 = v2

                        # Calculate contribution using normalized values
                        mean_normalized_v1 = sum(normalized_v1) / len(normalized_v1)
                        mean_normalized_v2 = sum(normalized_v2) / len(normalized_v2)
                        contribution = abs(mean_normalized_v2 - mean_normalized_v1)
                        delta += contribution
                        metric_contributions.append((metric, contribution))

            service_deltas[service] = delta
            # Sort metrics by contribution and keep top 5
            metric_contributions.sort(key=lambda x: x[1], reverse=True)
            service_metric_contributions[service] = metric_contributions[:5]

        # Select top-k services
        topk_services = sorted(service_deltas, key=lambda x: service_deltas[x], reverse=True)[:k]

        # Calculate distances - find minimum distance from any top-k service to any root cause
        service_min_distances = []

        for service in topk_services:
            min_distance = 999
            distance_details = {}

            try:
                d = nx.shortest_path_length(self.graph, service, self.root_services)
                min_distance = min(min_distance, d)
                distance_details[self.root_services] = d
            except Exception:
                distance_details[self.root_services] = "unreachable"
                continue

            service_min_distances.append(min_distance if min_distance < float("inf") else 999)

        # SDD@k is the minimum distance among all top-k services
        # Handle case where no valid distances are found
        sdd_value = min(service_min_distances) if service_min_distances else 999

        details = {"top_k_services": [], "sdd_value": sdd_value}

        for i, service in enumerate(topk_services):
            # Get distance to root, handle case where service_min_distances might be shorter than topk_services
            distance_to_root = service_min_distances[i] if i < len(service_min_distances) else 9999

            service_info = {
                "service_name": service,
                "rank": i + 1,
                "total_anomaly_magnitude": service_deltas[service],
                "distance_to_root": distance_to_root,
                "top_contributing_metrics": [],
            }

            # Add top contributing metrics with their contributions
            for metric_name, contribution in service_metric_contributions[service]:
                percentage = (contribution / service_deltas[service] * 100) if service_deltas[service] > 0 else 0
                service_info["top_contributing_metrics"].append(
                    {
                        "metric_name": metric_name,
                        "contribution": contribution,
                        "contribution_percentage": percentage,
                    }
                )

            details["top_k_services"].append(service_info)

        return sdd_value, details

    def compute_ac(self, service_name: str | None = None) -> dict[str, int]:
        """
        Compute Anomaly Cardinality (AC).

        This metric counts the number of golden signal metrics that are detected
        as anomalous for each service during the fault injection period.

        Formula:
        $$AC_s = |\\{m \\in M_s : \\text{isAnomalous}(m)\\}|$$

        Where:
        - $AC_s$ = anomaly cardinality for service $s$
        - $M_s$ = set of golden signal metrics for service $s$
        - $\\text{isAnomalous}(m)$ = anomaly detection function for metric $m$
        - A metric is considered anomalous if:
            $|z| = \\frac{|\\bar{m}_{abnormal} - \\bar{m}_{normal}|}{\\sigma_{normal}} > \\theta$
        - $\\theta = 2.0$ (z-score threshold, approximately 95% confidence level)
        - $\\sigma_{normal}$ = standard deviation of metric $m$ in normal period
        - $\\bar{m}_{normal/abnormal}$ = mean value of metric $m$ in normal/abnormal period

        Args:
            service_name (str | None): Specific service name, or None for all services

        Returns:
            dict[str, int]: Mapping from service name to anomaly cardinality
        """
        result = {}
        services = [service_name] if service_name else self.services

        for service in services:
            normal_metrics = self.loader.get_service_metrics(service, abnormal=False)
            abnormal_metrics = self.loader.get_service_metrics(service, abnormal=True)

            anomaly_count = 0

            # Check each metric for anomalies
            for metric in abnormal_metrics:
                if metric in normal_metrics:
                    normal_values = normal_metrics[metric]
                    abnormal_values = abnormal_metrics[metric]

                    if normal_values and abnormal_values:
                        normal_mean = statistics.mean(normal_values)
                        normal_std = statistics.stdev(normal_values) if len(normal_values) > 1 else 0.0
                        abnormal_mean = statistics.mean(abnormal_values)

                        # Use z-score for anomaly detection: z > 2.0 threshold (approximately 95% confidence)
                        if normal_std > 0:
                            z_score = abs(abnormal_mean - normal_mean) / normal_std
                            if z_score > 2.0:  # z-score threshold
                                anomaly_count += 1
                        elif normal_mean != abnormal_mean:  # No variance in normal period but values differ
                            anomaly_count += 1

            result[service] = anomaly_count

        return result

    def compute_cpl(self) -> float:
        """
        Compute Causal Path Length (CPL).

        This metric measures the shortest path length from root cause services
        to the entry service of the system. When multiple root cause services
        exist, the maximum path length is selected.

        Formula:
        $$CPL = \\max_{r \\in R} d(r, e)$$

        Where:
        - $CPL$ = causal path length
        - $R$ = set of root cause services
        - $e$ = entry service (typically the load generator service)
        - $d(r, e)$ = shortest path distance from root cause $r$ to entry service $e$

        Returns:
            float: Maximum causal path length among all root cause services
        """
        if not self.root_services or not self.graph:
            return 0.0

        entry_service = self.loader.get_entry_service()
        if not entry_service:
            return 0.0

        if entry_service not in self.graph:
            logger.warning(f"Entry service '{entry_service}' not found in graph")
            return 0.0

        try:
            path_length = nx.shortest_path_length(self.graph, entry_service, self.root_services)
            return path_length
        except nx.NetworkXNoPath:
            logger.warning(f"No path from entry service '{entry_service}' to root service '{self.root_services}'")
            return float("inf")
        except Exception as e:
            logger.warning(f"Error computing path length: {e}")
            return 0.0

    def get_root_cause_degree(self) -> int | None:
        """
        Get the root cause service with maximum degree.

        Formula:
        $$r^* = \\arg\\max_{r \\in R} \\deg(r)$$

        Where:
        - $r^*$ = root cause service with maximum degree
        - $R$ = set of root cause services
        - $\\deg(r)$ = degree of service $r$ in the dependency graph

        Returns:
            str | None: Service name with maximum degree, or None if no root services exist
        """
        if not self.root_services or not self.graph:
            return None

        max_degree = -1

        if self.root_services in self.graph:
            degree = int(self.graph.degree[self.root_services])  # type: ignore
            if degree > max_degree:
                max_degree = degree

        return max_degree

    def calculate_and_report(self, injection_id: int = 0, client: ApiClient | None = None) -> dict[str, Any]:
        results: dict[str, Any] = {}
        results["SDD@1"] = self.compute_sdd(k=1)
        results["SDD@3"] = self.compute_sdd(k=3)
        results["SDD@5"] = self.compute_sdd(k=5)
        results["AC"] = self.compute_ac()
        results["CPL"] = self.compute_cpl()
        results["RootServiceDegree"] = self.get_root_cause_degree()

        if injection_id > 0:
            if client is None:
                client = get_rcabench_client(base_url=os.environ.get("RCABENCH_BASE_URL"))
            api = InjectionsApi(client)
            datapack_name = self.loader.get_datapack()

            api.manage_injection_labels(
                id=injection_id,
                manage=ManageInjectionLabelReq(
                    add_labels=[
                        LabelItem(key="SDD@1", value=str(results["SDD@1"][0])),
                        LabelItem(key="SDD@3", value=str(results["SDD@3"][0])),
                        LabelItem(key="SDD@5", value=str(results["SDD@5"][0])),
                        LabelItem(key="CPL", value=str(results["CPL"])),
                        LabelItem(
                            key="RootServiceDegree",
                            value=str(results["RootServiceDegree"]),
                        ),
                    ]
                ),
            )
            logger.info(f"Updated metrics labels for {datapack_name}")

        return results
