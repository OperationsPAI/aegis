import datetime
import json
import math
from pathlib import Path
from typing import Any

import polars as pl

from ....datasets.rcabench import get_parent_resource_from_pod_name
from ....logging import logger, timeit
from ....pedestals import get_pedestal
from ....utils.serde import load_json
from ..defintion import SDG, DepEdge, DepKind, Indicator, PlaceKind, PlaceNode
from .common import (
    add_node_opt,
    calc_metric_min_max,
    is_constant_metric,
    replace_enum_values,
)


@timeit()
def build_sdg_from_rcabench(dataset: str, datapack: str, input_folder: Path) -> SDG:
    sdg = SDG()
    sdg.data["dataset"] = dataset
    sdg.data["datapack"] = datapack

    inject_time = load_inject_time(input_folder)
    sdg.data["inject_time"] = inject_time

    metrics = load_metrics(input_folder)
    metrics_histogram = load_metrics_histogram(input_folder)
    traces = load_traces(input_folder)
    logs = load_logs(input_folder)

    logger.debug("loading all dataframes")
    dataframes = pl.collect_all([metrics, metrics_histogram, traces, logs])
    metrics, metrics_histogram, traces, logs = dataframes

    logger.debug(f"len(metrics) = {len(metrics)}")
    logger.debug(f"len(metrics_histogram) = {len(metrics_histogram)}")
    logger.debug(f"len(traces)  = {len(traces)}")
    logger.debug(f"len(logs)    = {len(logs)}")

    apply_metrics(sdg, metrics)
    del metrics

    apply_metrics_histogram(sdg, metrics_histogram)
    del metrics_histogram

    apply_traces_and_logs(sdg, traces, logs)
    del traces, logs

    apply_services_link(sdg)

    apply_extra_indicators(sdg)

    apply_detector_conclusion(sdg, input_folder)

    return sdg


def load_inject_time(input_folder: Path) -> datetime.datetime:
    env = load_json(path=input_folder / "env.json")

    # tz = dateutil.tz.gettz(env["TIMEZONE"])

    normal_start = int(env["NORMAL_START"])
    normal_end = int(env["NORMAL_END"])

    abnormal_start = int(env["ABNORMAL_START"])
    abnormal_end = int(env["ABNORMAL_END"])

    assert normal_start < normal_end <= abnormal_start < abnormal_end

    if normal_end < abnormal_start:
        inject_time = int(math.ceil(normal_end + abnormal_start) / 2)
    else:
        inject_time = abnormal_start

    inject_time = datetime.datetime.fromtimestamp(inject_time, tz=datetime.timezone.utc)
    logger.debug(f"inject_time=`{inject_time}`")

    return inject_time


def merge_two_time_ranges(normal: pl.LazyFrame, anomal: pl.LazyFrame) -> pl.LazyFrame:
    assert "anomal" not in normal.collect_schema().names()
    assert "anomal" not in anomal.collect_schema().names()
    normal = normal.with_columns(anomal=pl.lit(0, dtype=pl.UInt8))
    anomal = anomal.with_columns(anomal=pl.lit(1, dtype=pl.UInt8))
    merged = pl.concat([normal, anomal])
    return merged


@timeit()
def load_metrics(input_folder: Path) -> pl.LazyFrame:
    normal_metrics = pl.scan_parquet(input_folder / "normal_metrics.parquet")
    anomal_metrics = pl.scan_parquet(input_folder / "abnormal_metrics.parquet")
    lf = merge_two_time_ranges(normal_metrics, anomal_metrics)

    return lf


METRIC_PREFIX_PLACE_KIND: dict[str, PlaceKind] = {
    "k8s.pod.": PlaceKind.pod,
    "jvm.cpu.recent_utilization": PlaceKind.pod,
    "jvm.system.cpu.load_1m": PlaceKind.pod,
    "jvm.system.cpu.utilization": PlaceKind.pod,
    "queueSize": PlaceKind.pod,
    #
    "k8s.container.": PlaceKind.container,
    "container.": PlaceKind.container,
    #
    "k8s.replicaset.": PlaceKind.replica_set,
    "k8s.deployment.": PlaceKind.deployment,
    "k8s.statefulset.": PlaceKind.stateful_set,
    #
    "hubble_": PlaceKind.service,
}


