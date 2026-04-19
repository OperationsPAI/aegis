from __future__ import annotations

import asyncio
import json
import time
from collections.abc import Awaitable, Callable
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING, Any, Literal

from tqdm import tqdm

from ...config import ConfigLoader, EvalConfig
from ...utils import get_logger
from ..data import DBDataManager, EvaluationSample
from ..processer import PROCESSER_FACTORY, BaseProcesser
from ..processer.rcabench import RCABenchProcesser

if TYPE_CHECKING:
    from ...agents.base_agent import BaseAgent

logger = get_logger(__name__, "INFO")


@dataclass
class RolloutResult:
    """Result returned by a rollout runner for a single sample."""

    response: str = ""
    trajectory_json: str | None = None
    time_cost: float = 0.0
    trace_id: str | None = None


class BaseBenchmark:
    """Base class for benchmarks.

    Evaluation phases:
      - preprocess: load and preprocess the data
      - judge: judge the correctness of a batch of predictions
      - stat: get metrics.
    """

    dataset: DBDataManager
    _source_to_processer: dict[str, BaseProcesser] = {}

    def __init__(
        self,
        config: EvalConfig | str,
        source_path_fn: Callable[[str], str | Path] | None = None,
    ) -> None:
        # config
        if isinstance(config, str):
            config = ConfigLoader.load_eval_config(path=config)
        self.config = config
        # Explicit arg > config.source_path_fn > config.source_path auto-build
        self._source_path_fn = source_path_fn or config.get_source_path_fn()

        # dataset
        self.dataset = DBDataManager(config)
        _samples = self.dataset.load()
        if len(_samples) == 0:
            raise ValueError(f"No samples found for data config '{self.config.data}'! Please check the data config.")

    @property
    def agent_type(self) -> str | None:
        """Get agent type from config."""
        return self.config.agent_type

    @property
    def model_name(self) -> str | None:
        """Get model name from config."""
        return self.config.model_name

    @property
    def tags(self) -> list[str] | None:
        """Get tags from config."""
        if self.config.data:
            return self.config.data.tags
        return None

    async def main(self):
        logger.info(f"> Running with config: \n{json.dumps(self.config.model_dump(), indent=2, ensure_ascii=False)}")
        self.preprocess()
        await self.judge()
        logger.info("> Running stat...")
        await self.stat()

    def preprocess(self) -> list[EvaluationSample]:
        """Preprocess the dataset before rollout."""
        samples = self.dataset.get_samples(
            stage="init",
            agent_type=self.agent_type,
            model_name=self.model_name,
            tags=self.tags,
            exclude_trajectories=True,
        )
        if self.config.max_samples is not None:
            samples = samples[: self.config.max_samples]
        logger.info(f"Preprocessing {len(samples)} samples...")

        results = []
        with ThreadPoolExecutor() as executor:
            futures = {executor.submit(self.preprocess_one, sample): sample for sample in samples}
            for future in tqdm(as_completed(futures), total=len(futures), desc="Preprocessing"):
                result = future.result()
                if result is not None:
                    results.append(result)

        self.dataset.save(results)
        logger.info(f"Successfully preprocessed {len(results)} samples. Updated to db.")
        return results

    def preprocess_one(self, sample: EvaluationSample) -> EvaluationSample | None:
        processer = self._get_processer(sample.dataset)
        processed_sample = processer.preprocess_one(sample)
        if processed_sample is None:
            return None
        return sample

    def submit_result(
        self,
        sample_id: int | None = None,
        dataset_index: int | None = None,
        response: str = "",
        trajectory_json: str | None = None,
        time_cost: float | None = None,
        trace_id: str | None = None,
    ) -> EvaluationSample:
        """Submit an agent result for a sample.

        Args:
            sample_id: DB primary key of the EvaluationSample. Use this OR dataset_index.
            dataset_index: Dataset index to identify the sample. Use this OR sample_id.
            response: Agent's final response/output.
            trajectory_json: JSON string of agent trajectory.
            time_cost: Wall-clock time for the rollout.
            trace_id: Optional trace ID for observability.

        Returns:
            Updated EvaluationSample.
        """
        from sqlmodel import select

        from ...db import EvaluationSample as _ES
        from ...utils import SQLModelUtils

        with SQLModelUtils.create_session() as _session:
            if sample_id is not None:
                stmt = select(_ES).where(_ES.id == sample_id)
            elif dataset_index is not None:
                stmt = select(_ES).where(
                    _ES.exp_id == self.config.exp_id,
                    _ES.dataset_index == dataset_index,
                )
            else:
                raise ValueError("Must provide either sample_id or dataset_index")

            sample = _session.exec(stmt).first()
            if sample is None:
                raise ValueError(f"Sample not found (sample_id={sample_id}, dataset_index={dataset_index})")

            # DB column is JSONB — deserialise the string so SQLAlchemy
            # sends a proper JSON value instead of a VARCHAR.
            trajectories_value: Any = None
            if trajectory_json is not None:
                if isinstance(trajectory_json, str):
                    try:
                        trajectories_value = json.loads(trajectory_json)
                    except json.JSONDecodeError:
                        trajectories_value = trajectory_json
                else:
                    trajectories_value = trajectory_json

            sample.update(
                response=response,
                trajectories=trajectories_value,
                time_cost=time_cost,
                trace_id=trace_id,
                stage="rollout",
            )
            _session.add(sample)
            _session.commit()
            _session.refresh(sample)
            return sample

    def _wrap_agent(
        self,
        agent: BaseAgent,
        on_event: Callable[[str, dict], Any] | None = None,
        **kwargs,
    ) -> Callable[[EvaluationSample], Awaitable[RolloutResult]]:
        """Wrap a :class:`BaseAgent` into a rollout runner callable.

        Handles sample field extraction, error logging, ``RunContext``
        creation, and result conversion so that agent implementations
        stay clean.

        Args:
            agent: The agent to wrap.
            on_event: Optional callback ``(sample_id, event_dict)`` invoked
                whenever the agent emits an event via :class:`RunContext`.
                Use this to feed an ``EvalTracker`` or similar.
            **kwargs: Forwarded to :meth:`BaseAgent.run`.
        """
        from ...agents.base_agent import RunContext

        async def _runner(sample: EvaluationSample) -> RolloutResult:
            sample_id = str(sample.id)
            incident = (sample.augmented_question or sample.raw_question or "").strip()
            meta = sample.meta if isinstance(sample.meta, dict) else {}
            data_dir: str = meta.get("path", "")

            if not incident or not data_dir:
                logger.warning(
                    "Skip sample %s (idx=%s): %s",
                    sample.id,
                    sample.dataset_index,
                    "missing incident" if not incident else "missing data_dir",
                )
                if on_event:
                    on_event(sample_id, {"type": "skipped", "sample": sample})
                return RolloutResult()

            ctx = RunContext()
            if on_event:
                _on_event = on_event  # bind for closure
                ctx.add_listener(lambda evt: _on_event(sample_id, evt))
                on_event(sample_id, {"type": "started", "sample": sample, "data_dir": data_dir})

            t0 = time.monotonic()
            result = await agent.run(incident=incident, data_dir=data_dir, ctx=ctx, **kwargs)
            elapsed = time.monotonic() - t0

            if on_event:
                evt_type = "completed" if result.response else "failed"
                on_event(sample_id, {"type": evt_type, "sample": sample})

            traj_json = result.trajectory.to_json() if result.trajectory else None
            return RolloutResult(
                response=result.response,
                trajectory_json=traj_json,
                time_cost=elapsed,
            )

        return _runner

    async def rollout(
        self,
        runner: Callable[[EvaluationSample], Awaitable[RolloutResult]] | BaseAgent,
        max_samples: int | None = None,
        on_event: Callable[[str, dict], Any] | None = None,
        **agent_kwargs,
    ) -> tuple[int, int]:
        """Run rollout for all pending (stage=init) samples with bounded concurrency.

        ``runner`` accepts either:

        * A :class:`BaseAgent` instance (recommended) — the framework handles
          sample parsing, error handling, and trajectory serialisation.
        * A bare async callable ``(EvaluationSample) -> RolloutResult`` for
          backward compatibility.

        Args:
            runner: Agent instance or async callable.
            max_samples: Override max_samples from config (None = use config value).
            on_event: Optional callback ``(sample_id, event_dict)`` invoked
                for lifecycle events (started, completed, failed, skipped)
                and any events the agent emits via :class:`RunContext`.
                Only used when *runner* is a :class:`BaseAgent`.
            **agent_kwargs: Extra keyword arguments forwarded to
                :meth:`BaseAgent.run` when *runner* is a :class:`BaseAgent`.

        Returns:
            (ok_count, fail_count) tuple.
        """
        from ...agents.base_agent import BaseAgent as _BaseAgent

        if isinstance(runner, _BaseAgent):
            actual_runner = self._wrap_agent(runner, on_event=on_event, **agent_kwargs)
        else:
            actual_runner = runner

        samples = self.dataset.get_samples(
            stage="init",
            agent_type=self.agent_type,
            model_name=self.model_name,
            tags=self.tags,
            exclude_trajectories=True,
        )
        limit = max_samples if max_samples is not None else self.config.max_samples
        if limit is not None:
            samples = samples[:limit]

        if not samples:
            logger.info("No samples to rollout.")
            return 0, 0

        logger.info(f"Rolling out {len(samples)} samples (concurrency={self.config.concurrency})...")
        semaphore = asyncio.Semaphore(self.config.concurrency)
        ok_count = 0
        fail_count = 0

        timeout = self.config.rollout_timeout

        async def _bounded(sample: EvaluationSample) -> bool:
            async with semaphore:
                t0 = time.monotonic()
                result: RolloutResult | None = None
                try:
                    coro = actual_runner(sample)
                    if timeout is not None:
                        result = await asyncio.wait_for(coro, timeout=timeout)
                    else:
                        result = await coro
                except asyncio.TimeoutError:
                    elapsed = time.monotonic() - t0
                    logger.warning(
                        f"Rollout timed out for sample {sample.id} after {elapsed:.1f}s (limit={timeout}s), skipping."
                    )
                    self.submit_result(
                        sample_id=sample.id,
                        response="",
                        time_cost=elapsed,
                    )
                    return False
                except Exception as exc:  # pylint: disable=broad-except
                    logger.error(f"Rollout failed for sample {sample.id}: {exc}", exc_info=True)
                elapsed = time.monotonic() - t0
                self.submit_result(
                    sample_id=sample.id,
                    response=result.response if result else "",
                    trajectory_json=result.trajectory_json if result else None,
                    time_cost=result.time_cost if result else elapsed,
                    trace_id=result.trace_id if result else None,
                )
                return result is not None and bool(result.response)

        tasks = [_bounded(s) for s in samples]
        for coro in asyncio.as_completed(tasks):
            success = await coro
            if success:
                ok_count += 1
            else:
                fail_count += 1

        logger.info(f"Rollout complete: {ok_count} ok / {fail_count} failed.")
        return ok_count, fail_count

    async def rollout_and_judge(
        self,
        runner: Callable[[EvaluationSample], Awaitable[RolloutResult]] | BaseAgent,
        max_samples: int | None = None,
        on_event: Callable[[str, dict], Any] | None = None,
        on_judge: Callable[[EvaluationSample, list[EvaluationSample]], Any] | None = None,
        **agent_kwargs,
    ) -> tuple[int, int, list[EvaluationSample]]:
        """Rollout and judge incrementally — judge each sample as soon as its rollout finishes.

        Combines :meth:`rollout` and :meth:`judge` into a single pass so that
        evaluation metrics can be observed in real-time.

        Args:
            runner: Agent instance or async callable.
            max_samples: Override max_samples from config.
            on_event: Rollout lifecycle event callback (same as :meth:`rollout`).
            on_judge: Called after each sample is judged with
                ``(judged_sample, all_judged_so_far)``.
            **agent_kwargs: Extra kwargs forwarded to :meth:`BaseAgent.run`.

        Returns:
            ``(ok_count, fail_count, judged_samples)`` tuple.
        """
        from ...agents.base_agent import BaseAgent as _BaseAgent

        if isinstance(runner, _BaseAgent):
            actual_runner = self._wrap_agent(runner, on_event=on_event, **agent_kwargs)
        else:
            actual_runner = runner

        samples = self.dataset.get_samples(
            stage="init",
            agent_type=self.agent_type,
            model_name=self.model_name,
            tags=self.tags,
            exclude_trajectories=True,
        )
        limit = max_samples if max_samples is not None else self.config.max_samples
        if limit is not None:
            samples = samples[:limit]

        if not samples:
            logger.info("No samples to rollout.")
            return 0, 0, []

        logger.info(
            f"Rolling out + judging {len(samples)} samples "
            f"(rollout_concurrency={self.config.concurrency}, "
            f"judge_concurrency={self.config.judge_concurrency})..."
        )

        semaphore = asyncio.Semaphore(self.config.concurrency)
        judge_semaphore = asyncio.Semaphore(self.config.judge_concurrency)
        judge_queue: asyncio.Queue[EvaluationSample | None] = asyncio.Queue()
        judged_samples: list[EvaluationSample] = []
        ok_count = 0
        fail_count = 0
        timeout = self.config.rollout_timeout

        async def _bounded(sample: EvaluationSample) -> bool:
            async with semaphore:
                t0 = time.monotonic()
                result: RolloutResult | None = None
                try:
                    coro = actual_runner(sample)
                    if timeout is not None:
                        result = await asyncio.wait_for(coro, timeout=timeout)
                    else:
                        result = await coro
                except asyncio.TimeoutError:
                    elapsed = time.monotonic() - t0
                    logger.warning(
                        f"Rollout timed out for sample {sample.id} after {elapsed:.1f}s (limit={timeout}s), skipping."
                    )
                    updated = self.submit_result(
                        sample_id=sample.id,
                        response="",
                        time_cost=elapsed,
                    )
                    await judge_queue.put(updated)
                    return False
                except Exception as exc:  # pylint: disable=broad-except
                    logger.error(f"Rollout failed for sample {sample.id}: {exc}", exc_info=True)
                elapsed = time.monotonic() - t0
                updated = self.submit_result(
                    sample_id=sample.id,
                    response=result.response if result else "",
                    trajectory_json=result.trajectory_json if result else None,
                    time_cost=result.time_cost if result else elapsed,
                    trace_id=result.trace_id if result else None,
                )
                await judge_queue.put(updated)
                return result is not None and bool(result.response)

        async def _judge_one_bounded(sample: EvaluationSample) -> None:
            async with judge_semaphore:
                try:
                    result = await self.judge_one(sample)
                    judged_samples.append(result)
                    if on_judge:
                        on_judge(result, judged_samples)
                except Exception as e:  # pylint: disable=broad-except
                    logger.error(f"Judge failed for sample {sample.id}: {e}", exc_info=True)

        pending_judge_tasks: list[asyncio.Task] = []

        async def _judge_consumer() -> None:
            while True:
                sample = await judge_queue.get()
                if sample is None:
                    break
                task = asyncio.create_task(_judge_one_bounded(sample))
                pending_judge_tasks.append(task)
            # Wait for all remaining judge tasks to finish
            if pending_judge_tasks:
                await asyncio.gather(*pending_judge_tasks, return_exceptions=True)

        # Start judge consumer in background
        consumer_task = asyncio.create_task(_judge_consumer())

        # Run rollouts
        rollout_tasks = [_bounded(s) for s in samples]
        for coro in asyncio.as_completed(rollout_tasks):
            success = await coro
            if success:
                ok_count += 1
            else:
                fail_count += 1

        # Signal judge consumer to stop and wait for all judge tasks
        await judge_queue.put(None)
        await consumer_task

        logger.info(
            f"Rollout+judge complete: {ok_count} ok / {fail_count} failed, "
            f"{len(judged_samples)} judged."
        )
        return ok_count, fail_count, judged_samples

    async def judge(self, stage: Literal["init", "rollout", "judged"] | None = "rollout") -> list[EvaluationSample]:
        """Judge samples.

        Args:
            stage (str|None, optional): The stage of samples to judge. If set to None, you can rejudge all samples.
        """
        samples = self.dataset.get_samples(
            stage=stage,
            agent_type=self.agent_type,
            model_name=self.model_name,
            tags=self.tags,
            exclude_trajectories=True,
        )
        logger.info(f"Judging {len(samples)} samples...")

        semaphore = asyncio.Semaphore(self.config.judge_concurrency)

        async def judge_with_semaphore(item: EvaluationSample):
            async with semaphore:
                try:
                    return await self.judge_one(item)
                except Exception as e:  # pylint: disable=broad-except
                    logger.error(
                        f">>>>>>>>>>>>>\nError judging sample '{item}': {e}\n<<<<<<<<<<<<<",
                        exc_info=True,
                    )
                    return None

        tasks = [judge_with_semaphore(item) for item in samples]
        results = []
        for task in tqdm(asyncio.as_completed(tasks), total=len(tasks), desc="Judging"):
            result = await task
            if result is not None:
                results.append(result)
        logger.info(f"Successfully judged {len(results)} samples. Updated to db.")
        return results

    async def judge_one(self, data: EvaluationSample) -> EvaluationSample:
        # Lazy-load trajectories if they were deferred during bulk query
        if data.trajectories is None and data.id is not None:
            data = self.dataset.load_with_trajectories(data)
        judger = self._get_processer(data.dataset)
        result = await judger.judge_one(data)
        result.update(stage="judged")  # update stage to judged
        self.dataset.save(result)
        return result

    async def stat(self) -> list[dict]:
        judged_samples = self.dataset.get_samples(
            stage="judged",
            agent_type=self.agent_type,
            model_name=self.model_name,
            tags=self.tags,
            exclude_trajectories=True,
        )
        logger.info(f"Stat from {len(judged_samples)} samples (agent={self.agent_type}, model={self.model_name}):")

        data_by_benchmark = self._group_data_by_benchmark(judged_samples)
        overall_results: list[dict] = []
        for benchmark, data in data_by_benchmark.items():
            evaluator = self._get_processer(benchmark)
            result = await evaluator.stat(data)
            result["agent_type"] = self.agent_type
            result["model_name"] = self.model_name
            overall_results.append(result)

        logger.info(json.dumps(overall_results, indent=4, ensure_ascii=False))
        print(json.dumps(overall_results, indent=4, ensure_ascii=False))
        return overall_results

    def _get_processer(self, source: str) -> BaseProcesser:
        if source not in self._source_to_processer:
            processer_cls = PROCESSER_FACTORY._registry.get(source.lower())
            if processer_cls is not None and issubclass(processer_cls, RCABenchProcesser):
                processer = processer_cls(self.config, source_path_fn=self._source_path_fn)
            else:
                processer = PROCESSER_FACTORY.get(source, self.config)
            self._source_to_processer[source] = processer
        return self._source_to_processer[source]

    def _group_data_by_benchmark(self, predict_data: list[EvaluationSample]) -> dict[str, list[EvaluationSample]]:
        data_by_benchmark: dict[str, list[EvaluationSample]] = {}
        for data in predict_data:
            benchmark = data.dataset
            if benchmark not in data_by_benchmark:
                data_by_benchmark[benchmark] = []
            data_by_benchmark[benchmark].append(data)
        return data_by_benchmark
