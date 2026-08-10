import functools
from typing import Annotated

import typer

from ..datasets.spec import get_datapack_folder, get_dataset_list
from ..logging import logger, timeit
from ..samplers.experiments.batch import run_sampler_batch
from ..samplers.experiments.report import generate_sampler_perf_report
from ..samplers.experiments.single import run_sampler_single
from ..samplers.metrics_sli import generate_metrics_sli
from ..samplers.spec import SamplingMode, global_sampler_registry
from ..utils.fmap import fmap_processpool

app = typer.Typer()


@app.command()
def show_samplers():
    """Show available sampler algorithms."""
    registry = global_sampler_registry()
    logger.info(f"Available samplers ({len(registry)}):")
    for name in registry:
        logger.info(f"    {name}")


@app.command()
@timeit()
def single(
    sampler: str,
    dataset: str,
    datapack: str,
    sampling_rate: float = 0.1,
    mode: SamplingMode = SamplingMode.OFFLINE,
    clear: bool = False,
    skip_finished: bool = True,
):
    """Run a single sampler on a datapack."""
    run_sampler_single(
        sampler=sampler,
        dataset=dataset,
        datapack=datapack,
        sampling_rate=sampling_rate,
        mode=mode,
        clear=clear,
        skip_finished=skip_finished,
    )


@app.command()
@timeit()
def batch(
    samplers: Annotated[list[str], typer.Option("-s", "--sampler")],
    datasets: Annotated[list[str], typer.Option("-d", "--dataset")],
    sampling_rates: Annotated[list[float], typer.Option("-r", "--rate")] = [0.1],
    modes: Annotated[list[SamplingMode], typer.Option("-m", "--mode")] = [SamplingMode.OFFLINE],
    sample_datapacks: int | None = None,
    clear: bool = False,
    skip_finished: bool = True,
    use_cpus: int | None = None,
    ignore_exceptions: bool = True,
):
    """Run multiple samplers on multiple datasets."""
    run_sampler_batch(
        samplers=samplers,
        datasets=datasets,
        sampling_rates=sampling_rates,
        modes=modes,
        sample_datapacks=sample_datapacks,
        clear=clear,
        skip_finished=skip_finished,
        use_cpus=use_cpus,
        ignore_exceptions=ignore_exceptions,
    )


@app.command()
@timeit()
def perf_report(
    datasets: Annotated[list[str], typer.Option("-d", "--dataset")],
    samplers: Annotated[list[str], typer.Option("-s", "--sampler")] | None = None,
    sampling_rates: Annotated[list[float], typer.Option("-r", "--rate")] | None = None,
    modes: Annotated[list[SamplingMode], typer.Option("-m", "--mode")] | None = None,
    warn_missing: bool = False,
):
    """
    Generate performance report for samplers.

    By default, auto-detects all available samplers, sampling rates, and modes.
    Use filters to specify particular configurations:

    Examples:
        # Report all configurations for datasets
        sample perf-report -d dataset1 -d dataset2

        # Report specific sampler only
        sample perf-report -d dataset1 -s random

        # Report specific rates and modes
        sample perf-report -d dataset1 -r 0.1 -r 0.2 -m offline

        # Report specific combinations
        sample perf-report -d dataset1 -s random -s my_sampler -r 0.1 -m offline -m online
    """
    if not datasets:
        logger.error("At least one dataset must be specified with -d/--dataset")
        raise typer.Exit(1)

    # Show what will be processed
    if samplers is None:
        logger.info("Auto-detecting available samplers...")
    else:
        logger.info(f"Filtering samplers: {samplers}")

    if sampling_rates is None:
        logger.info("Auto-detecting available sampling rates...")
    else:
        logger.info(f"Filtering sampling rates: {sampling_rates}")

    if modes is None:
        logger.info("Auto-detecting available sampling modes...")
    else:
        logger.info(f"Filtering sampling modes: {[m.value for m in modes]}")

    generate_sampler_perf_report(
        datasets=datasets,
        samplers=samplers,
        sampling_rates=sampling_rates,
        modes=modes,
        warn_missing=warn_missing,
    )


@app.command()
@timeit()
def generate_sli_metrics(
    dataset: str,
    datapack: str | None = None,
):
    """Generate metrics_sli.parquet for dataset or specific datapack."""
    if datapack is not None:
        # Generate for specific datapack
        input_folder = get_datapack_folder(dataset, datapack)
        generate_metrics_sli(input_folder)
    else:
        # Generate for all datapacks in dataset
        from ..datasets.spec import get_datapack_list

        datapacks = get_datapack_list(dataset)
        logger.info(f"Generating metrics_sli.parquet for {len(datapacks)} datapacks in {dataset}")
        tasks = []

        for dp in datapacks:
            input_folder = get_datapack_folder(dataset, dp)
            tasks.append(functools.partial(generate_metrics_sli, input_folder))

        fmap_processpool(tasks, parallel=64)
