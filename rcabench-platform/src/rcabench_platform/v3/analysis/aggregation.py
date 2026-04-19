import duckdb
import numpy as np
import polars as pl

from ..sdk.logging import logger
from ..sdk.utils.serde import save_parquet
from .data_prepare import Item

FAULT_TYPE_MAPPING = {
    # Pod/container-level faults
    "PodKill": "Pod",
    "PodFailure": "Pod",
    "ContainerKill": "Pod",
    # resource stress
    "MemoryStress": "Resource",
    "CPUStress": "Resource",
    "JVMCPUStress": "Resource",
    "JVMMemoryStress": "Resource",
    # HTTP faults
    "HTTPRequestAbort": "HTTP",
    "HTTPResponseAbort": "HTTP",
    "HTTPRequestDelay": "HTTP",
    "HTTPResponseDelay": "HTTP",
    "HTTPResponseReplaceBody": "HTTP",
    "HTTPResponsePatchBody": "HTTP",
    "HTTPRequestReplacePath": "HTTP",
    "HTTPRequestReplaceMethod": "HTTP",
    "HTTPResponseReplaceCode": "HTTP",
    # DNS
    "DNSError": "DNS",
    "DNSRandom": "DNS",
    # time
    "TimeSkew": "Time",
    # network faults
    "NetworkDelay": "Network",
    "NetworkLoss": "Network",
    "NetworkDuplicate": "Network",
    "NetworkCorrupt": "Network",
    "NetworkBandwidth": "Network",
    "NetworkPartition": "Network",
    # JVM application-level
    "JVMLatency": "JVM",
    "JVMReturn": "JVM",
    "JVMException": "JVM",
    "JVMGarbageCollector": "JVM",
    "JVMMySQLLatency": "JVM",
    "JVMMySQLException": "JVM",
}


def aggregate(items: list[Item]) -> pl.DataFrame:
    if not items:
        return pl.DataFrame()

    data_rows = []

    for item in items:
        row = {
            "injection_id": item._injection.id,
            "injection_name": item._injection.name,
            "fault_type": item.fault_type,
            "fault_category": FAULT_TYPE_MAPPING.get(item.fault_type, "Unknown"),
            "injected_service": item.injected_service,
            "is_pair": item.is_pair,
            "anomaly_degree": item.anomaly_degree,
            "workload": item.workload,
            # Data statistics
            "trace_count": item.trace_count,
            "duration_seconds": item.duration.total_seconds(),
            "qps": item.qps,
            "qpm": item.qpm,
            "service_count": len(item.service_names),
            "service_count_by_trace": len(item.service_names_by_trace),
            "service_coverage": item.service_coverage,
            # Log statistics
            "total_log_lines": sum(item.log_lines.values()),
            "log_services_count": len(item.log_lines),
            # Metric statistics
            "total_metric_count": sum(item.injection_metric_counts.values()),
            "unique_metrics": len(item.injection_metric_counts),
            # Trace depth statistics
            "avg_trace_length": (
                sum(length * count for length, count in item.trace_length.items()) / sum(item.trace_length.values())
                if item.trace_length
                else 0
            ),
            "max_trace_length": max(item.trace_length.keys()) if item.trace_length else 0,
            "max_service_length": max(item.service_length.keys()) if item.service_length else 0,
            "min_trace_length": min(item.trace_length.keys()) if item.trace_length else 0,
            "min_service_length": min(item.service_length.keys()) if item.service_length else 0,
            "SDD@1": item.datapack_metric_values.get("SDD@1", np.nan),
            "SDD@3": item.datapack_metric_values.get("SDD@3", np.nan),
            "SDD@5": item.datapack_metric_values.get("SDD@5", np.nan),
            "CPL": item.datapack_metric_values.get("CPL", np.nan),
            "RootServiceDegree": item.datapack_metric_values.get("RootServiceDegree", np.nan),
        }

        for metric_name, metric_value in item.datapack_metric_values.items():
            row[f"datapack_metric_{metric_name}"] = metric_value

        for algo_name, metric in item.algo_metrics.items():
            row[f"algo_{algo_name}"] = metric.to_dict()

        data_rows.append(row)

    df = pl.DataFrame(data_rows)

    # Flatten algo columns
    algo_cols = [col for col in df.columns if col.startswith("algo_")]

    if not algo_cols:
        return df

    expr_list = []

    for col in df.columns:
        if not col.startswith("algo_"):
            expr_list.append(pl.col(col))

    algo_fields = [
        "top1",
        "top3",
        "top5",
        "avg3",
        "avg5",
        "mrr",
        "time",
    ]
    for algo_col in algo_cols:
        for field_name in algo_fields:
            new_col_name = f"{algo_col}_{field_name}"
            expr_list.append(
                pl.col(algo_col)
                .map_elements(
                    lambda x, field=field_name: x.get(field, 0.0) if isinstance(x, dict) else 0.0,
                    return_dtype=pl.Float64,
                )
                .alias(new_col_name)
            )

    try:
        flattened_df = df.select(expr_list)
        return flattened_df
    except Exception as e:
        logger.error(f"Warning: Failed to flatten algo columns, excluding them: {e}")
        non_algo_cols = [col for col in df.columns if not col.startswith("algo_")]
        return df.select(non_algo_cols)


