"""RCABench evaluator (v2 schema).

The agent emits an `AgentRCAOutput` JSON. Judging is fully mechanical for the
deterministic axes (root_cause F1, overclaim, sql_executable) and uses an
LLM-as-judge only for chain coherence.
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
    """Processer for RCABench v2 evaluation.

    Agent contract: structured JSON (`AgentRCAOutput`). The judge:
      1. Type-aware matches each agent root_cause to a GT fault from
         injection.json's engine_config.
      2. Re-runs every evidence SQL via DuckDB on the case parquets.
      3. Asks an LLM to score the chain's coherence given the merged claims +
         executed SQL preview + GT causal_graph.

    Output (per sample, stored on `sample.meta['eval_v2']`):
        - root_cause_f1, overclaim_rate, sql_executable_rate, chain_coherence
        - headline = product of the four
        - per_fault, per_evidence, chain_judge — diagnostic detail
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

        alarm_endpoints = self._extract_alarm_endpoints(source_path)
        meta["alarm_endpoints"] = alarm_endpoints
        meta["path"] = str(symlink_path.absolute())

        formatted_reports = "\n".join(f"- {ep}" for ep in alarm_endpoints)
        template = AUGMENTATION_PROMPTS.get("RCABench", AUGMENTATION_PROMPTS["default"])
        augmented_question = template.format(
            reports=formatted_reports,
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

        reasoning_bits: list[str] = []
        reasoning_bits.append(
            f"rc_f1={result.root_cause_f1:.2f} sql={result.sql_executable_rate:.2f} "
            f"chain={result.chain_coherence:.2f} headline={result.headline:.2f}"
        )
        if result.parse_error:
            reasoning_bits.append(f"parse_error={result.parse_error}")
        if result.chain_judge and result.chain_judge.reasoning:
            reasoning_bits.append(f"judge: {result.chain_judge.reasoning}")

        sample.update(
            judged_response=None,
            correct=result.case_correct,
            confidence=result.headline,
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
                "case_correct_rate": 0.0,
                "avg_service_f1": 0.0,
                "avg_root_cause_f1": 0.0,
                "avg_sql_executable_rate": 0.0,
                "avg_chain_coherence": 0.0,
                "avg_headline": 0.0,
                "avg_overclaim_rate": 0.0,
            }

        n = len(samples)
        service_f1 = 0.0
        rc_f1 = 0.0
        overclaim = 0.0
        sql_ok = 0.0
        chain = 0.0
        headline = 0.0
        node_f1 = 0.0
        edge_f1 = 0.0
        correct = 0
        with_eval = 0
        parse_errors = 0
        zero_evidence = 0

        for s in samples:
            if not isinstance(s.meta, dict):
                continue
            ev = s.meta.get("eval_v2")
            if not isinstance(ev, dict) or "error" in ev:
                continue
            with_eval += 1
            service_f1 += float(ev.get("service_f1") or 0.0)
            rc_f1 += float(ev.get("root_cause_f1") or 0.0)
            overclaim += float(ev.get("overclaim_rate") or 0.0)
            sql_ok += float(ev.get("sql_executable_rate") or 0.0)
            chain += float(ev.get("chain_coherence") or 0.0)
            headline += float(ev.get("headline") or 0.0)
            node_f1 += float(ev.get("node_f1") or 0.0)
            edge_f1 += float(ev.get("edge_f1") or 0.0)
            if ev.get("case_correct"):
                correct += 1
            if ev.get("parse_error"):
                parse_errors += 1
            if not ev.get("per_evidence"):
                zero_evidence += 1

        denom = max(1, with_eval)
        return {
            "benchmark": self.name,
            "total_samples": n,
            "scored_samples": with_eval,
            "case_correct": correct,
            "case_correct_rate": round(correct / denom, 4),
            "avg_service_f1": round(service_f1 / denom, 4),
            "avg_root_cause_f1": round(rc_f1 / denom, 4),
            "avg_overclaim_rate": round(overclaim / denom, 4),
            "avg_sql_executable_rate": round(sql_ok / denom, 4),
            "avg_chain_coherence": round(chain / denom, 4),
            "avg_node_f1": round(node_f1 / denom, 4),
            "avg_edge_f1": round(edge_f1 / denom, 4),
            "avg_headline": round(headline / denom, 4),
            "parse_errors": parse_errors,
            "zero_evidence_outputs": zero_evidence,
        }
