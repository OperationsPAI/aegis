import datetime
import functools
import math
import traceback

import numpy as np
import polars as pl
import scipy.stats

from ...logging import logger, timeit
from .defintion import SDG, DepEdge, DepKind, Indicator, PlaceKind, PlaceNode


@timeit(log_args=False)
def calc_statistics(sdg: SDG):
    for node in sdg.iter_nodes():
        if node.kind == PlaceKind.function:
            calc_stat_for_function_node(node)
        elif node.kind == PlaceKind.pod:
            calc_stat_for_pod_node(node)
        elif node.kind == PlaceKind.container:
            calc_stat_for_container_node(node)

    for node in sdg.iter_nodes():
        if node.kind == PlaceKind.service:
            calc_stat_for_service_node(sdg, node)

    for edge in sdg.iter_edges():
        if edge.kind == DepKind.calls:
            calc_stat_for_calls_edge(sdg, edge)

    node_stat_names = set()
    for node in sdg.iter_nodes():
        collect_node_stat_names(node, node_stat_names)
    sdg.data["node_stat_names"] = sorted(node_stat_names)


STAT_PREFIX = ["stat.normal", "stat.anomal"]


def collect_node_stat_names(node: PlaceNode, names: set[str]) -> None:
    prefix = STAT_PREFIX[0]
    for k in node.data.keys():
        if k.startswith(prefix):
            stat_name = k[len(prefix) + 1 :]  # +1 for the dot
            names.add(stat_name)


def calc_stat_for_service_node(sdg: SDG, node: PlaceNode):
    assert node.kind == PlaceKind.service

    indicator_names = [
        "cpu",
        "mem",
        "diskio",
        "socket",
        "workload",
        "error",
        "latency-50",
        "latency-90",
    ]

    for name in indicator_names:
        indicator = node.indicators.get(name)
        if indicator is None:
            continue

        assert indicator.df.schema["value"] == pl.Float64
        assert indicator.df["value"].is_not_null().all()

        lf = indicator.df.lazy()
        normal_lf = lf.filter(pl.col("anomal") == 0)
        anomal_lf = lf.filter(pl.col("anomal") == 1)

        df_list = pl.collect_all([normal_lf, anomal_lf])

        value_list = [df["value"].to_numpy() for df in df_list]

        calc_regular_stat(node, indicator, value_list)

        node_stat = {
            "cpu": "cpu_usage",
            "mem": "memory_usage",
            "diskio": "diskio",
            "socket": "socket",
            "workload": "workload",
            "error": "error_rate",
            "latency-50": "latency_p50",
            "latency-90": "latency_p90",
        }

        for indicator_name, stat_name in node_stat.items():
            if indicator.name == indicator_name:
                mean = [indicator.data[f"{STAT_PREFIX[i]}.mean"] for i in range(2)]
                node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = mean[0]
                node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = mean[1]

    functions: list[PlaceNode] = []
    for edge in sdg.out_edges(node.id):
        if edge.kind == DepKind.includes:
            dst = sdg.get_node_by_id(edge.dst_id)
            assert dst.kind == PlaceKind.function
            functions.append(dst)

    if functions:
        error_rates: list[list[float]] = [[], []]
        for function in functions:
            values: list[float | None] = [function.data.get(f"{STAT_PREFIX[i]}.error_rate") for i in range(2)]
            if values[0] is None or values[1] is None:
                continue
            error_rates[0].append(values[0])
            error_rates[1].append(values[1])

        if error_rates[0] and error_rates[1]:
            node.data[f"{STAT_PREFIX[0]}.function_error_rate.mean"] = np.mean(error_rates[0])
            node.data[f"{STAT_PREFIX[1]}.function_error_rate.mean"] = np.mean(error_rates[1])