def is_special_constant_metric(metric: str) -> bool:
    return metric in (
        "k8s.container.cpu_request",
        "k8s.container.memory_request",
        "k8s.container.cpu_limit",
        "k8s.container.memory_limit",
    )


@timeit(log_args=False)
def apply_metrics(sdg: SDG, df: pl.DataFrame) -> None:
    df_map = df.partition_by("metric", as_dict=True)
    del df

    for (metric,), df in df_map.items():
        assert isinstance(metric, str) and metric

        if (not is_special_constant_metric(metric)) and is_constant_metric(df):
            logger.debug(f"ignore constant metric `{metric}`")
            continue

        found = False
        for prefix, place_kind in METRIC_PREFIX_PLACE_KIND.items():
            if metric.startswith(prefix):
                found = True
                apply_place_metrics(sdg, df, place_kind, metric)
                break

        if found:
            continue

        logger.warning(f"unhandled metric `{metric}` len = {len(df)}")


PLACE_METRICS_PARTITION: dict[PlaceKind, str] = {
    PlaceKind.pod: "attr.k8s.pod.name",
    PlaceKind.container: "attr.k8s.container.name",
    PlaceKind.stateful_set: "attr.k8s.statefulset.name",
    PlaceKind.deployment: "attr.k8s.deployment.name",
    PlaceKind.replica_set: "attr.k8s.replicaset.name",
    PlaceKind.machine: "attr.k8s.node.name",
    PlaceKind.service: "service_name",
}


def apply_place_metrics(sdg: SDG, df: pl.DataFrame, place_kind: PlaceKind, metric: str) -> None:
    df_map = df.partition_by(PLACE_METRICS_PARTITION[place_kind], as_dict=True)
    del df

    for (place_name,), df in df_map.items():
        assert isinstance(place_name, str) and place_name

        if (not is_special_constant_metric(metric)) and is_constant_metric(df):
            logger.debug(f"ignore constant metric `{metric}` for place `{place_name}`")
            continue

        apply_k8s_places(sdg, df)

        place_node = sdg.add_node(PlaceNode(kind=place_kind, self_name=place_name), strict=False)

        place_node.add_indicator(
            Indicator(
                name=metric,
                df=df.select("time", "anomal", "value"),
            )
        )


