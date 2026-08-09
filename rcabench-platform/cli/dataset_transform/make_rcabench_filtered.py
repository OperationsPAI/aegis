#!/usr/bin/env -S uv run -s
import functools
import json
import shutil
from pathlib import Path
from typing import Any

import networkx as nx
import polars as pl

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.sources.convert import link_subset
from rcabench_platform.v3.sdk.datasets.rcabench import rcabench_fix_injection_display_config
from rcabench_platform.v3.sdk.datasets.spec import (
    build_service_graph,
    get_datapack_folder,
    get_dataset_folder,
    get_dataset_meta_file,
    get_dataset_meta_folder,
)
from rcabench_platform.v3.sdk.utils.fmap import fmap_threadpool
from rcabench_platform.v3.sdk.utils.serde import save_parquet


@app.command()
@timeit()
def run():
    lf = pl.scan_parquet(get_dataset_meta_file("rcabench", "attributes.parquet"))

    col = pl.col("files.total_size:MiB")
    lf = lf.filter(col <= 500)

    lf = lf.filter(
        pl.col("detector.conclusion.rows").is_not_null(),
        pl.col("detector.issues.rows") > 0,
    )

    col = pl.col("injection.injection_point.class_name")
    lf = lf.filter(
        col.is_null() | (col.str.ends_with("Test") | col.str.ends_with("Config")).not_(),
    )

    col = pl.col("injection.rate")
    lf = lf.filter(
        col.is_null() | (col / 8000 < 20),
    )

    lf = lf.filter(
        pl.col("injection.mem_worker").is_null()
        | (pl.col("injection.mem_worker") * pl.col("injection.memory_size") >= 500)
    )

    df = lf.collect()

    tasks = []
    for row in df.select("datapack", "injection.fault_type", "injection.display_config").iter_rows(named=True):
        tasks.append(functools.partial(_check_datapack, row))

    results = fmap_threadpool(tasks, parallel=32)
    rules_df = pl.DataFrame(results)

    save_parquet(rules_df, path=get_dataset_meta_file("rcabench", "rules_check.parquet"))

    rule_columns = [
        "rule_1_network_no_direct_calls",
        "rule_2_http_method_same",
        "rule_3_http_no_direct_calls",
        "rule_4_single_point_no_calls",
        "rule_5_duplicated_spans",
        "rule_6_large_latency_normal",
        "rule_7_absolute_abnormal",
    ]

    # Create a boolean mask for datapacks that failed any rule
    failed_any_rule = rules_df.select(pl.any_horizontal([pl.col(col) for col in rule_columns]).alias("failed_any"))[
        "failed_any"
    ]
    kickout_datapacks = rules_df.filter(failed_any_rule)["datapack"].to_list()

    df = df.filter(pl.col("datapack").is_in(kickout_datapacks).not_())

    dataset = "rcabench_filtered"

    dataset_folder = get_dataset_folder(dataset)
    shutil.rmtree(dataset_folder, ignore_errors=True)

    datapacks = df["datapack"].to_list()
    link_subset(src_dataset="rcabench", dst_dataset=dataset, datapacks=datapacks)

    df = df.with_columns(pl.lit(dataset).alias("dataset"))

    meta_folder = get_dataset_meta_folder(dataset)
    save_parquet(df, path=meta_folder / "attributes.parquet")


