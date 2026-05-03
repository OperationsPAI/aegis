"""RCABench evaluator (v2 schema, simplified contract).

Per case the agent emits an ``AgentRCAOutput`` JSON. Judging is mechanical
for the deterministic axes (single-tier (service, fault_kind) match,
sql_executable) and uses a per-evidence LLM judge for evidence_support.

See the v2 README at ``v3/sdk/evaluation/v2/README.md`` for the full
metric contract; this module is the aggregator that wraps `evaluate_v2`.
"""

import json
import uuid
from collections.abc import Callable
from pathlib import Path
from typing import Any

from ....evaluation.causal_graph import CausalGraph
from ....evaluation.v2 import EvaluationResultV2, evaluate_v2
from ...config import EvalConfig
from ..data import EvaluationSample
from .base_match_processor import BaseMatchProcesser
from .prompts import AUGMENTATION_PROMPTS


class RCABenchProcesser(BaseMatchProcesser):
    """Processer for RCABench v2 evaluation (simplified contract).

    Agent contract: structured JSON (`AgentRCAOutput`). Per case:
      1. Single-tier (service, fault_kind) multiset match against GT
         engine_config faults → precision/recall/f1, exact_match,
         fault_kind_accuracy.
      2. Re-runs every evidence SQL via DuckDB → sql_executable_rate.
      3. Per-evidence LLM judge over (claim ↔ SQL row preview + chain
         coherence) → evidence_support_rate.
      4. Service-level node_f1 / edge_f1 vs GT causal_graph.

    Output (per sample, stored on `sample.meta['eval_v2']`):
        - precision, recall, f1, exact_match, fault_kind_accuracy,
          sql_executable_rate, evidence_support_rate, node_f1, edge_f1
        - per_fault, per_evidence — diagnostic detail
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
        """Materialize a per-sample symlinked data dir + render the V2 prompt."""
        assert sample.meta is not None
        meta = dict(sample.meta)

        if self.source_path_fn is not None:
            source_data_dir = str(self.source_path_fn(sample.source))
        else:
            source_data_dir = meta.get("source_data_dir") or meta.get("path")
        if not source_data_dir:
            raise ValueError(f"Sample {sample.id} has no source_data_dir or path in meta")

        source_path = Path(source_data_dir).expanduser()
        if not source_path.exists() or not source_path.is_dir():
            raise ValueError(f"Source data dir missing: {source_path}")

        eval_data_dir = Path("eval-data") / sample.exp_id
        eval_data_dir.mkdir(parents=True, exist_ok=True)
        symlink_path = eval_data_dir / f"data_{uuid.uuid4().hex[:8]}"
        if symlink_path.exists() or symlink_path.is_symlink():
            symlink_path.unlink()
        symlink_path.symlink_to(source_path.absolute(), target_is_directory=True)

        # Still computed (and stored on meta) for downstream analysis —
        # judge / aggregator code can compare what the agent flagged
        # against the GT-affected endpoints. Just no longer leaked to the
        # agent via the prompt: the new RCABench template frames the
        # incident as a user-ticket complaint, so finding the affected
        # endpoints is part of the task.
        alarm_endpoints = self._extract_alarm_endpoints(source_path)
        meta["alarm_endpoints"] = alarm_endpoints
        meta["path"] = str(symlink_path.absolute())

        template = AUGMENTATION_PROMPTS.get("RCABench", AUGMENTATION_PROMPTS["default"])
        augmented_question = template.format(
            directory_path=str(symlink_path.absolute()),
        )

        sample.update(augmented_question=augmented_question, meta=meta)
        return sample

    @staticmethod
    def _extract_endpoint_from_component(component: str) -> str | None:
        if not component.startswith("span|"):
            return None
        endpoint = component.split("::", 1)[1].strip() if "::" in component else component.split("|", 1)[1].strip()
        return endpoint or None

    @staticmethod
    def _extract_alarm_endpoints(source_path: Path) -> list[str]:
        cg_path = source_path / "causal_graph.json"
        if not cg_path.exists():
            raise ValueError(f"causal_graph.json not found in {source_path}")
        graph = CausalGraph.from_dict(json.loads(cg_path.read_text()))
        candidates = (
            graph.alarm_nodes
            if graph.alarm_nodes
            else [n for n in graph.nodes if n.component.startswith("span|loadgenerator::")]
        )
        endpoints: list[str] = []
        seen: set[str] = set()
        for node in candidates:
            ep = RCABenchProcesser._extract_endpoint_from_component(node.component)
            if ep and ep not in seen:
                seen.add(ep)
                endpoints.append(ep)
        if not endpoints:
            raise ValueError(f"No valid endpoints found in {source_path}")
        return endpoints

    async def judge_one(self, sample: EvaluationSample) -> EvaluationSample:
        meta = dict(sample.meta) if isinstance(sample.meta, dict) else {"previous_meta": sample.meta}
        case_dir = self._resolve_case_dir(meta, sample)

        injection = self._load_json(case_dir / "injection.json") if case_dir else None
        gt_graph = self._load_gt_graph(case_dir) if case_dir else None

        if not case_dir or injection is None:
            sample.update(
                correct=False,
                confidence=0.0,
                reasoning="missing case dir or injection.json",
                judged_response=None,
            )
            meta["eval_v2"] = {"error": "missing case dir or injection.json"}
            sample.update(meta=meta)
            return sample

        result: EvaluationResultV2 = await evaluate_v2(
            agent_output_raw=sample.response or "",
            injection=injection,
            parquet_dir=case_dir,
            gt_graph=gt_graph,
            llm_client=self.judge_client,
            judge_model=self.judge_model,
            case_name=sample.source,
        )

        meta["eval_v2"] = result.model_dump(mode="json")

        kind_str = f"{result.fault_kind_accuracy:.2f}" if result.fault_kind_accuracy is not None else "n/a"
        ev_str = f"{result.evidence_support_rate:.2f}" if result.evidence_support_rate is not None else "n/a"
        reasoning_bits: list[str] = [
            f"f1={result.f1:.2f} exact={int(result.exact_match)} "
            f"kind_acc={kind_str} sql={result.sql_executable_rate:.2f} "
            f"ev_support={ev_str} node_f1={result.node_f1:.2f} edge_f1={result.edge_f1:.2f}"
        ]
        if result.parse_error:
            reasoning_bits.append(f"parse_error={result.parse_error}")
        if result.n_evidence_judge_failed:
            reasoning_bits.append(f"judge_failed={result.n_evidence_judge_failed}/{result.n_evidence}")

        # Surface per-evidence judge reasoning lines on `judged_response` so
        # the dashboard can render them alongside the agent response. One
        # line per evidence keeps the diff readable for cases with many.
        judge_lines: list[str] = []
        for rec in result.per_evidence:
            if rec.supported is None and not rec.judge_reasoning:
                continue
            tag = "?" if rec.supported is None else ("Y" if rec.supported else "N")
            judge_lines.append(f"[{rec.label}] supported={tag} {rec.judge_reasoning}")
        judged_response: str | None = "\n".join(judge_lines) if judge_lines else None

        sample.update(
            judged_response=judged_response,
            correct=result.exact_match,
            confidence=result.f1,
            reasoning=" | ".join(reasoning_bits),
            extracted_final_answer=None,
            meta=meta,
        )
        return sample

    @staticmethod
    def _resolve_case_dir(meta: dict[str, Any], sample: EvaluationSample) -> Path | None:
        path = meta.get("path") or meta.get("source_data_dir")
        if path:
            p = Path(path)
            if p.exists():
                return p.resolve()
        return None

    @staticmethod
    def _load_json(path: Path) -> dict[str, Any] | None:
        if not path.exists():
            return None
        try:
            return json.loads(path.read_text())
        except Exception:
            return None

    @staticmethod
    def _load_gt_graph(case_dir: Path) -> CausalGraph | None:
        cg = case_dir / "causal_graph.json"
        if not cg.exists():
            return None
        try:
            return CausalGraph.from_dict(json.loads(cg.read_text()))
        except Exception:
            return None

    def calculate_metrics(self, samples: list[EvaluationSample]) -> dict:
        if not samples:
            return {
                "benchmark": self.name,
                "total_samples": 0,
                "scored_samples": 0,
                "exact_match_count": 0,
                "exact_match_rate": 0.0,
                "avg_precision": 0.0,
                "avg_recall": 0.0,
                "avg_f1": 0.0,
                "avg_fault_kind_accuracy": 0.0,
                "kind_accuracy_denom": 0,
                "avg_sql_executable_rate": 0.0,
                "avg_evidence_support_rate": 0.0,
                "avg_node_f1": 0.0,
                "avg_edge_f1": 0.0,
                "parse_errors": 0,
                "zero_evidence_outputs": 0,
                "judge_failed": 0,
            }

        n = len(samples)

        # Sums divide by total n so parse-failed / no-eval samples count as 0
        # for every metric except fault_kind_accuracy — there a 0 denominator
        # means "no service-correct rcs to grade", which is genuinely
        # different from "graded as 0" and would smear the metric. Those
        # cases are excluded from the kind-accuracy mean and kind_accuracy_denom
        # exposes how many cases actually contributed.
        exact_count = 0
        precision_sum = 0.0
        recall_sum = 0.0
        f1_sum = 0.0
        sql_sum = 0.0
        ev_support_sum = 0.0
        node_f1_sum = 0.0
        edge_f1_sum = 0.0
        kind_acc_sum = 0.0
        kind_acc_cases = 0
        scored = 0
        parse_errors = 0
        zero_evidence = 0
        judge_failed_evidences = 0

        for s in samples:
            if not isinstance(s.meta, dict):
                continue
            ev = s.meta.get("eval_v2")
            if not isinstance(ev, dict) or "error" in ev:
                continue
            scored += 1

            precision_sum += float(ev.get("precision") or 0.0)
            recall_sum += float(ev.get("recall") or 0.0)
            f1_sum += float(ev.get("f1") or 0.0)
            sql_sum += float(ev.get("sql_executable_rate") or 0.0)
            node_f1_sum += float(ev.get("node_f1") or 0.0)
            edge_f1_sum += float(ev.get("edge_f1") or 0.0)

            esr = ev.get("evidence_support_rate")
            if esr is not None:
                ev_support_sum += float(esr)
            # else: case did not produce a usable evidence_support_rate;
            # README says it counts as 0 in the benchmark mean.

            kind_acc = ev.get("fault_kind_accuracy")
            if kind_acc is not None:
                kind_acc_sum += float(kind_acc)
                kind_acc_cases += 1

            if ev.get("exact_match"):
                exact_count += 1
            if ev.get("parse_error"):
                parse_errors += 1
            if not ev.get("per_evidence"):
                zero_evidence += 1
            judge_failed_evidences += int(ev.get("n_evidence_judge_failed") or 0)

        denom = max(1, n)
        kind_denom = max(1, kind_acc_cases)
        return {
            "benchmark": self.name,
            "total_samples": n,
            "scored_samples": scored,
            "exact_match_count": exact_count,
            "exact_match_rate": round(exact_count / denom, 4),
            "avg_precision": round(precision_sum / denom, 4),
            "avg_recall": round(recall_sum / denom, 4),
            "avg_f1": round(f1_sum / denom, 4),
            "avg_fault_kind_accuracy": round(kind_acc_sum / kind_denom, 4),
            "kind_accuracy_denom": kind_acc_cases,
            "avg_sql_executable_rate": round(sql_sum / denom, 4),
            "avg_evidence_support_rate": round(ev_support_sum / denom, 4),
            "avg_node_f1": round(node_f1_sum / denom, 4),
            "avg_edge_f1": round(edge_f1_sum / denom, 4),
            "parse_errors": parse_errors,
            "zero_evidence_outputs": zero_evidence,
            "judge_failed": judge_failed_evidences,
        }