def apply_k8s_places(sdg: SDG, df: pl.DataFrame) -> None:
    df = df.select(
        "attr.k8s.node.name",
        "attr.k8s.namespace.name",
        "attr.k8s.statefulset.name",
        "attr.k8s.deployment.name",
        "attr.k8s.replicaset.name",
        "attr.k8s.pod.name",
        "attr.k8s.container.name",
    )

    for (
        machine_name,
        namespace_name,
        stateful_set_name,
        deployment_name,
        replica_set_name,
        pod_name,
        container_name,
    ) in df.iter_rows():
        if pod_name is not None:
            resources = get_parent_resource_from_pod_name(pod_name)
            if resources[0] == "Deployment":
                if deployment_name:
                    assert deployment_name == resources[1]
                else:
                    deployment_name = resources[1]

                if replica_set_name:
                    assert replica_set_name == resources[2]
                else:
                    replica_set_name = resources[2]
            elif resources[0] == "StatefulSet":
                if stateful_set_name:
                    assert stateful_set_name == resources[1]
                else:
                    stateful_set_name = resources[1]
            else:
                pass

        machine_node = add_node_opt(sdg, PlaceKind.machine, machine_name)
        namespace_node = add_node_opt(sdg, PlaceKind.namespace, namespace_name)
        stateful_set_node = add_node_opt(sdg, PlaceKind.stateful_set, stateful_set_name)
        deployment_node = add_node_opt(sdg, PlaceKind.deployment, deployment_name)
        replica_set_node = add_node_opt(sdg, PlaceKind.replica_set, replica_set_name)
        pod_node = add_node_opt(sdg, PlaceKind.pod, pod_name)
        container_node = add_node_opt(sdg, PlaceKind.container, container_name)

        if pod_node and container_node:
            sdg.add_edge(
                DepEdge(
                    src_id=pod_node.id,
                    dst_id=container_node.id,
                    kind=DepKind.runs,
                ),
                strict=False,
            )

        if machine_node and pod_node:
            sdg.add_edge(
                DepEdge(
                    src_id=machine_node.id,
                    dst_id=pod_node.id,
                    kind=DepKind.schedules,
                ),
                strict=False,
            )

        if stateful_set_node and pod_node:
            sdg.add_edge(
                DepEdge(
                    src_id=stateful_set_node.id,
                    dst_id=pod_node.id,
                    kind=DepKind.manages,
                ),
                strict=False,
            )

        if replica_set_node and pod_node:
            sdg.add_edge(
                DepEdge(
                    src_id=replica_set_node.id,
                    dst_id=pod_node.id,
                    kind=DepKind.manages,
                ),
                strict=False,
            )

        if deployment_node and replica_set_node:
            sdg.add_edge(
                DepEdge(
                    src_id=deployment_node.id,
                    dst_id=replica_set_node.id,
                    kind=DepKind.scales,
                ),
                strict=False,
            )

        if namespace_node and deployment_node:
            sdg.add_edge(
                DepEdge(
                    src_id=namespace_node.id,
                    dst_id=deployment_node.id,
                    kind=DepKind.owns,
                ),
                strict=False,
            )

        if namespace_node and stateful_set_node:
            sdg.add_edge(
                DepEdge(
                    src_id=namespace_node.id,
                    dst_id=stateful_set_node.id,
                    kind=DepKind.owns,
                ),
                strict=False,
            )


@timeit()
def load_metrics_histogram(input_folder: Path) -> pl.LazyFrame:
    normal_histogram = pl.scan_parquet(input_folder / "normal_metrics_histogram.parquet")
    anomal_histogram = pl.scan_parquet(input_folder / "abnormal_metrics_histogram.parquet")
    lf = merge_two_time_ranges(normal_histogram, anomal_histogram)

    lf = lf.with_columns(
        pl.when(pl.col("metric") == "jvm.gc.duration")
        .then(pl.concat_str("metric", "attr.jvm.gc.name", separator=":").alias("metric"))
        .otherwise(pl.col("metric"))
    )

    return lf


@timeit(log_args=False)
def apply_metrics_histogram(sdg: SDG, df: pl.DataFrame) -> None:
    df = df.with_columns(pl.col("metric").str.starts_with("hubble").alias("_is_hubble"))

    df_map = df.partition_by("_is_hubble", as_dict=True)
    del df

    for (is_hubble,), df in df_map.items():
        if is_hubble is True:
            apply_hubble_metrics_histogram(sdg, df)
        elif is_hubble is False:
            apply_k8s_metrics_histogram(sdg, df)
        else:
            raise ValueError(f"Unexpected value for `_is_hubble`: {is_hubble}")


def apply_hubble_metrics_histogram(sdg: SDG, df: pl.DataFrame) -> None:
    # TODO: partition by (destination, source)

    df_map = df.partition_by("metric", "service_name", as_dict=True)
    del df

    for (metric, service_name), df in df_map.items():
        assert isinstance(metric, str) and metric
        assert isinstance(service_name, str) and service_name

        service_node = sdg.add_node(PlaceNode(kind=PlaceKind.service, self_name=service_name), strict=False)

        service_node.add_indicator(
            Indicator(
                name=metric + ":hist.sum",
                df=df.select(
                    "time",
                    "anomal",
                    pl.col("sum").alias("value"),
                    "count",
                    "sum",
                    "min",
                    "max",
                ),
            )
        )


