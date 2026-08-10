import functools
import logging
import multiprocessing
import multiprocessing.pool
import os
import time
import traceback
from collections.abc import Callable, Sequence
from multiprocessing.pool import ThreadPool
from pathlib import Path
from typing import Any, Literal, TypeVar

from tqdm.auto import tqdm

logger = logging.getLogger(__name__)


def timeit(func):
    """Decorator to measure and log function execution time to .timeit.log

    Only logs when ENABLE_TIMEIT environment variable is set.
    """

    @functools.wraps(func)
    def wrapper(*args, **kwargs):
        if not os.getenv("ENABLE_TIMEIT"):
            return func(*args, **kwargs)

        start = time.perf_counter()
        result = func(*args, **kwargs)
        elapsed = time.perf_counter() - start

        log_path = Path(".timeit.log")
        with log_path.open("a") as f:
            f.write(f"{func.__module__}.{func.__name__}: {elapsed:.6f}s\n")

        return result

    return wrapper


def set_cpu_limit_outer(n: int | None) -> None:
    if n is None:
        return

    os.environ["POLARS_MAX_THREADS"] = str(n)


def set_cpu_limit_inner(n: int | None) -> None:
    if n is None:
        return

    os.environ["POLARS_MAX_THREADS"] = str(n)

    try:
        import torch  # type: ignore

        torch.set_num_threads(n)
    except ImportError:
        pass


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
    show_progress: bool = True,
    log_level: int | None = None,
    log_file: str | None = None,
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

    if num_workers > 1:
        if mode == "threadpool":
            pool: ThreadPool | multiprocessing.pool.Pool = ThreadPool(
                processes=num_workers,
            )
        elif mode == "processpool":
            set_cpu_limit_outer(cpu_limit_each)
            pool = multiprocessing.get_context("spawn").Pool(
                processes=num_workers,
                initializer=call_initializers,
                initargs=(initializers(cpu_limit=cpu_limit_each, log_level=log_level, log_file=log_file),),
            )
        else:
            raise ValueError(f"Unknown mode: {mode}")

        with pool:
            # Use apply_async with direct function call
            asyncs = [pool.apply_async(task) for task in tasks]
            finished = [False] * len(asyncs)
            index_results: list[tuple[int, R]] = []
            exception_count = 0

            with tqdm(total=len(asyncs), desc=f"fmap_{mode}", disable=not show_progress) as pbar:
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
                                logger.error(f"Exception in task {i}: {e}")
                            else:
                                raise e
                    time.sleep(0.1)

        index_results.sort(key=lambda x: x[0])
        results = [result for _, result in index_results]
    else:
        results = []
        exception_count = 0
        for i, task in enumerate(tqdm(tasks, desc="fmap", disable=not show_progress)):
            try:
                result = task()
                results.append(result)
            except Exception as e:
                exception_count += 1
                if ignore_exceptions:
                    traceback.print_exc()
                    logger.error(f"Exception in task {i}: {e}")
                else:
                    raise e

    if exception_count > 0:
        logger.warning(f"fmap_{mode} completed with {exception_count} exceptions.")

    logger.debug(f"fmap_{mode} completed with {len(results)} results in {len(tasks)} tasks.")

    return results


def fmap_threadpool(
    tasks: Sequence[Callable[[], R]],
    *,
    parallel: int,
    ignore_exceptions: bool = False,
    show_progress: bool = True,
) -> list[R]:
    return _fmap(
        "threadpool",
        tasks,
        parallel=parallel,
        ignore_exceptions=ignore_exceptions,
        cpu_limit_each=None,
        show_progress=show_progress,
    )


def fmap_processpool(
    tasks: Sequence[Callable[[], R]],
    *,
    parallel: int,
    ignore_exceptions: bool = False,
    cpu_limit_each: int | None = None,
    show_progress: bool = True,
    log_level: int | None = None,
    log_file: str | None = None,
) -> list[R]:
    return _fmap(
        "processpool",
        tasks,
        parallel=parallel,
        ignore_exceptions=ignore_exceptions,
        cpu_limit_each=cpu_limit_each,
        show_progress=show_progress,
        log_level=log_level,
        log_file=log_file,
    )


def set_log_level(level: int | None) -> None:
    """Set log level for src modules in child processes."""
    if level is not None:
        logging.getLogger("src").setLevel(level)


def setup_subprocess_logging(log_file_path: str | None, log_level: int) -> None:
    """Setup logging in subprocess to write to shared log file."""
    formatter = logging.Formatter(
        fmt="%(asctime)s - %(name)s - %(levelname)s - [PID:%(process)d] - %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )

    root_logger = logging.getLogger()
    root_logger.setLevel(log_level)
    root_logger.handlers.clear()

    # File handler (if specified)
    if log_file_path:
        file_handler = logging.FileHandler(log_file_path, mode="a", encoding="utf-8")
        file_handler.setLevel(log_level)
        file_handler.setFormatter(formatter)
        root_logger.addHandler(file_handler)

    # Console handler for critical errors
    console_handler = logging.StreamHandler()
    console_handler.setLevel(logging.ERROR)  # Only show errors on console from subprocesses
    console_handler.setFormatter(formatter)
    root_logger.addHandler(console_handler)

    # Suppress verbose logs from third-party libraries
    logging.getLogger("openai").setLevel(logging.WARNING)
    logging.getLogger("httpcore").setLevel(logging.WARNING)
    logging.getLogger("httpx").setLevel(logging.WARNING)
    logging.getLogger("openai._base_client").setLevel(logging.WARNING)
    logging.getLogger("httpcore.connection").setLevel(logging.WARNING)
    logging.getLogger("anthropic").setLevel(logging.WARNING)
    logging.getLogger("anthropic._base_client").setLevel(logging.WARNING)
    logging.getLogger("openinference.instrumentation.langchain").setLevel(logging.INFO)


def initializers(
    *,
    cpu_limit: int | None = None,
    log_level: int | None = None,
    log_file: str | None = None,
) -> list[tuple[Callable, Any]]:
    ans: list[tuple[Callable, Any]] = [
        (set_cpu_limit_inner, (cpu_limit,)),
    ]

    # Setup logging in subprocess
    if log_level is not None:
        ans.append((setup_subprocess_logging, (log_file, log_level)))

    return ans