class DuckDBAggregator:
    def __init__(self, df: pl.DataFrame):
        self.conn = duckdb.connect(":memory:")
        save_parquet(df, path="temp/algo/raw.parquet")
        self.conn.register("data", df.to_arrow())

    def print_schema(self) -> None:
        try:
            schema_result = self.conn.execute("DESCRIBE data").fetchdf()
            print("Data Table Schema:")
            print("=" * 60)
            print(f"{'Column Name':<30} {'Type':<15} {'Null':<10}")
            print("-" * 60)

            for _, row in schema_result.iterrows():
                column_name = row["column_name"]
                column_type = row["column_type"]
                null_allowed = row["null"]
                print(f"{column_name:<30} {column_type:<15} {null_allowed:<10}")

            print("-" * 60)
            print(f"Total {len(schema_result)} columns")
            print("=" * 60)

        except Exception as e:
            logger.error(f"Failed to get schema information: {e}")

    def _custom_sql(self, sql_query: str) -> pl.DataFrame:
        if not sql_query or not sql_query.strip():
            raise ValueError("SQL query cannot be empty")

        try:
            # query_preview = sql_query.strip()[:200] + ("..." if len(sql_query.strip()) > 200 else "")
            # logger.debug(f"Executing SQL query: {query_preview}")

            result_arrow = self.conn.execute(sql_query).arrow()
            result_df = pl.from_arrow(result_arrow)

            if not isinstance(result_df, pl.DataFrame):
                raise TypeError(f"Expected DataFrame, got {type(result_df)}")

            # logger.debug(f"Query returned {result_df.height} rows, {result_df.width} columns")
            return result_df

        except Exception as e:
            logger.error(f"SQL execution failed: {e}")
            logger.debug(f"Failed query: {sql_query}")
            raise

    def _group_by_analysis(self, group_column_sql: str, group_column_name: str) -> pl.DataFrame:
        return self._multi_group_by_analysis([group_column_sql], [group_column_name])

    def _multi_group_by_analysis(self, group_columns_sql: list[str], group_column_names: list[str]) -> pl.DataFrame:
        if len(group_columns_sql) != len(group_column_names):
            raise ValueError("group_columns_sql and group_column_names must have the same length")

        algo_columns = self._get_algo_columns()

        # Build algorithm aggregations with validation
        algo_aggregations = self._build_algo_aggregations(algo_columns)

        # Build select clause with proper escaping
        select_parts = []
        for sql_expr, col_name in zip(group_columns_sql, group_column_names):
            # Validate column name to prevent injection
            if not col_name.replace("_", "").replace("@", "").isalnum():
                raise ValueError(f"Invalid column name: {col_name}")
            select_parts.append(f"({sql_expr}) AS {col_name}")

        select_clause = ",\n            ".join(select_parts)
        group_clause = ",\n            ".join(group_column_names)

        # Build the complete query using parts
        query_parts = ["SELECT", f"    {select_clause},", "    COUNT(*) AS count"]

        if algo_aggregations:
            query_parts.append(f"    {','.join(algo_aggregations)}")

        query_parts.extend(["FROM data", f"GROUP BY {group_clause}", "ORDER BY count DESC"])

        # Construct final SQL query
        sql_parts = []
        sql_parts.extend(query_parts[:3])  # SELECT clause parts

        if algo_aggregations:
            algo_clause = ",\n    " + ",\n    ".join(algo_aggregations)
            sql_parts.append(algo_clause)
            remaining_parts = query_parts[4:]
        else:
            remaining_parts = query_parts[3:]

        sql_parts.extend(["\n" + part for part in remaining_parts])
        sql = "\n".join(sql_parts)

        try:
            raw_result = self._custom_sql(sql)
            return self._post_process_multi_analysis_results(raw_result, group_column_names)
        except Exception as e:
            logger.error(f"Failed to execute group by analysis query: {e}")
            logger.debug(f"Query was: {sql}")
            return pl.DataFrame()

    def _build_algo_aggregations(self, algo_columns: list[str]) -> list[str]:
        """Build algorithm aggregation clauses with validation."""
        algo_aggregations = []

        # Define valid metric suffixes
        valid_suffixes = ("_top1", "_top3", "_top5", "_avg3", "_avg5", "_mrr", "_time")

        for col in algo_columns:
            if not col.endswith(valid_suffixes):
                continue

            # Validate column name format
            if not col.startswith("algo_"):
                continue

            col_parts = col.replace("algo_", "").rsplit("_", 1)
            if len(col_parts) != 2:
                continue

            algo_name, metric_type = col_parts

            # Validate names contain only safe characters
            if not algo_name.replace("_", "").isalnum() or not metric_type.replace("_", "").isalnum():
                continue

            alias = f"avg_{algo_name}_{metric_type}"
            algo_aggregations.append(f"AVG({col}) AS {alias}")

        return algo_aggregations

    def _post_process_multi_analysis_results(
        self, raw_result: pl.DataFrame, group_column_names: list[str]
    ) -> pl.DataFrame:
        if raw_result.height == 0:
            return pl.DataFrame()

        algo_cols = [col for col in raw_result.columns if col.startswith("avg_")]

        if not algo_cols:
            return raw_result

        algorithms = set()
        metrics = set()

        for col in algo_cols:
            parts = col.replace("avg_", "").rsplit("_", 1)
            if len(parts) == 2:
                algo_name, metric_type = parts
                algorithms.add(algo_name)
                metrics.add(metric_type)

        algorithms = sorted(list(algorithms))
        metrics = sorted(list(metrics))

        result_rows = []

        for row in raw_result.iter_rows(named=True):
            count = row["count"]

            for algo in algorithms:
                algo_row = {"count": count, "algorithm": algo}

                for group_col in group_column_names:
                    algo_row[group_col] = row[group_col]

                for metric in metrics:
                    col_name = f"avg_{algo}_{metric}"
                    value = row.get(col_name, None)
                    algo_row[metric] = value

                result_rows.append(algo_row)

        if result_rows:
            result_df = pl.DataFrame(result_rows)

            sort_columns = group_column_names + ["algorithm"]
            result_df = result_df.sort(sort_columns)
            return result_df
        else:
            return pl.DataFrame()

    def _build_sdd_conditions(self, k: int) -> str:
        """Build SDD condition SQL based on k value."""
        # Validate required columns exist
        required_columns = []
        if k >= 1:
            required_columns.append("datapack_metric_SDD@1")
        if k >= 3:
            required_columns.append("datapack_metric_SDD@3")
        if k >= 5:
            required_columns.append("datapack_metric_SDD@5")

        for col in required_columns:
            try:
                test_query = f'SELECT "{col}" FROM data LIMIT 1'
                self._custom_sql(test_query)
            except Exception:
                logger.error(f"Required column {col} not found")
                raise ValueError(f"Required column {col} not found in data")

        if k == 1:
            return """
            CASE
                WHEN "datapack_metric_SDD@1" = 0 THEN 'SDD@1 = 0'
                ELSE 'SDD@1 > 0'
            END
            """
        elif k == 3:
            return """
            CASE
                WHEN "datapack_metric_SDD@3" = 0 AND "datapack_metric_SDD@1" > 0
                    THEN 'SDD@3 = 0 (SDD@1 > 0)'
                WHEN "datapack_metric_SDD@3" > 0 THEN 'SDD@3 > 0'
                WHEN "datapack_metric_SDD@1" = 0 THEN 'SDD@1 = 0'
                ELSE 'Other'
            END
            """
        elif k == 5:
            return """
            CASE
                WHEN "datapack_metric_SDD@5" = 0
                     AND "datapack_metric_SDD@3" > 0
                     AND "datapack_metric_SDD@1" > 0
                    THEN 'SDD@5 = 0 (SDD@1,3 > 0)'
                WHEN "datapack_metric_SDD@5" > 0 THEN 'SDD@5 > 0'
                WHEN "datapack_metric_SDD@3" = 0 AND "datapack_metric_SDD@1" > 0
                    THEN 'SDD@3 = 0 (SDD@1 > 0)'
                WHEN "datapack_metric_SDD@1" = 0 THEN 'SDD@1 = 0'
                ELSE 'Other'
            END
            """
        else:
            raise ValueError("k must be 1, 3, or 5")

    def _get_algo_columns(self) -> list[str]:
        try:
            columns_result = self.conn.execute("PRAGMA table_info('data')").fetchdf()
            algo_columns = [
                str(row["name"]) for _, row in columns_result.iterrows() if str(row["name"]).startswith("algo_")
            ]
            return algo_columns
        except Exception as e:
            logger.error(f"Failed to get algorithm columns: {e}")
            return []

    def _extract_algorithm_names(self, algo_columns: list[str]) -> list[str]:
        """Extract unique algorithm names from column names."""
        algorithms = set()
        for col in algo_columns:
            if col.startswith("algo_") and "_" in col:
                # Validate column name format
                col_without_prefix = col.replace("algo_", "")
                if col_without_prefix.count("_") >= 1:
                    algo_name = col_without_prefix.rsplit("_", 1)[0]
                    # Validate algorithm name contains only safe characters
                    if algo_name.replace("_", "").isalnum():
                        algorithms.add(algo_name)

        return sorted(list(algorithms))

    def _build_performance_select_statements(self, algorithms: list[str], algo_columns: list[str]) -> list[str]:
        """Build SELECT statements for performance metrics."""
        select_statements = []

        # Define metric types to include
        metric_types = ["top1", "top3", "top5", "avg3", "avg5", "mrr", "time"]

        for algo in algorithms:
            for metric_type in metric_types:
                col_name = f"algo_{algo}_{metric_type}"

                if col_name in algo_columns:
                    alias = f"{algo}_{metric_type}_avg"
                    select_statements.append(f"AVG({col_name}) AS {alias}")

        return select_statements

    def _process_overall_performance_results(self, raw_result: pl.DataFrame, algorithms: list[str]) -> pl.DataFrame:
        """Process raw query results into the final format."""
        result_rows = []

        if raw_result.height == 0:
            return pl.DataFrame()

        row_data = raw_result.row(0, named=True)
        total_count = row_data.get("total_count", 0)

        for algo in algorithms:
            algo_row = {
                "algorithm": algo,
                "count": total_count,
                "top1": row_data.get(f"{algo}_top1_avg"),
                "top3": row_data.get(f"{algo}_top3_avg"),
                "top5": row_data.get(f"{algo}_top5_avg"),
                "avg3": row_data.get(f"{algo}_avg3_avg"),
                "avg5": row_data.get(f"{algo}_avg5_avg"),
                "mrr": row_data.get(f"{algo}_mrr_avg"),
                "avg_time": row_data.get(f"{algo}_time_avg"),
            }
            result_rows.append(algo_row)

        if result_rows:
            result_df = pl.DataFrame(result_rows)
            return result_df.sort("algorithm")
        else:
            return pl.DataFrame()

    def _algo_performance_div(self, algorithm_name: str) -> pl.DataFrame:
        """Divide algorithm performance into categories."""
        if not algorithm_name or not algorithm_name.replace("_", "").isalnum():
            raise ValueError(f"Invalid algorithm name: {algorithm_name}")

        algo_columns = self._get_algo_columns()

        # Define required columns for this algorithm
        required_cols = [
            f"algo_{algorithm_name}_top1",
            f"algo_{algorithm_name}_top3",
            f"algo_{algorithm_name}_top5",
        ]

        # Validate all required columns exist
        missing_cols = [col for col in required_cols if col not in algo_columns]
        if missing_cols:
            logger.warning(f"Algorithm {algorithm_name} missing columns: {missing_cols}")
            return pl.DataFrame()

        top1_col, top3_col, top5_col = required_cols

        query = f"""
        SELECT
            *,
            CASE
                WHEN {top1_col} = 1 THEN 'top1_success'
                WHEN {top3_col} = 1 THEN 'top3_success'
                WHEN {top5_col} = 1 THEN 'top5_success'
                ELSE 'complete_failure'
            END AS performance_category
        FROM data
        WHERE {top1_col} IS NOT NULL
            AND {top3_col} IS NOT NULL
            AND {top5_col} IS NOT NULL
        """

        try:
            return self._custom_sql(query)
        except Exception as e:
            logger.error(f"Failed to execute algorithm performance division for {algorithm_name}: {e}")
            logger.debug(f"Query was: {query}")
            return pl.DataFrame()

    def _build_algo_analysis_query(self, performance_filter) -> str:
        agg_metrics = [
            'AVG("SDD@1") AS avg_sdd1',
            'AVG("SDD@3") AS avg_sdd3',
            'AVG("SDD@5") AS avg_sdd5',
            "AVG(CPL) AS avg_cpl",
            "AVG(RootServiceDegree) AS avg_root_service_degree",
            "AVG(trace_count) AS avg_trace_count",
            "AVG(duration_seconds) AS avg_duration",
            "AVG(qps) AS avg_qps",
            "AVG(service_count) AS avg_service_count",
            "AVG(avg_trace_length) AS avg_trace_length",
            "AVG(max_trace_length) AS avg_max_trace_length",
        ]

        # Build WHERE clause based on filter type
        if isinstance(performance_filter, str):
            where_clause = f"performance_category = '{performance_filter}'"
        elif isinstance(performance_filter, list):
            escaped_categories = [f"'{cat}'" for cat in performance_filter]
            where_clause = f"performance_category IN ({', '.join(escaped_categories)})"
        else:
            raise ValueError("performance_filter must be string or list of strings")

        query = f"""
        SELECT
            performance_category,
            fault_category,
            fault_type,
            COUNT(*) AS count,
            {", ".join(agg_metrics)}
        FROM breakdown_data
        WHERE {where_clause}
        GROUP BY performance_category, fault_category, fault_type
        ORDER BY performance_category, count DESC
        """

        return query

    def close(self):
        self.conn.close()

    def perf_group_by_fault_category(self) -> pl.DataFrame:
        return self._group_by_analysis("fault_category", "fault_category")

    def perf_group_by_fault_type(self) -> pl.DataFrame:
        return self._group_by_analysis("fault_type", "fault_type")

    def perf_common_failures(self, k: int = 1, min_algorithms: int = 3) -> pl.DataFrame:
        if k not in [1, 3, 5]:
            raise ValueError("k must be 1, 3, or 5")

        algo_columns = self._get_algo_columns()
        topk_columns = [col for col in algo_columns if col.endswith(f"_top{k}")]

        if not topk_columns or len(topk_columns) < min_algorithms:
            logger.warning(f"Insufficient algorithms for analysis: found {len(topk_columns)}, need {min_algorithms}")
            return pl.DataFrame()

        # Build per-column failure indicators (0/1), treating NULL as failure as well
        failure_terms: list[str] = []
        for col in topk_columns:
            # Validate column name contains only safe characters
            if not col.replace("_", "").replace("@", "").isalnum():
                logger.warning(f"Skipping invalid column name: {col}")
                continue
            # CASE WHEN col = 0 OR col IS NULL THEN 1 ELSE 0 END
            failure_terms.append(f"(CASE WHEN {col} = 0 OR {col} IS NULL THEN 1 ELSE 0 END)")

        if not failure_terms:
            return pl.DataFrame()

        failure_sum_expr = " + ".join(failure_terms)

        query = f"""
        SELECT
            *,
            ({failure_sum_expr}) AS failure_count,
            {len(topk_columns)} AS algorithms_considered
        FROM data
        WHERE ({failure_sum_expr}) >= {min_algorithms}
        ORDER BY fault_category, fault_type
        """

        try:
            return self._custom_sql(query)
        except Exception as e:
            logger.error(f"Failed to execute common failed cases analysis: {e}")
            logger.debug(f"Query was: {query}")
            return pl.DataFrame()

    def perf_sdd_k(self, k: int) -> pl.DataFrame:
        """Analyze SDD@k distribution."""
        if k not in [1, 3, 5]:
            raise ValueError("k must be 1, 3, or 5")

        # Validate that the required column exists
        sdd_column = f"SDD@{k}"
        try:
            # Check if column exists by trying to select it
            test_query = f'SELECT "{sdd_column}" FROM data LIMIT 1'
            self._custom_sql(test_query)
        except Exception:
            logger.error(f"Column {sdd_column} not found in data")
            return pl.DataFrame()

        group_sql = f"""
        CASE
            WHEN "{sdd_column}" = 0 THEN 'SDD@{k} = 0'
            ELSE 'SDD@{k} > 0'
        END
        """

        return self._group_by_analysis(group_sql, "sdd_category")

    def perf_sddk_and_fault_category(self, k: int) -> pl.DataFrame:
        """Analyze fault categories combined with SDD@k analysis."""
        if k not in [1, 3, 5]:
            raise ValueError("k must be 1, 3, or 5")

        # Define SDD conditions based on k value
        sdd_conditions = self._build_sdd_conditions(k)

        group_sql_list = [
            "fault_category",
            sdd_conditions,
        ]
        group_names = ["fault_category", f"sdd_k{k}_category"]
        return self._multi_group_by_analysis(group_sql_list, group_names)

    def perf_algo_failures(self, algorithm_name: str) -> pl.DataFrame:
        """Analyze algorithm failure patterns."""
        breakdown_df = self._algo_performance_div(algorithm_name)

        if breakdown_df.height == 0:
            logger.warning(f"No breakdown data available for algorithm: {algorithm_name}")
            return pl.DataFrame()

        try:
            self.conn.register("breakdown_data", breakdown_df.to_arrow())

            query = self._build_algo_analysis_query("complete_failure")
            return self._custom_sql(query)

        except Exception as e:
            logger.error(f"Failed to analyze failures for algorithm {algorithm_name}: {e}")
            return pl.DataFrame()

    def perf_algo_success(self, algorithm_name: str) -> pl.DataFrame:
        breakdown_df = self._algo_performance_div(algorithm_name)

        if breakdown_df.height == 0:
            logger.warning(f"No breakdown data available for algorithm: {algorithm_name}")
            return pl.DataFrame()

        try:
            self.conn.register("breakdown_data", breakdown_df.to_arrow())

            success_categories = ["top1_success", "top3_success", "top5_success"]
            query = self._build_algo_analysis_query(success_categories)
            return self._custom_sql(query)

        except Exception as e:
            logger.error(f"Failed to analyze successes for algorithm {algorithm_name}: {e}")
            return pl.DataFrame()

    def perf_overall(self) -> pl.DataFrame:
        algo_columns = self._get_algo_columns()

        if not algo_columns:
            logger.warning("No algorithm columns found")
            return pl.DataFrame()

        # Extract algorithm names
        algorithms = self._extract_algorithm_names(algo_columns)

        if not algorithms:
            logger.warning("No valid algorithm names found")
            return pl.DataFrame()

        # Build select statements for performance metrics
        select_statements = self._build_performance_select_statements(algorithms, algo_columns)

        if not select_statements:
            logger.warning("No valid performance metrics found")
            return pl.DataFrame()

        select_clause = ",\n            ".join(select_statements)

        query = f"""
        SELECT
            COUNT(*) as total_count,
            {select_clause}
        FROM data
        """

        try:
            raw_result = self._custom_sql(query)

            if raw_result.height == 0:
                return pl.DataFrame()

            return self._process_overall_performance_results(raw_result, algorithms)

        except Exception as e:
            logger.error(f"Failed to execute overall performance query: {e}")
            logger.debug(f"Query was: {query}")
            return pl.DataFrame()

    def dataset_fault_type(self) -> pl.DataFrame:
        query = """
        SELECT
            fault_type,
            fault_category,
            SUM(CASE WHEN anomaly_degree = 'no' THEN 1 ELSE 0 END) AS no,
            SUM(CASE WHEN anomaly_degree = 'absolute' THEN 1 ELSE 0 END) AS absolute,
            SUM(CASE WHEN anomaly_degree = 'may' THEN 1 ELSE 0 END) AS may,
            COUNT(*) AS total_count
        FROM data
        GROUP BY fault_type, fault_category
        ORDER BY total_count DESC
        """

        try:
            return self._custom_sql(query)
        except Exception as e:
            logger.error(f"Failed to execute dataset fault type analysis: {e}")
            logger.debug(f"Query was: {query}")
            return pl.DataFrame()

    def dataset_overall(self) -> pl.DataFrame:
        """
        Generate dataset basic information statistics

        Returns:
            pl.DataFrame: DataFrame containing dataset basic statistics
        """
        query = """
        SELECT
            COUNT(*) AS total_datapacks,
            AVG(service_count) AS service_count,
            MAX(service_count_by_trace) AS max_services_by_trace_per_datapack,
            MIN(service_count_by_trace) AS min_services_by_trace_per_datapack,
            AVG(service_count_by_trace) AS avg_services_by_trace_per_datapack,
            MAX(max_trace_length) AS longest_trace_length,
            MAX(max_service_length) AS longest_service_length,
            SUM(total_log_lines) AS total_log_lines,
            AVG(total_metric_count) AS avg_metric_samples_per_datapack,
            AVG(unique_metrics) AS avg_unique_metrics_per_datapack,
            SUM(trace_count) AS total_traces,
            SUM(duration_seconds) AS total_duration_seconds,
            SUM(duration_seconds) / 3600.0 AS total_duration_hours,
        FROM data
        """

        try:
            result = self._custom_sql(query)

            # Add calculated fields and formatting
            if result.height > 0:
                # Convert to more readable format
                processed_result = result.with_columns(
                    [
                        (pl.col("total_duration_hours").round(2)).alias("total_duration_hours"),
                        (pl.col("total_traces") / pl.col("total_duration_seconds")).round(2).alias("traces_per_second"),
                    ]
                )

                # Reorder columns for better logical organization
                column_order = [
                    "total_datapacks",
                    "service_count",
                    "max_services_by_trace_per_datapack",
                    "min_services_by_trace_per_datapack",
                    "avg_services_by_trace_per_datapack",
                    "longest_trace_length",
                    "longest_service_length",
                    "total_log_lines",
                    "avg_metric_samples_per_datapack",
                    "avg_unique_metrics_per_datapack",
                    "total_traces",
                    "total_duration_seconds",
                    "total_duration_hours",
                ]

                # Select existing columns only
                available_columns = [col for col in column_order if col in processed_result.columns]
                processed_result = processed_result.select(available_columns)

                return processed_result
            else:
                return pl.DataFrame()

        except Exception as e:
            logger.error(f"Failed to execute dataset overall analysis: {e}")
            logger.debug(f"Query was: {query}")
            return pl.DataFrame()