def apply_k8s_metrics_histogram(sdg: SDG, df: pl.DataFrame) -> None:
    assert df["attr.k8s.pod.name"].is_not_null().all()
    assert df["attr.k8s.service.name"].is_not_null().all()
    assert df["attr.k8s.namespace.name"].is_not_null().all()

    df_map = df.partition_by("metric", "attr.k8s.pod.name", as_dict=True)
    del df

    for (metric, pod_name), df in df_map.items():
        assert isinstance(metric, str) and metric
        assert isinstance(pod_name, str) and pod_name

        pod_node = sdg.add_node(PlaceNode(kind=PlaceKind.pod, self_name=pod_name), strict=False)

        pod_node.add_indicator(
            Indicator(
                name=metric + ":hist.sum",
                df=df.select(
                    "time",
                    "anomal",
                    pl.col("sum").alias("value"),
                    "count",
                    "sum",
                    "min",
                    "max",
                ),
            )
        )

        apply_pod_service_namespace(sdg, df, pod_node)


def apply_pod_service_namespace(sdg: SDG, df: pl.DataFrame, pod_node: PlaceNode) -> None:
    selected = df.select("attr.k8s.service.name", "attr.k8s.namespace.name").unique()
    for service_name, namespace_name in selected.iter_rows():
        assert isinstance(service_name, str) and service_name
        assert isinstance(namespace_name, str) and namespace_name

        service_node = sdg.add_node(PlaceNode(kind=PlaceKind.service, self_name=service_name), strict=False)
        namespace_node = sdg.add_node(PlaceNode(kind=PlaceKind.namespace, self_name=namespace_name), strict=False)

        sdg.add_edge(
            DepEdge(
                src_id=namespace_node.id,
                dst_id=service_node.id,
                kind=DepKind.owns,
            ),
            strict=False,
        )

        sdg.add_edge(
            DepEdge(
                src_id=service_node.id,
                dst_id=pod_node.id,
                kind=DepKind.routes_to,
            ),
            strict=False,
        )


@timeit()
def load_traces(input_folder: Path, system: str = "ts") -> pl.LazyFrame:
    pedestal = get_pedestal(system)

    normal_traces = pl.scan_parquet(input_folder / "normal_traces.parquet")
    anomal_traces = pl.scan_parquet(input_folder / "abnormal_traces.parquet")
    lf = merge_two_time_ranges(normal_traces, anomal_traces)

    lf = pedestal.add_op_name(lf)

    status_code_values = ["Unset", "Ok", "Error"]
    lf = lf.with_columns(
        replace_enum_values("attr.status_code", status_code_values, start=0),
    )

    lf = lf.with_columns(
        pl.col("duration").cast(pl.Float64),
        pl.col("attr.http.response.status_code").cast(pl.Float64),
        pl.col("attr.http.request.content_length").cast(pl.Float64),
        pl.col("attr.http.response.content_length").cast(pl.Float64),
    )

    return lf


@timeit()
def load_logs(input_folder: Path) -> pl.LazyFrame:
    normal_logs = pl.scan_parquet(input_folder / "normal_logs.parquet")
    anomal_logs = pl.scan_parquet(input_folder / "abnormal_logs.parquet")
    lf = merge_two_time_ranges(normal_logs, anomal_logs)

    level_values = ["", "TRACE", "DEBUG", "INFO", "WARN", "ERROR", "SEVERE"]
    lf = lf.with_columns(pl.col("level").str.replace("WARNING", "WARN", literal=True))
    lf = lf.with_columns(replace_enum_values("level", level_values, start=0).alias("level_number"))

    return lf