def _check_datapack(row: dict[str, Any]) -> dict[str, Any]:
    datapack = row["datapack"]
    assert isinstance(datapack, str) and datapack

    datapack_folder = get_datapack_folder("rcabench", datapack)

    injection_fault_type = row["injection.fault_type"]
    assert isinstance(injection_fault_type, str)

    injection_config = json.loads(row["injection.display_config"])
    assert isinstance(injection_config, dict)

    rcabench_fix_injection_display_config(injection_config)

    injection_point = injection_config.get("injection_point")

    # Initialize result with all rules as False
    result = {
        "datapack": datapack,
        "rule_1_network_no_direct_calls": False,
        "rule_2_http_method_same": False,
        "rule_3_http_no_direct_calls": False,
        "rule_4_single_point_no_calls": False,
        "rule_5_duplicated_spans": False,
        "rule_6_large_latency_normal": False,
        "rule_7_absolute_abnormal": False,
    }

    if not injection_point:
        return result
    assert isinstance(injection_point, dict)

    # Rule 1: Network fault types - no direct calls
    if injection_fault_type.startswith("Network"):
        direction = injection_point.get("direction")
        if direction is None:
            direction = injection_config.get("direction")
        assert direction in ["from", "to", "both"], injection_config

        source_service = injection_point.get("source_service")
        target_service = injection_point.get("target_service")
        if source_service and target_service:
            assert isinstance(source_service, str)
            assert isinstance(target_service, str)

            has_direct_calls = False

            if direction in ("from", "both"):
                has_direct_calls |= scan_direct_calls_from_traces(
                    datapack_folder,
                    source_service,
                    target_service,
                )

            if direction in ("to", "both"):
                has_direct_calls |= scan_direct_calls_from_traces(
                    datapack_folder,
                    target_service,
                    source_service,
                )

            if not has_direct_calls:
                logger.debug(
                    f"datapack `{datapack}`: no direct calls between \
                        `{source_service}` and `{target_service}`, direction `{direction}`"
                )
                result["rule_1_network_no_direct_calls"] = True

    # Rule 2: HTTPRequestReplaceMethod - same method
    if injection_fault_type == "HTTPRequestReplaceMethod":
        method = injection_point.get("method")
        replace_method = injection_config.get("replace_method")
        if method and replace_method:
            assert isinstance(method, str) and method
            assert isinstance(replace_method, str) and replace_method
            if method == replace_method:
                result["rule_2_http_method_same"] = True

    # Rule 3: HTTP fault types - no direct calls
    if injection_fault_type.startswith("HTTP"):
        app_name = injection_point.get("app_name")
        server_address = injection_point.get("server_address")
        if app_name and server_address:
            assert isinstance(app_name, str) and app_name
            assert isinstance(server_address, str) and server_address

            has_direct_calls = scan_direct_calls_from_traces(
                datapack_folder,
                source_service=app_name,
                target_service=server_address,
            )

            if not has_direct_calls:
                logger.debug(f"datapack `{datapack}`: no direct calls between `{app_name}` and `{server_address}`")
                result["rule_3_http_no_direct_calls"] = True

    # Rule 4: Single point failure - no calls to target
    if is_single_point_failure(injection_fault_type):
        target_service = injection_point.get("app_label")
        if target_service is None:
            target_service = injection_point.get("app_name")
        if target_service:
            assert isinstance(target_service, str) and target_service

            has_direct_calls = scan_direct_calls_from_traces(
                datapack_folder,
                None,
                target_service,
            )

            if not has_direct_calls:
                logger.debug(f"datapack `{datapack}`: no direct calls to `{target_service}`")
                result["rule_4_single_point_no_calls"] = True

    # Rule 5: Duplicated spans
    if scan_duplicated_spans(datapack_folder):
        logger.debug(f"datapack `{datapack}`: has duplicated spans")
        result["rule_5_duplicated_spans"] = True

    # Rule 6: Large latency in normal range
    if scan_large_latency_in_normal_range(datapack_folder):
        logger.debug(f"datapack `{datapack}`: large_latency_in_normal_range")
        result["rule_6_large_latency_normal"] = True

    return result


def is_single_point_failure(fault_type: str) -> bool:
    return True
    # return fault_type.startswith("JVM") or fault_type in ("PodFailure", "CPUStress", "MemoryStress")


@timeit()
def scan_path_from_traces(
    datapack_folder: Path,
    source_service: str | None,
    target_service: str,
) -> bool:
    assert datapack_folder.exists()

    normal_traces = pl.scan_parquet(datapack_folder / "normal_traces.parquet")
    anomal_traces = pl.scan_parquet(datapack_folder / "abnormal_traces.parquet")
    traces = pl.concat([normal_traces, anomal_traces])

    service_graph = build_service_graph(traces)
    if source_service is None:
        return target_service in service_graph
    if source_service == target_service:
        return True
    if source_service not in service_graph or target_service not in service_graph:
        return False
    return nx.has_path(service_graph, source_service, target_service)


@timeit()
def scan_direct_calls_from_traces(
    datapack_folder: Path,
    source_service: str | None,
    target_service: str,
) -> bool:
    assert datapack_folder.exists()

    normal_traces = pl.scan_parquet(datapack_folder / "normal_traces.parquet")
    anomal_traces = pl.scan_parquet(datapack_folder / "abnormal_traces.parquet")
    traces = pl.concat([normal_traces, anomal_traces])

    lf = traces.select(
        "span_id",
        "parent_span_id",
        "service_name",
    )

    lf = lf.join(
        lf.select("span_id", pl.col("service_name").alias("parent_service_name")),
        left_on="parent_span_id",
        right_on="span_id",
        how="left",
    )

    if source_service:
        lf = lf.filter(
            pl.col("parent_service_name") == source_service,
            pl.col("service_name") == target_service,
        )
    else:
        lf = lf.filter(
            pl.col("service_name") == target_service,
        )

    df = lf.collect()

    logger.debug(f"source=`{source_service}`, target=`{target_service}`, len(df)={len(df)}")

    return len(df) > 0


@timeit()
def scan_duplicated_spans(datapack_folder: Path) -> bool:
    for file in ("normal_traces.parquet", "abnormal_traces.parquet"):
        lf = pl.scan_parquet(datapack_folder / file)
        lf = lf.select("span_id", "span_name", "parent_span_id")
        df = lf.collect()

        total_count = df.n_unique()
        id_count = df.select("span_id").n_unique()

        if len(df) != total_count or total_count != id_count:
            return True

    return False


@timeit()
def scan_large_latency_in_normal_range(datapack_folder: Path) -> bool:
    lf = pl.scan_parquet(datapack_folder / "normal_traces.parquet")
    lf = lf.select(pl.col("duration").quantile(0.99))
    p99_duration = lf.collect().item()
    if p99_duration is None:
        return False
    return p99_duration > 6 * 1e9


if __name__ == "__main__":
    app()
