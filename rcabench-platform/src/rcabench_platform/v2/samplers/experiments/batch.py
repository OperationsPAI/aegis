"""Batch sampler execution module."""

import functools
import multiprocessing
import random
import sys
import time

from ...logging import logger, timeit
from ..spec import SamplingMode, global_sampler_registry
from .single import run_sampler_single


@timeit(log_level="INFO")
def run_sampler_batch(
    samplers: list[str],
    datasets: list[str],
    sampling_rates: list[float],
    modes: list[SamplingMode],
    *,
    sample_datapacks: int | None = None,
    clear: bool = False,
    skip_finished: bool = True,
    use_cpus: int | None = None,
    ignore_exceptions: bool = True,
):
    """
    Run multiple samplers on multiple datasets in batch.

    Args:
        samplers: List of sampler names
        datasets: List of dataset names
        sampling_rates: List of sampling rates to test
        modes: List of sampling modes to test
        sample_datapacks: If set, randomly sample this many datapacks per dataset
        clear: Whether to clear existing output
        skip_finished: Whether to skip already finished experiments
        use_cpus: Number of CPUs to use (default: all available - 4)
        ignore_exceptions: Whether to ignore exceptions and continue
    """
    from ...datasets.spec import get_datapack_list

    registry = global_sampler_registry()
    for sampler in samplers:
        assert sampler in registry, f"Sampler {sampler} not found in registry"

    logger.debug(f"samplers={samplers}")
    logger.debug(f"datasets={datasets}")
    logger.debug(f"sampling_rates={sampling_rates}")
    logger.debug(f"modes={[m.value for m in modes]}")

    for dataset in datasets:
        datapacks = get_datapack_list(dataset)

        if sample_datapacks is not None:
            assert sample_datapacks > 0
            k = min(sample_datapacks, len(datapacks))
            datapacks = random.sample(datapacks, k)

        logger.info(f"Processing dataset {dataset} with {len(datapacks)} datapacks")

        for sampler in samplers:
            sampler_instance = registry[sampler]()
            sampler_cpu_count = sampler_instance.needs_cpu_count()

            if sampler_cpu_count is None:
                parallel = 0
            else:
                assert sampler_cpu_count > 0
                usable_cpu_count = use_cpus or max(multiprocessing.cpu_count() - 4, 0)
                parallel = usable_cpu_count // sampler_cpu_count

            del sampler_instance

            tasks = []
            for datapack in datapacks:
                for sampling_rate in sampling_rates:
                    for mode in modes:
                        tasks.append(
                            functools.partial(
                                run_sampler_single,
                                sampler,
                                dataset,
                                datapack,
                                sampling_rate,
                                mode,
                                clear=clear,
                                skip_finished=skip_finished,
                            )
                        )

            logger.info(f"Running {len(tasks)} sampler tasks for {sampler} on {dataset}")

            # Import here to avoid circular imports
            from ...utils.fmap import fmap_processpool

            t0 = time.time()
            fmap_processpool(
                tasks,
                parallel=parallel,
                cpu_limit_each=sampler_cpu_count,
                ignore_exceptions=ignore_exceptions,
            )
            t1 = time.time()

            total_walltime = t1 - t0
            avg_walltime = total_walltime / len(tasks)

            logger.debug(f"Total   walltime: {total_walltime:.3f} seconds")
            logger.debug(f"Average walltime: {avg_walltime:.3f} seconds")

            logger.debug(f"Finished running sampler `{sampler}` on dataset `{dataset}`")

            sys.stdout.flush()