@timeit(log_args=False)
def apply_traces_and_logs(sdg: SDG, traces: pl.DataFrame, logs: pl.DataFrame, system: str = "ts") -> None:
    pedestal = get_pedestal(system)
    traces, id2op, id2parent = pedestal.fix_client_spans(traces)

    df_map = traces.partition_by("op_name", as_dict=True)
    del traces

    for (op_name,), df in df_map.items():
        assert isinstance(op_name, str) and op_name

        if len(df) == 0:
            continue

        if op_name.endswith("GET") or op_name.endswith("POST"):
            continue

        function_node = sdg.add_node(
            PlaceNode(kind=PlaceKind.function, self_name=op_name),
            strict=False,
        )

        fn_indicator_cols = [
            "duration",
            "attr.status_code",
            "attr.http.response.status_code",
            "attr.http.request.content_length",
            "attr.http.response.content_length",
        ]

        fn_lf_list = []
        for col_name in fn_indicator_cols:
            indicator_lf = (
                df.lazy()
                .filter(pl.col(col_name).is_not_null())
                .select(
                    "time",
                    "anomal",
                    pl.col(col_name).alias("value"),
                    "trace_id",
                    "span_id",
                    "parent_span_id",
                    "attr.span_kind",
                )
            )
            fn_lf_list.append(indicator_lf)

        fn_indicator_df_list = pl.collect_all(fn_lf_list)

        for col_name, indicator_df in zip(fn_indicator_cols, fn_indicator_df_list):
            if len(indicator_df) == 0:
                continue

            function_node.add_indicator(
                Indicator(
                    name=col_name,
                    df=indicator_df,
                )
            )

        selected = df.select(
            "span_id",
            "attr.k8s.pod.name",
            "attr.k8s.service.name",
            "attr.k8s.namespace.name",
        )
        for span_id, pod_name, service_name, service_namespace in selected.iter_rows():
            assert isinstance(span_id, str) and span_id

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
                    if parent_node.id == function_node.id:
                        logger.warning(f"self loop for function `{op_name}` span_id=`{span_id}`")

            if service_name is None:
                logger.warning(f"op_name `{op_name}` missing service_name")

            service_node = add_node_opt(sdg, PlaceKind.service, service_name)
            if service_node:
                sdg.add_edge(
                    DepEdge(
                        src_id=service_node.id,
                        dst_id=function_node.id,
                        kind=DepKind.includes,
                    ),
                    strict=False,
                )

            namespace_node = add_node_opt(sdg, PlaceKind.namespace, service_namespace)
            if namespace_node and service_node:
                sdg.add_edge(
                    DepEdge(
                        src_id=namespace_node.id,
                        dst_id=service_node.id,
                        kind=DepKind.owns,
                    ),
                    strict=False,
                )

            pod_node = add_node_opt(sdg, PlaceKind.pod, pod_name)
            if pod_node:
                resources = get_parent_resource_from_pod_name(pod_name)
                if resources[0] == "Deployment":
                    deployment_name = resources[1]
                    replica_set_name = resources[2]
                    assert deployment_name and replica_set_name

                    deployment_node = sdg.add_node(
                        PlaceNode(kind=PlaceKind.deployment, self_name=deployment_name),
                        strict=False,
                    )

                    replica_set_node = sdg.add_node(
                        PlaceNode(kind=PlaceKind.replica_set, self_name=replica_set_name),
                        strict=False,
                    )

                    sdg.add_edge(
                        DepEdge(
                            src_id=deployment_node.id,
                            dst_id=replica_set_node.id,
                            kind=DepKind.scales,
                        ),
                        strict=False,
                    )

                    sdg.add_edge(
                        DepEdge(
                            src_id=replica_set_node.id,
                            dst_id=pod_node.id,
                            kind=DepKind.manages,
                        ),
                        strict=False,
                    )
                elif resources[0] == "StatefulSet":
                    stateful_set_name = resources[1]
                    assert stateful_set_name

                    stateful_set_node = sdg.add_node(
                        PlaceNode(kind=PlaceKind.stateful_set, self_name=stateful_set_name),
                        strict=False,
                    )
                    sdg.add_edge(
                        DepEdge(
                            src_id=stateful_set_node.id,
                            dst_id=pod_node.id,
                            kind=DepKind.manages,
                        ),
                        strict=False,
                    )
                else:
                    pass

                if service_node and pod_node:
                    sdg.add_edge(
                        DepEdge(
                            src_id=service_node.id,
                            dst_id=pod_node.id,
                            kind=DepKind.routes_to,
                        ),
                        strict=False,
                    )

    logs = logs.with_columns(
        pl.col("span_id").map_elements(lambda x: id2op.get(x), return_dtype=pl.String).alias("op_name")
    )

    df_map = logs.partition_by("op_name", as_dict=True)
    for (op_name,), df in df_map.items():
        if not op_name:
            continue
        assert isinstance(op_name, str) and op_name

        if len(df) == 0:
            continue

        function_node = sdg.query_node_by_kind(PlaceKind.function, op_name)
        assert function_node, op_name

        function_node.add_indicator(
            Indicator(
                name="log_level",
                df=df.select(
                    "time",
                    "anomal",
                    pl.col("level_number").alias("value"),
                    "level",
                    "message",
                    "trace_id",
                    "span_id",
                ),
            )
        )


