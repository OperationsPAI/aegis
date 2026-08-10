import datetime
from pathlib import Path

import polars as pl

from ....logging import logger, timeit
from ....pedestals import get_pedestal
from ..defintion import SDG, DepEdge, DepKind, Indicator, PlaceKind, PlaceNode
from .common import is_constant_metric


@timeit()
def build_sdg_from_rcaeval(dataset: str, datapack: str, input_folder: Path, system: str = "ts") -> SDG:
    sdg = SDG()
    sdg.data["dataset"] = dataset
    sdg.data["datapack"] = datapack

    inject_time = load_inject_time(input_folder)
    sdg.data["inject_time"] = inject_time

    traces = load_traces(input_folder, inject_time, system)
    metrics = load_metrics(input_folder, inject_time)

    logger.debug("loading all dataframes")
    dataframes = pl.collect_all([traces, metrics])
    traces, metrics = dataframes

    logger.debug(f"len(traces)={len(traces)}")
    logger.debug(f"len(metrics)={len(metrics)}")

    apply_metrics(sdg, metrics)
    del metrics

    apply_traces(sdg, traces, system)
    del traces

    return sdg


def load_inject_time(input_folder: Path) -> datetime.datetime:
    inject_time_file = input_folder / "inject_time.txt"

    with open(inject_time_file) as f:
        timestamp = int(f.read().strip())
    inject_time = datetime.datetime.fromtimestamp(timestamp, tz=datetime.timezone.utc)

    logger.debug(f"inject_time=`{inject_time}`")
    return inject_time


def load_traces(input_folder: Path, inject_time: datetime.datetime, system: str = "ts") -> pl.LazyFrame:
    pedestal = get_pedestal(system)

    lf = pl.scan_parquet(input_folder / "traces.parquet")
    lf = lf.with_columns((pl.col("time") >= inject_time).cast(pl.UInt8).alias("anomal"))
    lf = lf.with_columns(pl.col("duration").cast(pl.Float64))
    lf = pedestal.add_op_name(lf)

    return lf


def apply_traces(sdg: SDG, traces: pl.DataFrame, system: str = "ts") -> None:
    pedestal = get_pedestal(system)
    traces, id2op, id2parent = pedestal.fix_client_spans(traces)

    df_map = traces.partition_by("op_name", as_dict=True)
    for (op_name,), df in df_map.items():
        assert isinstance(op_name, str) and op_name
        assert len(df) > 0

        function_node = sdg.add_node(
            PlaceNode(kind=PlaceKind.function, self_name=op_name),
            strict=False,
        )

        function_node.add_indicator(
            Indicator(
                name="duration",
                df=df.select(
                    "time",
                    "anomal",
                    pl.col("duration").alias("value"),
                    "trace_id",
                    "span_id",
                    "parent_span_id",
                ),
            ),
            strict=False,
        )

        selected = df.select("span_id", "service_name")
        for span_id, service_name in selected.iter_rows():
            assert isinstance(span_id, str) and span_id
            assert isinstance(service_name, str) and service_name

            # function-function edges
            parent_span_id = id2parent.get(span_id)
            if parent_span_id:
                parent_op_name = id2op.get(parent_span_id)
                if parent_op_name:
                    parent_node = sdg.add_node(
                        PlaceNode(kind=PlaceKind.function, self_name=parent_op_name),
                        strict=False,
                    )
                    sdg.add_edge(
                        DepEdge(
                            src_id=parent_node.id,
                            dst_id=function_node.id,
                            kind=DepKind.calls,
                        ),
                        strict=False,
                    )

            assert service_name

            service_node = sdg.add_node(
                PlaceNode(kind=PlaceKind.service, self_name=service_name),
                strict=False,
            )

            sdg.add_edge(
                DepEdge(
                    src_id=service_node.id,
                    dst_id=function_node.id,
                    kind=DepKind.includes,
                ),
                strict=False,
            )

    top_op_names = set()
    for span_id, op_name in id2op.items():
        parent = id2parent.get(span_id)
        if parent is None or id2op.get(parent) is None:
            top_op_names.add(op_name)

    sdg.data["top_op_names"] = top_op_names

    if len(top_op_names) > 0:
        for op_name in top_op_names:
            logger.debug(f"top_op_name: {op_name}")
    else:
        logger.debug("No top_op_names")


def load_metrics(input_folder: Path, inject_time: datetime.datetime) -> pl.LazyFrame:
    lf = pl.scan_parquet(input_folder / "simple_metrics.parquet")

    lf = lf.with_columns((pl.col("time") >= inject_time).cast(pl.UInt8).alias("anomal"))

    return lf


def apply_metrics(sdg: SDG, metrics: pl.DataFrame) -> None:
    df_map = metrics.partition_by("metric", "service_name", as_dict=True)
    del metrics

    for (metric, service_name), df in df_map.items():
        assert isinstance(metric, str) and metric
        assert isinstance(service_name, str) and service_name

        if is_constant_metric(df):
            logger.debug(f"ignore constant metric `{metric}`")
            continue

        service_node = sdg.add_node(
            PlaceNode(kind=PlaceKind.service, self_name=service_name),
            strict=False,
        )

        df = (
            df.lazy()
            .select("time", "anomal", "value")
            .filter(pl.col("value").is_not_null(), pl.col("value").is_not_nan())
            .collect()
        )

        if len(df) == 0:
            continue

        service_node.add_indicator(Indicator(name=metric, df=df))
