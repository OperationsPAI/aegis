import functools
import itertools
import multiprocessing
import os
import random
import sys
import time

from ..algorithms.spec import global_algorithm_registry
from ..datasets.spec import get_datapack_folder, get_datapack_list
from ..logging import logger, timeit
from ..samplers.spec import SamplingMode
from ..utils.fmap import fmap_processpool
from .single import run_single


@timeit(log_level="INFO")
def run_batch(
    algorithms: list[str],
    datasets: list[str],
    *,
    sample: int | None = None,
    clear: bool = False,
    skip_finished: bool = True,
    use_cpus: int | None = None,
    ignore_exceptions: bool = True,
    include_sampled: bool = False,
    samplers: list[str] | None = None,
    sampling_rates: list[float] | None = None,
    sampling_modes: list[str] | None = None,
):
    registry = global_algorithm_registry()
    for algorithm in algorithms:
        assert algorithm in registry

    logger.debug(f"algorithms=`{algorithms}`")

    # Auto-detect available sampler configurations if include_sampled is True
    if include_sampled:
        # Use the same scanning logic as sampler report
        from ..samplers.experiments.report import _scan_available_configurations

        (
            available_samplers,
            available_rates,
            available_modes,
        ) = _scan_available_configurations(datasets)

        if samplers is None:
            samplers = available_samplers
            logger.info(f"Auto-detected samplers: {samplers}")

        if sampling_rates is None:
            sampling_rates = available_rates
            logger.info(f"Auto-detected sampling rates: {sampling_rates}")

        if sampling_modes is None:
            sampling_modes = [mode.value for mode in available_modes]
            logger.info(f"Auto-detected sampling modes: {sampling_modes}")

    for dataset in datasets:
        datapacks = get_datapack_list(dataset)

        if sample is not None:
            assert sample > 0
            k = min(sample, len(datapacks))
            datapacks = random.sample(datapacks, k)

        for algorithm in algorithms:
            alg = registry[algorithm]()
            alg_cpu_count = alg.needs_cpu_count()

            if alg_cpu_count is None:
                parallel = 0
            else:
                assert alg_cpu_count > 0
                usable_cpu_count = use_cpus or max(multiprocessing.cpu_count() - 4, 0)
                parallel = usable_cpu_count // alg_cpu_count

            del alg

            tasks = []

            # Add regular (non-sampled) tasks
            if not include_sampled:
                for datapack in datapacks:
                    tasks.append(
                        functools.partial(
                            run_single,
                            algorithm,
                            dataset,
                            datapack,
                            clear=clear,
                            skip_finished=skip_finished,
                            sampler=None,
                            sampling_rate=None,
                            sampling_mode=None,
                        )
                    )
            # Add sampled tasks
            if include_sampled and samplers and sampling_rates and sampling_modes:
                # Use itertools.product to avoid deep nesting
                for (
                    datapack,
                    sampler,
                    sampling_rate,
                    sampling_mode,
                ) in itertools.product(datapacks, samplers, sampling_rates, sampling_modes):
                    # Check if this sampler configuration exists for this datapack
                    datapack_folder = get_datapack_folder(dataset, datapack)
                    sampler_path = datapack_folder / "sampled" / f"{sampler}_{sampling_rate}_{sampling_mode}"

                    if sampler_path.exists() and sampler_path.is_dir():
                        tasks.append(
                            functools.partial(
                                run_single,
                                algorithm,
                                dataset,
                                datapack,
                                clear=clear,
                                skip_finished=skip_finished,
                                sampler=sampler,
                                sampling_rate=sampling_rate,
                                sampling_mode=sampling_mode,
                            )
                        )

            t0 = time.time()
            fmap_processpool(
                tasks,
                parallel=parallel,
                cpu_limit_each=alg_cpu_count,
                ignore_exceptions=ignore_exceptions,
            )
            t1 = time.time()

            total_walltime = t1 - t0
            avg_walltime = total_walltime / len(tasks) if len(tasks) > 0 else 0

            logger.debug(f"Total   walltime: {total_walltime:.3f} seconds")
            logger.debug(f"Average walltime: {avg_walltime:.3f} seconds")

            if include_sampled:
                logger.debug(
                    f"Finished running algorithm `{algorithm}` on sampled dataset `{dataset}` ({len(tasks)} tasks)"
                )
            else:
                logger.debug(f"Finished running algorithm `{algorithm}` on dataset `{dataset}`")

            sys.stdout.flush()