def apply_services_link(sdg: SDG) -> None:
    for service_node in sdg.get_all_nodes_by_kind(PlaceKind.service):
        deployment_node = sdg.query_node_by_kind(
            PlaceKind.deployment,
            service_node.self_name,
        )
        if deployment_node:
            sdg.add_edge(
                DepEdge(
                    src_id=service_node.id,
                    dst_id=deployment_node.id,
                    kind=DepKind.related_to,
                ),
                strict=False,
            )

        stateful_set_node = sdg.query_node_by_kind(
            PlaceKind.stateful_set,
            service_node.self_name,
        )
        if stateful_set_node:
            sdg.add_edge(
                DepEdge(
                    src_id=service_node.id,
                    dst_id=stateful_set_node.id,
                    kind=DepKind.related_to,
                ),
                strict=False,
            )

    for pod_node in sdg.get_all_nodes_by_kind(PlaceKind.pod):
        resource = get_parent_resource_from_pod_name(pod_node.self_name)
        if resource[1]:
            service_node = sdg.add_node(PlaceNode(kind=PlaceKind.service, self_name=resource[1]), strict=False)
            sdg.add_edge(
                DepEdge(src_id=service_node.id, dst_id=pod_node.id, kind=DepKind.routes_to),
                strict=False,
            )


def apply_extra_indicators(sdg: SDG) -> None:
    pairs = [
        ("container.cpu.usage", "k8s.container.cpu_request"),
        ("container.memory.usage", "k8s.container.memory_request"),
    ]
    for container in sdg.get_all_nodes_by_kind(PlaceKind.container):
        for usage, request in pairs:
            usage_indicator = container.indicators.get(usage)
            request_indicator = container.indicators.get(request)
            if usage_indicator is None or request_indicator is None:
                continue

            request_min, request_max = calc_metric_min_max(request_indicator.df)
            if not ((request_max - request_min) < 1e-8):
                continue
            request_value = (request_max + request_min) / 2

            df = usage_indicator.df.select("time", "anomal", pl.col("value").truediv(request_value))
            container.add_indicator(Indicator(name=usage + ":request_percentage", df=df))


def apply_detector_conclusion(sdg: SDG, input_folder: Path) -> None:
    conclusion_path = input_folder / "conclusion.parquet"
    if not conclusion_path.exists():
        return

    df = pl.read_parquet(conclusion_path, columns=["SpanName", "Issues"])
    if len(df) == 0:
        return

    ts_ui_dashboard = sdg.query_node_by_kind(PlaceKind.service, "ts-ui-dashboard")
    assert ts_ui_dashboard

    ts_ui_dashboard_functions: list[PlaceNode] = []
    for edge in sdg.out_edges(ts_ui_dashboard.id):
        if edge.kind == DepKind.includes:
            dst_node = sdg.get_node_by_id(edge.dst_id)
            ts_ui_dashboard_functions.append(dst_node)

    span_names = {}
    for span_name, issues in df.select("SpanName", "Issues").iter_rows():
        assert isinstance(span_name, str) and span_name
        assert isinstance(issues, str) and issues
        span_names[span_name] = json.loads(issues)

    sli_nodes: list[dict[str, Any]] = []
    for span_name, issues in span_names.items():
        assert isinstance(span_name, str) and span_name
        for node in ts_ui_dashboard_functions:
            if span_name in node.self_name:
                sli_nodes.append(
                    {
                        "node.id": node.id,
                        "node.self_name": node.self_name,
                        "issues": issues,
                    }
                )
                break

    sdg.data["detector.sli.nodes"] = sli_nodes
