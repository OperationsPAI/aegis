import multiprocessing
import multiprocessing.pool
import os
import time
import traceback
from collections.abc import Callable, Sequence
from typing import Any, Literal, TypeVar

from tqdm.auto import tqdm

from ..algorithms.spec import global_algorithm_registry, set_global_algorithm_registry
from ..config import get_config, set_config
from ..logging import get_real_logger, logger, set_real_logger, timeit
from ..samplers.spec import global_sampler_registry, set_global_sampler_registry


def set_cpu_limit_outer(n: int | None) -> None:
    if n is None:
        return

    os.environ["POLARS_MAX_THREADS"] = str(n)


def set_cpu_limit_inner(n: int | None) -> None:
    if n is None:
        return

    try:
        import torch  # type: ignore

        torch.set_num_threads(n)
    except ImportError:
        pass


def initializers(*, cpu_limit: int | None = None) -> list[tuple[Callable, Any]]:
    ans = [
        (set_cpu_limit_inner, (cpu_limit,)),
        # (set_real_logger, (get_real_logger(),)),
        (set_config, (get_config(),)),
        (set_global_algorithm_registry, (global_algorithm_registry(),)),
        (set_global_sampler_registry, (global_sampler_registry(),)),
    ]

    return ans


def call_initializers(init_list: list[tuple[Callable, Any]]) -> None:
    for func, args in init_list:
        func(*args)


R = TypeVar("R")


def _fmap(
    mode: Literal["threadpool", "processpool"],
    tasks: Sequence[Callable[[], R]],
    *,
    parallel: int,
    ignore_exceptions: bool,
    cpu_limit_each: int | None,
) -> list[R]:
    if cpu_limit_each is not None:
        assert mode == "processpool", "cpu_limit is only supported for processpool mode"
        assert cpu_limit_each > 0, "cpu_limit must be greater than 0"

    if not isinstance(tasks, list):
        tasks = list(tasks)

    if len(tasks) == 0:
        return []

    if parallel is None or parallel > 1:
        num_workers = parallel or multiprocessing.cpu_count()
        num_workers = min(num_workers, len(tasks))
    else:
        num_workers = 1

    logger_ = logger.opt(depth=2)

    if num_workers > 1:
        if mode == "threadpool":
            pool = multiprocessing.pool.ThreadPool(
                processes=num_workers,
            )
        elif mode == "processpool":
            set_cpu_limit_outer(cpu_limit_each)
            pool = multiprocessing.get_context("spawn").Pool(
                processes=num_workers,
                initializer=call_initializers,
                initargs=(initializers(),),
            )
        else:
            raise ValueError(f"Unknown mode: {mode}")

        try:
            asyncs = [pool.apply_async(task) for task in tasks]
            finished = [False] * len(asyncs)
            index_results: list[tuple[int, R]] = []
            exception_count = 0

            with tqdm(total=len(asyncs), desc=f"fmap_{mode}") as pbar:
                while not all(finished):
                    for i, async_ in enumerate(asyncs):
                        if finished[i]:
                            continue
                        if not async_.ready():
                            continue
                        try:
                            result = async_.get(timeout=0.1)
                            finished[i] = True
                            index_results.append((i, result))
                            pbar.update(1)
                        except multiprocessing.TimeoutError:
                            continue
                        except Exception as e:
                            exception_count += 1
                            finished[i] = True
                            pbar.update(1)
                            if ignore_exceptions:
                                traceback.print_exc()
                                logger_.error("Exception in task {}: {}", i, e)
                            else:
                                raise e
                    pbar.update(0)
                    time.sleep(1)
        finally:
            # Graceful shutdown: close() stops accepting new tasks, then
            # join() waits for workers to finish. This properly releases
            # OS-level semaphores that the spawn-context Pool creates.
            # The previous `with pool:` only called terminate()+join()
            # which can leave semaphores leaked on abrupt exit.
            pool.close()
            pool.join()

        index_results.sort(key=lambda x: x[0])
        results = [result for _, result in index_results]
    else:
        results = []
        exception_count = 0
        for i, task in enumerate(tqdm(tasks, desc="fmap")):
            try:
                result = task()
                results.append(result)
            except Exception as e:
                exception_count += 1
                if ignore_exceptions:
                    traceback.print_exc()
                    logger_.error("Exception in task {}: {}", i, e)
                else:
                    raise e

    if exception_count > 0:
        logger_.warning(f"fmap_{mode} completed with {exception_count} exceptions.")

    logger_.debug(f"fmap_{mode} completed with {len(results)} results in {len(tasks)} tasks.")

    return results


@timeit(log_args=False)
def fmap_threadpool(
    tasks: Sequence[Callable[[], R]],
    *,
    parallel: int,
    ignore_exceptions: bool = False,
) -> list[R]:
    return _fmap(
        "threadpool",
        tasks,
        parallel=parallel,
        ignore_exceptions=ignore_exceptions,
        cpu_limit_each=None,
    )


@timeit(log_args=False)
def fmap_processpool(
    tasks: Sequence[Callable[[], R]],
    *,
    parallel: int,
    ignore_exceptions: bool = False,
    cpu_limit_each: int | None = None,
) -> list[R]:
    return _fmap(
        "processpool",
        tasks,
        parallel=parallel,
        ignore_exceptions=ignore_exceptions,
        cpu_limit_each=cpu_limit_each,
    )