def calc_stat_for_pod_node(node: PlaceNode):
    assert node.kind == PlaceKind.pod

    indicator_names = [
        "k8s.pod.cpu.usage",
        "k8s.pod.memory.usage",
        "jvm.gc.duration:PS Scavenge:hist.sum",
    ]

    for name in indicator_names:
        indicator = node.indicators.get(name)
        if indicator is None:
            continue

        assert indicator.df.schema["value"] == pl.Float64
        assert indicator.df["value"].is_not_null().all()

        lf = indicator.df.lazy()
        normal_lf = lf.filter(pl.col("anomal") == 0)
        anomal_lf = lf.filter(pl.col("anomal") == 1)

        df_list = pl.collect_all([normal_lf, anomal_lf])

        value_list = [df["value"].to_numpy() for df in df_list]

        calc_regular_stat(node, indicator, value_list)

        if indicator.name == "k8s.pod.cpu.usage":
            stat_name = "cpu_usage"
            mean = [indicator.data[f"{STAT_PREFIX[i]}.mean"] for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = mean[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = mean[1]

        elif indicator.name == "k8s.pod.memory.usage":
            stat_name = "memory_usage"
            mean = [indicator.data[f"{STAT_PREFIX[i]}.mean"] for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = mean[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = mean[1]

        elif indicator.name == "jvm.gc.duration:PS Scavenge:hist.sum":
            stat_name = "jvm_gc_duration"
            mean = [indicator.data[f"{STAT_PREFIX[i]}.mean"] for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = mean[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = mean[1]


def calc_stat_for_container_node(node: PlaceNode):
    assert node.kind == PlaceKind.container

    indicator_names = [
        "container.cpu.usage:request_percentage",
        "container.memory.usage:request_percentage",
        "k8s.container.restarts",
    ]

    for name in indicator_names:
        indicator = node.indicators.get(name)
        if indicator is None:
            continue

        assert indicator.df.schema["value"] == pl.Float64
        assert indicator.df["value"].is_not_null().all()

        lf = indicator.df.lazy()
        normal_lf = lf.filter(pl.col("anomal") == 0)
        anomal_lf = lf.filter(pl.col("anomal") == 1)

        df_list = pl.collect_all([normal_lf, anomal_lf])

        value_list = [df["value"].to_numpy() for df in df_list]

        calc_regular_stat(node, indicator, value_list)

        if indicator.name == "container.cpu.usage:request_percentage":
            stat_name = "cpu_usage"
            mean = [indicator.data[f"{STAT_PREFIX[i]}.mean"] for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = mean[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = mean[1]

        elif indicator.name == "container.memory.usage:request_percentage":
            stat_name = "memory_usage"
            mean = [indicator.data[f"{STAT_PREFIX[i]}.mean"] for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = mean[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = mean[1]

        elif indicator.name == "k8s.container.restarts":
            stat_name = "restart_count"
            max_value = [indicator.data[f"{STAT_PREFIX[i]}.max"] for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = max_value[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = max_value[1]


def calc_stat_for_function_node(node: PlaceNode):
    assert node.kind == PlaceKind.function

    indicator_names = [
        "duration",
        "attr.status_code",
        "attr.http.response.status_code",
        "attr.http.request.content_length",
        "attr.http.response.content_length",
    ]

    for name in indicator_names:
        indicator = node.indicators.get(name)
        if indicator is None:
            continue

        assert indicator.df.schema["value"] == pl.Float64
        assert indicator.df["value"].is_not_null().all()

        lf = indicator.df.lazy()
        normal_lf = lf.filter(pl.col("anomal") == 0)
        anomal_lf = lf.filter(pl.col("anomal") == 1)

        df_list = pl.collect_all([normal_lf, anomal_lf])

        value_list = [df["value"].to_numpy() for df in df_list]

        calc_regular_stat(node, indicator, value_list)

        if indicator.name == "duration":
            stat_name = "qpm"
            qpm = [calc_qpm(df_list[i]) for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = qpm[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = qpm[1]

            stat_name = "latency"
            trimmed_mean_p10 = [indicator.data[f"{STAT_PREFIX[i]}.trimmed_mean.p10"] for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = trimmed_mean_p10[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = trimmed_mean_p10[1]

            stat_name = "latency_p50"
            p50 = [indicator.data[f"{STAT_PREFIX[i]}.quantile.p50"] for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = p50[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = p50[1]

            stat_name = "latency_p90"
            p90 = [indicator.data[f"{STAT_PREFIX[i]}.quantile.p90"] for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = p90[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = p90[1]

            stat_name = "traces_id_set"
            traces_id_set = [calc_traces_id_set(df_list[i]) for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = traces_id_set[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = traces_id_set[1]

        elif indicator.name == "attr.status_code":
            stat_name = "error_rate"
            error_rate = [calc_error_rate(value_list[i]) for i in range(2)]
            node.data[f"{STAT_PREFIX[0]}.{stat_name}"] = error_rate[0]
            node.data[f"{STAT_PREFIX[1]}.{stat_name}"] = error_rate[1]


def calc_regular_stat(
    node: PlaceNode,
    indicator: Indicator,
    value_list: list[np.ndarray],
):
    stat_functions = [
        ("count", calc_count),
        ("min", calc_min),
        ("max", calc_max),
        ("mean", calc_mean),
    ]

    for p in [0.05, 0.10, 0.25]:
        f = functools.partial(calc_trimmed_mean, proportiontocut=p)
        stat_functions.append((f"trimmed_mean.p{int(p * 100)}", f))

    for q in [0.50, 0.90, 0.95, 0.99]:
        f = functools.partial(calc_quantile, q=q)
        stat_functions.append((f"quantile.p{int(q * 100)}", f))

    for stat_name, stat_func in stat_functions:
        len0 = len(value_list[0])
        len1 = len(value_list[1])

        try:
            if len0 > 0:
                stat0 = stat_func(value_list[0])
            else:
                stat0 = None

            if len1 > 0:
                stat1 = stat_func(value_list[1])
            else:
                stat1 = None
        except Exception as e:
            traceback.print_exc()
            logger.error(f"Failed to calc `{stat_name}` for `{node.uniq_name}|{indicator.name}`: {type(e)}")
            raise

        indicator.data[f"{STAT_PREFIX[0]}.{stat_name}"] = stat0
        indicator.data[f"{STAT_PREFIX[1]}.{stat_name}"] = stat1


def calc_count(value: np.ndarray) -> float:
    return float(len(value))


def calc_min(value: np.ndarray) -> float:
    ans = value.min()
    assert isinstance(ans, float) and not math.isnan(ans), f"ans=`{ans}`"
    return float(ans)


def calc_max(value: np.ndarray) -> float:
    ans = value.max()
    assert isinstance(ans, float) and not math.isnan(ans), f"ans=`{ans}`"
    return float(ans)


def calc_mean(value: np.ndarray) -> float:
    ans = value.mean()
    assert isinstance(ans, float) and not math.isnan(ans), f"ans=`{ans}`"
    return float(ans)


def calc_trimmed_mean(value: np.ndarray, proportiontocut: float) -> float:
    assert 0 < proportiontocut < 1
    ans = scipy.stats.trim_mean(value, proportiontocut)
    assert isinstance(ans, float) and not math.isnan(ans), f"ans=`{ans}`"
    return float(ans)


def calc_quantile(value: np.ndarray, q: float) -> float:
    assert 0 < q < 1
    ans = np.quantile(value, q)
    assert isinstance(ans, float) and not math.isnan(ans), f"ans=`{ans}`"
    return float(ans)


def calc_qpm(duration_df: pl.DataFrame) -> float:
    if len(duration_df) == 0:
        return 0.0

    time_min = duration_df["time"].min()
    time_max = duration_df["time"].max()

    assert isinstance(time_min, datetime.datetime)
    assert isinstance(time_max, datetime.datetime)

    time_in_minutes = (time_max - time_min).total_seconds() / 60.0
    if time_in_minutes == 0:
        return len(duration_df)

    qpm = len(duration_df) / time_in_minutes
    return qpm


def calc_error_rate(status_code: np.ndarray) -> float:
    if len(status_code) == 0:
        return 0.0

    ans = np.mean(status_code == 2)
    assert isinstance(ans, float) and not math.isnan(ans), f"ans=`{ans}`"
    return float(ans)


def calc_traces_id_set(duration_df: pl.DataFrame) -> set[str]:
    return set(duration_df["trace_id"].unique())


def calc_stat_for_calls_edge(sdg: SDG, edge: DepEdge):
    assert edge.kind == DepKind.calls

    src = sdg.get_node_by_id(edge.src_id)
    dst = sdg.get_node_by_id(edge.dst_id)

    for prefix in STAT_PREFIX:
        src_traces_id_set = src.data.get(f"{prefix}.traces_id_set") or set()
        dst_traces_id_set = dst.data.get(f"{prefix}.traces_id_set") or set()

        common_traces_id_set = src_traces_id_set & dst_traces_id_set
        edge.data[f"{prefix}.traces_id_set"] = common_traces_id_set

        if src_traces_id_set:
            forward_call_prob = len(common_traces_id_set) / len(src_traces_id_set)
        else:
            forward_call_prob = 0.0

        if dst_traces_id_set:
            backward_call_prob = len(common_traces_id_set) / len(dst_traces_id_set)
        else:
            backward_call_prob = 0.0

        edge.data[f"{prefix}.forward_call_prob"] = forward_call_prob
        edge.data[f"{prefix}.backward_call_prob"] = backward_call_prob
