import json
import math
import uuid
from collections.abc import Callable
from pathlib import Path
from typing import Any

from ....evaluation.causal_graph import CausalGraph
from ....evaluation.rca_metrics import evaluate_graphs
from ...config import EvalConfig
from ..data import EvaluationSample
from .base_match_processor import BaseMatchProcesser
from .prompts import AUGMENTATION_PROMPTS


class RCABenchProcesser(BaseMatchProcesser):
    """Processer for RCABench evaluation.

    RCABench (Root Cause Analysis Benchmark) is designed to evaluate
    agent capabilities in systematic problem analysis and root cause identification
    using keyword matching and structural analysis.
    """

    name: str = "RCABench"

    def __init__(
        self,
        config: EvalConfig,
        source_path_fn: Callable[[str], str | Path] | None = None,
    ) -> None:
        super().__init__(config)
        self.source_path_fn = source_path_fn

    def preprocess_one(self, sample: EvaluationSample) -> EvaluationSample:
        """Preprocess a sample by dynamically generating symlink and question.

        This method:
        1. Creates a symlink from the original data directory to a random path
        2. Reads alarm_nodes from causal_graph.json to extract SLO-violated endpoints
        3. Formats the question using the RCABench template
        4. Updates the sample's meta with the dynamic path

        Args:
            sample: The evaluation sample to preprocess.

        Returns:
            The preprocessed sample with augmented_question and updated meta.
        """
        assert sample.meta is not None
        meta = dict(sample.meta)

        # Resolve source data directory: fn takes priority, then meta fields
        if self.source_path_fn is not None:
            source_data_dir = str(self.source_path_fn(sample.source))
        else:
            source_data_dir = meta.get("source_data_dir") or meta.get("path")
        if not source_data_dir:
            raise ValueError(f"Sample {sample.id} has no source_data_dir or path in meta")

        source_path = Path(source_data_dir).expanduser()

        if not source_path.exists() or not source_path.is_dir():
            raise ValueError(f"Source data directory does not exist or is not a directory: {source_path}")

        # Create eval-data directory with exp_id subdirectory
        eval_data_dir = Path("eval-data") / sample.exp_id
        eval_data_dir.mkdir(parents=True, exist_ok=True)

        # Generate random name for symlink to prevent model hack
        random_name = f"data_{uuid.uuid4().hex[:8]}"
        symlink_path = eval_data_dir / random_name

        # Remove existing symlink if present
        if symlink_path.exists() or symlink_path.is_symlink():
            symlink_path.unlink()

        # Create symlink to original data directory
        symlink_path.symlink_to(source_path.absolute(), target_is_directory=True)

        # Extract alarm endpoints from causal_graph.json
        alarm_endpoints = self._extract_alarm_endpoints(source_path)
        meta["alarm_endpoints"] = alarm_endpoints

        # Format reports for the question
        formatted_reports = "\n".join(f"- {endpoint}" for endpoint in alarm_endpoints)

        # Use template to generate question
        template_key = "RCABench" if "RCABench" in AUGMENTATION_PROMPTS else "default"

        if template_key == "RCABench":
            # RCABench template uses {reports} and {directory_path}
            augmented_question = AUGMENTATION_PROMPTS[template_key].format(
                reports=formatted_reports,
                directory_path=str(symlink_path.absolute()),
            )
        else:
            # Fallback to default template
            augmented_question = AUGMENTATION_PROMPTS[template_key].format(
                question=sample.raw_question, directory_path=str(symlink_path.absolute())
            )

        # Update meta with dynamic path (for judge_one to use)
        meta["path"] = str(symlink_path.absolute())

        sample.update(
            augmented_question=augmented_question,
            meta=meta,
        )
        return sample

    @staticmethod
    def _extract_endpoint_from_component(component: str) -> str | None:
        """Extract HTTP endpoint from a causal graph component identifier.

        Handles component formats like:
        - "span|loadgenerator::HTTP POST http://ts-ui-dashboard:8080/api/v1/..."
        - "span|ts-ui-dashboard::POST /api/v1/..."
        - "span|HTTP POST http://ts-ui-dashboard:8080/api/v1/..."  (no :: separator)
        - "span| http://ts-ui-dashboard:8080/api/v1/..."  (no :: separator, no method)

        Args:
            component: Component identifier string.

        Returns:
            Extracted endpoint string (e.g. "HTTP POST http://..."), or None if
            the component does not represent an HTTP endpoint.
        """
        if "::" in component:
            # Standard format: "span|service::ENDPOINT"
            endpoint = component.split("::", 1)[1].strip()
        elif "|" in component:
            # Fallback format: "span|ENDPOINT" (no :: separator)
            endpoint = component.split("|", 1)[1].strip()
        else:
            return None

        # Only keep entries that look like HTTP endpoints
        if not any(
            endpoint.startswith(method)
            for method in ("HTTP ", "GET ", "POST ", "PUT ", "DELETE ", "PATCH ", "http://", "https://")
        ):
            return None

        return endpoint

    @staticmethod
    def _extract_alarm_endpoints(source_path: Path) -> list[str]:
        """Extract alarm endpoint descriptions from causal_graph.json.

        Reads the alarm_nodes from causal_graph.json and extracts user-facing
        HTTP endpoints that are experiencing SLO violations.

        Falls back to scanning all nodes for loadgenerator spans if alarm_nodes
        is empty, and returns a generic message if no endpoints are found.

        Args:
            source_path: Path to the data directory containing causal_graph.json.

        Returns:
            List of endpoint description strings.
        """
        causal_graph_file = source_path / "causal_graph.json"
        if not causal_graph_file.exists():
            raise ValueError(f"causal_graph.json not found in {source_path}")

        with open(causal_graph_file) as f:
            cg_data = json.load(f)

        graph = CausalGraph.from_dict(cg_data)

        # Primary: use alarm_nodes
        endpoints: list[str] = []
        seen: set[str] = set()

        candidates = (
            graph.alarm_nodes
            if graph.alarm_nodes
            else [n for n in graph.nodes if n.component.startswith("span|loadgenerator::")]
        )

        for node in candidates:
            endpoint = RCABenchProcesser._extract_endpoint_from_component(node.component)
            if endpoint and endpoint not in seen:
                seen.add(endpoint)
                endpoints.append(endpoint)

        if not endpoints:
            raise ValueError(f"No valid endpoints found in {source_path}")

        return endpoints

    async def judge_one(self, sample: EvaluationSample) -> EvaluationSample:
        data = sample
        response = data.response or ""
        correct_answer = data.correct_answer or ""

        trajectories = self._load_trajectories(data.trajectories)
        (
            tool_success_count,
            tool_failure_count,
        ) = self._compute_tool_usage_stats(trajectories)
        tool_bonus = self._compute_tool_bonus(tool_success_count, tool_failure_count)

        # Parse CausalGraph from response
        agent_graph, parse_error = self._parse_causal_graph(response)

        # Evaluate the CausalGraph
        score, evaluation_details = self._evaluate_causal_graph(agent_graph, correct_answer)
        score_with_bonus = score + tool_bonus

        if isinstance(data.meta, dict):
            meta = dict(data.meta)
        else:
            meta = {"previous_meta": data.meta}

        meta["tool_usage"] = {
            "used": tool_success_count > 0,
            "success_count": tool_success_count,
            "failure_count": tool_failure_count,
            "bonus": tool_bonus,
            "base_score": score,
            "final_score": score_with_bonus,
        }

        meta["causal_graph_evaluation"] = evaluation_details
        meta["parse_error"] = parse_error

        # Load GT graph and compute new metrics
        gt_graph = self._load_gt_causal_graph(data)
        gt_root_cause_services = self._load_gt_root_cause_services(data)
        if agent_graph and gt_graph:
            # Check if GT has alarm_nodes before computing metrics
            if not gt_graph.get_alarm_services():
                data_path = data.meta.get("path") if isinstance(data.meta, dict) else None
                print(f"  ⚠ Warning: GT data missing alarm_nodes (sample: {data.source}, path: {data_path})")

            graph_result = await evaluate_graphs(agent_graph, gt_graph, gt_root_cause_services=gt_root_cause_services)
            meta["graph_metrics"] = graph_result.model_dump()

        # Print CausalGraph for debugging
        if agent_graph:
            print("\n" + "=" * 80)
            print("CAUSAL GRAPH EVALUATION RESULT")
            print("=" * 80)
            print(f"Sample ID: {data.id}")
            print(f"Correct Answer: {correct_answer}")
            print("\nAgent Graph:")
            print(f"  Nodes: {len(agent_graph.nodes)}")
            for node in agent_graph.nodes:
                print(f"    - {node.component}: {list(node.state)}")
            print(f"  Edges: {len(agent_graph.edges)}")
            for edge in agent_graph.edges:
                print(f"    - {edge.source} → {edge.target}")
            print(f"  Root Causes: {len(agent_graph.root_causes)}")
            for rc in agent_graph.root_causes:
                print(f"    - {rc.component}: {list(rc.state)}")
            print("\nEvaluation:")
            print(f"  Root Cause Services: {evaluation_details.get('root_cause_services', [])}")
            print(f"  Correct: {evaluation_details.get('correct', False)}")
            print(f"  Score: {score:.2f}")
            print(f"  Score with Bonus: {score_with_bonus:.2f}")
            print("=" * 80 + "\n")

        # Enrich reasoning with fault injection context
        base_reasoning = evaluation_details.get("reasoning") or ""
        injection = self._load_injection_json(data)
        if injection:
            injection_context = self._format_injection_context(injection)
            if injection_context:
                reasoning_str = f"{base_reasoning} | Injection: {injection_context}"
            else:
                reasoning_str = base_reasoning
        else:
            reasoning_str = base_reasoning

        data.update(
            judged_response=None,
            correct=evaluation_details.get("correct", False),
            confidence=score_with_bonus,
            reasoning=reasoning_str or None,
            extracted_final_answer=None,
            meta=meta,
        )

        return data

    @staticmethod
    def _load_trajectories(raw_trajectories: Any) -> list[dict[str, Any]]:
        if not raw_trajectories:
            return []

        if isinstance(raw_trajectories, str):
            try:
                parsed = json.loads(raw_trajectories)
            except json.JSONDecodeError:
                return []
        else:
            parsed = raw_trajectories

        # Span format: {"trajectories": [{"trajectory_id", "agent_name", "messages"}]}
        if isinstance(parsed, dict) and "trajectories" in parsed:
            flat_messages: list[dict[str, Any]] = []
            for traj in parsed.get("trajectories", []):
                if isinstance(traj, dict):
                    for msg in traj.get("messages", []):
                        if isinstance(msg, dict):
                            flat_messages.append(msg)
            return flat_messages

        # Legacy format: {"agent_trajectories": [...]}
        if isinstance(parsed, dict) and "agent_trajectories" in parsed:
            flat_messages = []
            for agent_traj in parsed.get("agent_trajectories", []):
                for turn in agent_traj.get("turns", []):
                    for msg in turn.get("messages", []):
                        if isinstance(msg, dict):
                            flat_messages.append(msg)
            return flat_messages

        # Legacy format: [{"agent": ..., "trajectory": [...]}]
        if isinstance(parsed, list):
            flat_messages = []
            for item in parsed:
                if not isinstance(item, dict):
                    continue
                if "trajectory" in item and isinstance(item["trajectory"], list):
                    flat_messages.extend(msg for msg in item["trajectory"] if isinstance(msg, dict))
                else:
                    flat_messages.append(item)
            return flat_messages

        return []

    @staticmethod
    def _compute_tool_usage_stats(trajectories: list[dict[str, Any]]) -> tuple[int, int]:
        assistant_call_ids: set[str] = set()
        tool_outcomes: dict[str, bool] = {}

        for message in trajectories:
            role = message.get("role")

            if role == "assistant":
                tool_calls = message.get("tool_calls") or []
                for tool_call in tool_calls:
                    call_id = tool_call.get("id")
                    if isinstance(call_id, str):
                        assistant_call_ids.add(call_id)

            if role == "tool":
                tool_call_id = message.get("tool_call_id")
                if not isinstance(tool_call_id, str):
                    continue

                content = message.get("content")
                success = True
                if isinstance(content, str) and ("An error occurred while running the tool." in content):
                    success = False

                if success:
                    tool_outcomes[tool_call_id] = True
                else:
                    if tool_outcomes.get(tool_call_id) is not True:
                        tool_outcomes[tool_call_id] = False

        success_count = sum(1 for call_id in assistant_call_ids if tool_outcomes.get(call_id) is True)
        failure_count = sum(1 for call_id in assistant_call_ids if tool_outcomes.get(call_id) is False)

        return success_count, failure_count

    @staticmethod
    def _compute_tool_bonus(success_count: int, failure_count: int) -> float:
        if success_count == 0 and failure_count == 0:
            return 0.0

        total_calls = success_count + failure_count
        if total_calls == 0 or success_count == 0:
            return 0.0

        ideal_calls = 10.0
        max_bonus = 0.2

        success_ratio = success_count / total_calls
        failure_penalty = 1.0 - math.tanh(failure_count)
        balance = 1.0 - math.tanh(abs(total_calls - ideal_calls) / max(ideal_calls, 1.0))

        raw_score = success_ratio * failure_penalty * balance
        bonus = max_bonus * raw_score

        return bonus if bonus > 0 else 0.0

    @staticmethod
    def _parse_causal_graph(response: str) -> tuple[CausalGraph | None, str | None]:
        """Parse CausalGraph from JSON response.

        Args:
            response: JSON string containing CausalGraph

        Returns:
            Tuple of (CausalGraph object or None, error message or None)
        """
        if not response:
            return None, "Empty response"

        try:
            parsed_json = json.loads(response)
        except json.JSONDecodeError as e:
            return None, f"JSON decode error: {str(e)}"

        try:
            graph = CausalGraph.from_dict(parsed_json)
            return graph, None
        except Exception as e:
            return None, f"Error parsing CausalGraph: {str(e)}"

    @staticmethod
    def _normalize_service_name(service_name: str) -> str:
        """Normalize service name for comparison.

        - Convert to lowercase
        - Remove 'ts-' prefix if present
        - Remove all hyphens
        """
        normalized = service_name.strip().lower()
        if normalized.startswith("ts-"):
            normalized = normalized[3:]
        normalized = normalized.replace("-", "")
        return normalized

    def _evaluate_causal_graph(
        self, agent_graph: CausalGraph | None, correct_answer: str
    ) -> tuple[float, dict[str, Any]]:
        """Evaluate CausalGraph against correct answer.

        Args:
            agent_graph: Parsed CausalGraph from agent response
            correct_answer: Ground truth service name(s), comma-separated

        Returns:
            Tuple of (score, evaluation_details dict)
        """
        evaluation_details: dict[str, Any] = {
            "correct": False,
            "root_cause_services": [],
            "reasoning": None,
        }

        score = 0.0

        if not agent_graph:
            evaluation_details["reasoning"] = "Failed to parse CausalGraph"
            return score, evaluation_details

        # Give base score for valid structure
        score += 0.1

        # Extract root cause services from agent graph
        root_cause_services = agent_graph.get_root_cause_services()
        evaluation_details["root_cause_services"] = list(root_cause_services)

        if not root_cause_services:
            evaluation_details["reasoning"] = "No root causes identified in graph"
            return score, evaluation_details

        # Normalize correct answers
        correct_answers = [ans.strip() for ans in correct_answer.split(",")]
        normalized_correct = {self._normalize_service_name(ans) for ans in correct_answers}

        # Normalize agent root causes
        normalized_agent = {self._normalize_service_name(rc) for rc in root_cause_services}

        # Exact set matching (after normalization)
        matched = normalized_agent & normalized_correct

        if matched:
            score += 1.0
            evaluation_details["correct"] = True
            evaluation_details["reasoning"] = f"Root cause services matched: {matched}"
        else:
            evaluation_details["reasoning"] = (
                f"Root cause services {list(root_cause_services)} do not match correct answer(s): {correct_answers}"
            )

        return score, evaluation_details

    def _load_gt_causal_graph(self, sample: EvaluationSample) -> CausalGraph | None:
        """Load ground truth CausalGraph from causal_graph.json.

        Args:
            sample: The evaluation sample containing meta with path

        Returns:
            CausalGraph object or None if loading fails
        """
        if not sample.meta or "path" not in sample.meta:
            return None

        gt_path = Path(sample.meta["path"]) / "causal_graph.json"

        if not gt_path.exists():
            return None

        try:
            with open(gt_path) as f:
                parsed_json = json.load(f)
            return CausalGraph.from_dict(parsed_json)
        except Exception:
            return None

    def _load_injection_json(self, sample: EvaluationSample) -> dict[str, Any] | None:
        """Load injection.json for a sample.

        Args:
            sample: The evaluation sample containing meta with path

        Returns:
            Parsed injection dict, or None if unavailable
        """
        if not sample.meta or "path" not in sample.meta:
            return None

        injection_path = Path(sample.meta["path"]) / "injection.json"

        if not injection_path.exists():
            return None

        try:
            with open(injection_path) as f:
                return json.load(f)
        except Exception:
            return None

    def _load_gt_root_cause_services(self, sample: EvaluationSample) -> set[str] | None:
        """Load ground truth root cause services from injection.json.

        injection.json's ground_truth.service is the authoritative source for
        fault injection targets, supporting multiple root cause services.

        Args:
            sample: The evaluation sample containing meta with path

        Returns:
            Set of root cause service names, or None if unavailable
        """
        injection = self._load_injection_json(sample)
        if not injection:
            return None
        services = injection.get("ground_truth", {}).get("service", [])
        return set(services) if services else None

    @staticmethod
    def _format_injection_context(injection: dict[str, Any]) -> str:
        """Format injection.json into a human-readable fault context string.

        Extracts fault type, injection target, duration, and ground truth
        to help downstream analysis understand what was injected.
        """
        parts: list[str] = []

        # Fault type
        fault_type = injection.get("fault_type")
        if fault_type is not None:
            parts.append(f"Fault type: {fault_type}")

        # Parse display_config for injection point details
        display_config_raw = injection.get("display_config")
        if display_config_raw:
            try:
                dc = json.loads(display_config_raw) if isinstance(display_config_raw, str) else display_config_raw
                ip = dc.get("injection_point", {})
                if ip:
                    target_parts = []
                    if ip.get("app_name"):
                        target_parts.append(f"service={ip['app_name']}")
                    if ip.get("class_name"):
                        target_parts.append(f"class={ip['class_name']}")
                    if ip.get("method_name"):
                        target_parts.append(f"method={ip['method_name']}")
                    if target_parts:
                        parts.append(f"Injection target: {', '.join(target_parts)}")
                duration = dc.get("duration")
                if duration:
                    parts.append(f"Duration: {duration} min")
                mem_type = dc.get("mem_type")
                if mem_type is not None:
                    label = "Heap" if mem_type == 1 else "Stack" if mem_type == 2 else str(mem_type)
                    parts.append(f"Memory type: {label}")
                namespace = dc.get("namespace")
                if namespace:
                    parts.append(f"Namespace: {namespace}")
            except (json.JSONDecodeError, TypeError):
                pass

        # Ground truth summary
        gt = injection.get("ground_truth", {})
        if gt:
            gt_parts = []
            if gt.get("service"):
                gt_parts.append(f"service={gt['service']}")
            if gt.get("function"):
                gt_parts.append(f"function={gt['function']}")
            if gt.get("metric"):
                gt_parts.append(f"metric={gt['metric']}")
            if gt_parts:
                parts.append(f"Ground truth: {', '.join(gt_parts)}")

        # Time window
        start = injection.get("start_time")
        end = injection.get("end_time")
        if start and end:
            parts.append(f"Time window: {start} ~ {end}")

        return "; ".join(parts) if parts else ""

    def calculate_metrics(self, samples: list[EvaluationSample]) -> dict:
        """Calculate metrics from the judged data."""
        if not samples:
            return {"total_samples": 0, "accuracy": 0.0, "correct_count": 0, "incorrect_count": 0, "unknown_count": 0}

        total_samples = len(samples)
        correct_count = 0
        incorrect_count = 0
        unknown_count = 0

        # Accumulators for graph metrics
        edge_f1_sum = 0.0
        node_f1_sum = 0.0
        root_cause_f1_sum = 0.0
        component_f1_sum = 0.0
        path_reachability_true_count = 0
        path_reachability_applicable_count = 0
        graph_metrics_count = 0

        for sample in samples:
            if sample.correct is True:
                correct_count += 1
            elif sample.correct is False:
                incorrect_count += 1
            else:  # sample.correct is None
                unknown_count += 1

            # Aggregate graph metrics if available
            if isinstance(sample.meta, dict) and "graph_metrics" in sample.meta:
                gm = sample.meta["graph_metrics"]
                primary = gm.get("primary", {})
                secondary = gm.get("secondary", {})

                edge_f1_sum += primary.get("edge_f1", 0.0)
                node_f1_sum += primary.get("node_f1", 0.0)
                root_cause_f1_sum += primary.get("root_cause_f1", 0.0)
                component_f1_sum += secondary.get("component_f1", 0.0)

                # Path reachability (bool | None)
                # Treat None (no correct root cause) as False (path not reachable)
                pr = primary.get("path_reachability")
                path_reachability_applicable_count += 1
                if pr is True:
                    path_reachability_true_count += 1

                graph_metrics_count += 1

        # 计算正确率（排除未判断的样本）
        judged_samples = correct_count + incorrect_count
        accuracy = (correct_count / judged_samples * 100) if judged_samples > 0 else 0.0

        # Calculate average graph metrics
        avg_edge_f1 = round(edge_f1_sum / graph_metrics_count, 4) if graph_metrics_count > 0 else 0.0
        avg_node_f1 = round(node_f1_sum / graph_metrics_count, 4) if graph_metrics_count > 0 else 0.0
        avg_root_cause_f1 = round(root_cause_f1_sum / graph_metrics_count, 4) if graph_metrics_count > 0 else 0.0
        avg_component_f1 = round(component_f1_sum / graph_metrics_count, 4) if graph_metrics_count > 0 else 0.0
        # Path reachability: None (no correct root cause) is treated as False (0.0)
        avg_path_reachability = (
            round(path_reachability_true_count / path_reachability_applicable_count, 4)
            if path_reachability_applicable_count > 0
            else 0.0
        )

        return {
            "benchmark": self.name,
            "total_samples": total_samples,
            "accuracy": round(accuracy, 2),
            "correct_count": correct_count,
            "incorrect_count": incorrect_count,
            "unknown_count": unknown_count,
            "judged_samples": judged_samples,
            "details": {
                "correct_rate": f"{correct_count}/{judged_samples}" if judged_samples > 0 else "0/0",
                "coverage": round((judged_samples / total_samples * 100), 2) if total_samples > 0 else 0.0,
            },
            "graph_metrics": {
                "samples_with_metrics": graph_metrics_count,
                "avg_edge_f1": avg_edge_f1,
                "avg_node_f1": avg_node_f1,
                "avg_root_cause_f1": avg_root_cause_f1,
                "avg_component_f1": avg_component_f1,
                "avg_path_reachability": avg_path_reachability,
                "path_reachability_total_samples": path_reachability_applicable_count,
            },
        }
