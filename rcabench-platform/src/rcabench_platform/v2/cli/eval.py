from typing import Annotated

import polars as pl
import typer

from ..algorithms.spec import global_algorithm_registry
from ..datasets.spec import get_dataset_index_path, get_dataset_list
from ..experiments.batch import run_batch
from ..experiments.report import generate_perf_report
from ..experiments.single import run_single
from ..logging import logger, timeit

app = typer.Typer()


@app.command()
def show_algorithms():
    registry = global_algorithm_registry()
    logger.info(f"Available algorithms ({len(registry)}):")
    for name in registry:
        logger.info(f"    {name}")


@app.command()
def show_datasets():
    datasets = get_dataset_list()
    logger.info(f"Available datasets ({len(datasets)}):")
    for dataset in datasets:
        index_lf = pl.scan_parquet(get_dataset_index_path(dataset))
        datapack_count = index_lf.select(pl.len()).collect().item()
        logger.info(f"    {dataset:<24} ({datapack_count:>4} datapacks)")


@app.command()
@timeit()
def single(
    algorithm: str,
    dataset: str,
    datapack: str,
    clear: bool = False,
    skip_finished: bool = True,
    sampler: str | None = None,
    sampling_rate: float | None = None,
    sampling_mode: str | None = None,
):
    run_single(
        algorithm,
        dataset,
        datapack,
        clear=clear,
        skip_finished=skip_finished,
        sampler=sampler,
        sampling_rate=sampling_rate,
        sampling_mode=sampling_mode,
    )


@app.command()
@timeit()
def batch(
    algorithms: Annotated[list[str], typer.Option("-a", "--algorithms")],
    datasets: Annotated[list[str], typer.Option("-d", "--datasets")],
    sample: int | None = None,
    clear: bool = False,
    skip_finished: bool = True,
    use_cpus: int | None = None,
    ignore_exceptions: bool = True,
    include_sampled: bool = False,
    samplers: Annotated[list[str] | None, typer.Option("-s", "--sampler")] = None,
    sampling_rates: Annotated[list[float] | None, typer.Option("-r", "--sampling-rate")] = None,
    sampling_modes: Annotated[list[str] | None, typer.Option("-m", "--sampling-mode")] = None,
):
    run_batch(
        algorithms,
        datasets,
        sample=sample,
        clear=clear,
        skip_finished=skip_finished,
        use_cpus=use_cpus,
        ignore_exceptions=ignore_exceptions,
        include_sampled=include_sampled,
        samplers=samplers,
        sampling_rates=sampling_rates,
        sampling_modes=sampling_modes,
    )


@app.command()
@timeit()
def perf_report(dataset: str, warn_missing: bool = False, include_sampled: bool = False):
    generate_perf_report(dataset, warn_missing=warn_missing, include_sampled=include_sampled)
