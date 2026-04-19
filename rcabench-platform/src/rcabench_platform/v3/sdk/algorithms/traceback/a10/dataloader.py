from pathlib import Path

import duckdb

from ....logging import logger
from ....utils.env import debug
from .model import SDG, SpanEdge

# ============================================================================
# Module 1: Data Loading
# ============================================================================


class DataLoader:
    def __init__(self, input_folder: Path, con: duckdb.DuckDBPyConnection):
        self.input_folder = input_folder
        self.con = con

    def load_all(self):
        self._load_traces()
        self._load_conclusion()
        self._load_metrics()
        self._load_metrics_hist()
        self._load_metrics_sum()
        self._load_logs()

        if debug():
            # Print table stats
            tables_to_check = [
                "traces_good",
                "traces_bad",
                "conclusion",
                "metrics_good",
                "metrics_bad",
                "metrics_hist_good",
                "metrics_hist_bad",
                "metrics_sum_good",
                "metrics_sum_bad",
                "logs_good",
                "logs_bad",
            ]
            for table in tables_to_check:
                try:
                    result = self.con.execute(f"SELECT COUNT(*) FROM {table}").fetchone()
                    if result:
                        logger.debug(f"Loaded {result[0]} rows into {table}")
                except Exception:
                    pass

    def _load_traces(self):
        normal_traces = self.input_folder / "normal_traces.parquet"
        assert normal_traces.exists()
        self.con.execute(
            f"""
            CREATE TABLE traces_good AS
            SELECT * FROM read_parquet('{normal_traces}')
        """
        )

        abnormal_traces = self.input_folder / "abnormal_traces.parquet"
        assert abnormal_traces.exists()
        self.con.execute(
            f"""
            CREATE TABLE traces_bad AS
            SELECT * FROM read_parquet('{abnormal_traces}')
        """
        )

    def _load_conclusion(self):
        conclusion_file = self.input_folder / "conclusion.parquet"
        assert conclusion_file.exists()
        self.con.execute(
            f"""
            CREATE TABLE conclusion AS
            SELECT * FROM read_parquet('{conclusion_file}')
        """
        )

    def _load_metrics(self):
        normal_metrics = self.input_folder / "normal_metrics.parquet"
        assert normal_metrics.exists()
        self.con.execute(
            f"""
            CREATE TABLE metrics_good AS
            SELECT * FROM read_parquet('{normal_metrics}')
        """
        )

        abnormal_metrics = self.input_folder / "abnormal_metrics.parquet"
        assert abnormal_metrics.exists()
        self.con.execute(
            f"""
            CREATE TABLE metrics_bad AS
            SELECT * FROM read_parquet('{abnormal_metrics}')
        """
        )

    def _load_metrics_hist(self):
        normal_metrics = self.input_folder / "normal_metrics_histogram.parquet"
        assert normal_metrics.exists()
        self.con.execute(
            f"""
            CREATE TABLE metrics_hist_good AS
            SELECT * FROM read_parquet('{normal_metrics}')
        """
        )

        abnormal_metrics = self.input_folder / "abnormal_metrics_histogram.parquet"
        assert abnormal_metrics.exists()
        self.con.execute(
            f"""
            CREATE TABLE metrics_hist_bad AS
            SELECT * FROM read_parquet('{abnormal_metrics}')
        """
        )

    def _load_metrics_sum(self):
        normal_metrics = self.input_folder / "normal_metrics_sum.parquet"
        assert normal_metrics.exists()
        self.con.execute(
            f"""
            CREATE TABLE metrics_sum_good AS
            SELECT * FROM read_parquet('{normal_metrics}')
        """
        )

        abnormal_metrics = self.input_folder / "abnormal_metrics_sum.parquet"
        assert abnormal_metrics.exists()
        self.con.execute(
            f"""
            CREATE TABLE metrics_sum_bad AS
            SELECT * FROM read_parquet('{abnormal_metrics}')
        """
        )

    def _load_logs(self):
        normal_logs = self.input_folder / "normal_logs.parquet"
        assert normal_logs.exists()
        self.con.execute(
            f"""
            CREATE TABLE logs_good AS
            SELECT * FROM read_parquet('{normal_logs}')
        """
        )

        abnormal_logs = self.input_folder / "abnormal_logs.parquet"
        assert abnormal_logs.exists()
        self.con.execute(
            f"""
        CREATE TABLE logs_bad AS
            SELECT * FROM read_parquet('{abnormal_logs}')
        """
        )


# ============================================================================
# Module 2: Service Dependency Graph Construction
# ============================================================================


class SDGBuilder:
    def __init__(self, con: duckdb.DuckDBPyConnection):
        self.con = con

    def build(self) -> SDG:
        query = """
        WITH all_traces AS (
            SELECT * FROM traces_good
            UNION ALL
            SELECT * FROM traces_bad
        )
        SELECT DISTINCT
            parent.service_name AS caller_service,
            parent.span_name AS caller_span_name,
            child.service_name AS callee_service,
            child.span_name AS callee_span_name
        FROM all_traces AS parent
        JOIN all_traces AS child
            ON parent.span_id = child.parent_span_id
            AND parent.trace_id = child.trace_id
        WHERE parent.service_name IS NOT NULL
            AND child.service_name IS NOT NULL
            AND parent.span_name IS NOT NULL
            AND child.span_name IS NOT NULL
        """

        result = self.con.execute(query).fetchall()

        services = set()
        edges = []

        for caller_svc, caller_span, callee_svc, callee_span in result:
            services.add(caller_svc)
            services.add(callee_svc)
            edges.append(
                SpanEdge(
                    caller_service=caller_svc,
                    caller_span_name=caller_span,
                    callee_service=callee_svc,
                    callee_span_name=callee_span,
                )
            )

        return SDG(services=services, edges=edges)
