from pathlib import Path
from typing import Any

import polars as pl
import streamlit as st

from .utils import cached_load_json, cached_load_parquet, validate_dataset_folder


class DataLoader:
    def __init__(self, dataset_path: str | None = None) -> None:
        self.dataset_path = Path(dataset_path) if dataset_path else None
        self._env_data: dict[str, Any] | None = None
        self._injection_data: dict[str, Any] | None = None
        self._conclusion_data: pl.DataFrame | None = None
        self._metrics_data: dict[str, pl.DataFrame] = {}
        self._logs_data: dict[str, pl.DataFrame] = {}
        self._traces_data: dict[str, pl.DataFrame] = {}

    def set_dataset_path(self, path: str) -> bool:
        dataset_path = Path(path)
        is_valid, missing_files = validate_dataset_folder(dataset_path)

        if not is_valid:
            st.error(f"Dataset validation failed, missing files: {', '.join(missing_files)}")
            return False

        self.dataset_path = dataset_path
        self._clear_cache()
        return True

    def _clear_cache(self) -> None:
        self._env_data = None
        self._injection_data = None
        self._conclusion_data = None
        self._metrics_data = {}
        self._logs_data = {}
        self._traces_data = {}

    @property
    def is_loaded(self) -> bool:
        return self.dataset_path is not None and self.dataset_path.exists()

    def get_env_data(self) -> dict[str, Any]:
        if not self.is_loaded or self.dataset_path is None:
            return {}

        if self._env_data is None:
            env_file = self.dataset_path / "env.json"
            self._env_data = cached_load_json(str(env_file))

        return self._env_data

    def get_injection_data(self) -> dict[str, Any]:
        if not self.is_loaded or self.dataset_path is None:
            return {}

        if self._injection_data is None:
            injection_file = self.dataset_path / "injection.json"
            self._injection_data = cached_load_json(str(injection_file))

        return self._injection_data

    def get_conclusion_data(self) -> pl.DataFrame:
        if not self.is_loaded or self.dataset_path is None:
            return pl.DataFrame()

        if self._conclusion_data is None:
            conclusion_file = self.dataset_path / "conclusion.parquet"
            self._conclusion_data = cached_load_parquet(str(conclusion_file))

        return self._conclusion_data

    def get_metrics_data(self, data_type: str = "both") -> pl.DataFrame:
        if not self.is_loaded or self.dataset_path is None:
            return pl.DataFrame()

        if data_type not in self._metrics_data:
            if data_type == "normal":
                metrics_file = self.dataset_path / "normal_metrics.parquet"
                self._metrics_data[data_type] = cached_load_parquet(str(metrics_file))
            elif data_type == "abnormal":
                metrics_file = self.dataset_path / "abnormal_metrics.parquet"
                self._metrics_data[data_type] = cached_load_parquet(str(metrics_file))
            elif data_type == "both":
                normal_df = self.get_metrics_data("normal")
                abnormal_df = self.get_metrics_data("abnormal")

                if not normal_df.is_empty() and not abnormal_df.is_empty():
                    # Use Polars to combine data efficiently
                    try:
                        normal_df = normal_df.with_columns(pl.lit("normal").alias("data_type"))
                        abnormal_df = abnormal_df.with_columns(pl.lit("abnormal").alias("data_type"))
                        combined_df = pl.concat([normal_df, abnormal_df])
                        self._metrics_data[data_type] = combined_df
                    except Exception:
                        # Fallback if concat fails
                        self._metrics_data[data_type] = normal_df
                elif not normal_df.is_empty():
                    self._metrics_data[data_type] = normal_df
                elif not abnormal_df.is_empty():
                    self._metrics_data[data_type] = abnormal_df
                else:
                    self._metrics_data[data_type] = pl.DataFrame()

        return self._metrics_data.get(data_type, pl.DataFrame())

    def get_logs_data(self, data_type: str = "both") -> pl.DataFrame:
        if not self.is_loaded or self.dataset_path is None:
            return pl.DataFrame()

        if data_type not in self._logs_data:
            if data_type == "normal":
                logs_file = self.dataset_path / "normal_logs.parquet"
                self._logs_data[data_type] = cached_load_parquet(str(logs_file))
            elif data_type == "abnormal":
                logs_file = self.dataset_path / "abnormal_logs.parquet"
                self._logs_data[data_type] = cached_load_parquet(str(logs_file))
            elif data_type == "both":
                normal_df = self.get_logs_data("normal")
                abnormal_df = self.get_logs_data("abnormal")

                if not normal_df.is_empty() and not abnormal_df.is_empty():
                    try:
                        normal_df = normal_df.with_columns(pl.lit("normal").alias("data_type"))
                        abnormal_df = abnormal_df.with_columns(pl.lit("abnormal").alias("data_type"))
                        combined_df = pl.concat([normal_df, abnormal_df])
                        self._logs_data[data_type] = combined_df
                    except Exception:
                        self._logs_data[data_type] = normal_df
                elif not normal_df.is_empty():
                    self._logs_data[data_type] = normal_df
                elif not abnormal_df.is_empty():
                    self._logs_data[data_type] = abnormal_df
                else:
                    self._logs_data[data_type] = pl.DataFrame()

        return self._logs_data.get(data_type, pl.DataFrame())

    def get_traces_data(self, data_type: str = "both") -> pl.DataFrame:
        if not self.is_loaded or self.dataset_path is None:
            return pl.DataFrame()

        if data_type not in self._traces_data:
            if data_type == "normal":
                traces_file = self.dataset_path / "normal_traces.parquet"
                self._traces_data[data_type] = cached_load_parquet(str(traces_file))
            elif data_type == "abnormal":
                traces_file = self.dataset_path / "abnormal_traces.parquet"
                self._traces_data[data_type] = cached_load_parquet(str(traces_file))
            elif data_type == "both":
                normal_df = self.get_traces_data("normal")
                abnormal_df = self.get_traces_data("abnormal")

                if not normal_df.is_empty() and not abnormal_df.is_empty():
                    # Use Polars to combine data efficiently
                    try:
                        normal_df = normal_df.with_columns(pl.lit("normal").alias("data_type"))
                        abnormal_df = abnormal_df.with_columns(pl.lit("abnormal").alias("data_type"))
                        combined_df = pl.concat([normal_df, abnormal_df])
                        self._traces_data[data_type] = combined_df
                    except Exception:
                        # Fallback if concat fails
                        self._traces_data[data_type] = normal_df
                elif not normal_df.is_empty():
                    self._traces_data[data_type] = normal_df
                elif not abnormal_df.is_empty():
                    self._traces_data[data_type] = abnormal_df
                else:
                    self._traces_data[data_type] = pl.DataFrame()

        return self._traces_data.get(data_type, pl.DataFrame())

    def get_available_metrics(self) -> list[str]:
        metrics_df = self.get_metrics_data("both")
        if metrics_df.is_empty():
            return []

        if "metric" in metrics_df.columns:
            try:
                unique_metrics = (
                    metrics_df.select("metric").filter(pl.col("metric").is_not_null()).unique().sort("metric")
                )
                return unique_metrics["metric"].to_list()
            except Exception:
                # Fallback
                return sorted([m for m in metrics_df["metric"].unique().to_list() if m is not None])

        # Get numeric columns excluding certain ones
        exclude_cols = ["timestamp", "time", "data_type"]
        numeric_cols = [
            col for col in metrics_df.columns if metrics_df[col].dtype in [pl.Int64, pl.Float64, pl.Int32, pl.Float32]
        ]
        return [col for col in numeric_cols if col not in exclude_cols]

    def get_available_services(self) -> list[str]:
        metrics_df = self.get_metrics_data("both")
        if metrics_df.is_empty():
            return []

        if "service_name" in metrics_df.columns:
            try:
                unique_services = (
                    metrics_df.select("service_name")
                    .filter(pl.col("service_name").is_not_null())
                    .filter(pl.col("service_name").str.len_chars() > 0)
                    .unique()
                    .sort("service_name")
                )
                return unique_services["service_name"].to_list()
            except Exception:
                # Fallback
                services = metrics_df["service_name"].unique().to_list()
                return sorted([s for s in services if s and str(s).strip()])

        return []

    def get_metrics_by_service(self, services: list[str], metrics: list[str], data_type: str = "both") -> pl.DataFrame:
        metrics_df = self.get_metrics_data(data_type)
        if metrics_df.is_empty():
            return pl.DataFrame()

        if "metric" in metrics_df.columns and "service_name" in metrics_df.columns:
            try:
                filtered_df = metrics_df.filter(
                    pl.col("service_name").is_in(services) & pl.col("metric").is_in(metrics)
                )
                return filtered_df
            except Exception:
                # Return empty DataFrame if filtering fails
                return pl.DataFrame()

        time_col = self._get_time_column(metrics_df)
        cols_to_keep = metrics + ([time_col] if time_col else [])
        if "data_type" in metrics_df.columns:
            cols_to_keep.append("data_type")

        available_cols = [col for col in cols_to_keep if col in metrics_df.columns]
        return metrics_df.select(available_cols)

    def _get_time_column(self, df: pl.DataFrame) -> str | None:
        time_columns = ["timestamp", "time", "datetime", "ts"]
        for col in time_columns:
            if col in df.columns:
                return col

        for col in df.columns:
            if "time" in col.lower() and df[col].dtype in [pl.Int64, pl.Float64]:
                return col

        return None

    def get_dataset_summary(self) -> dict[str, Any]:
        if not self.is_loaded:
            return {}

        env_data = self.get_env_data()
        injection_data = self.get_injection_data()
        metrics_df = self.get_metrics_data("both")
        logs_df = self.get_logs_data("both")
        traces_df = self.get_traces_data("both")

        return {
            "dataset_path": str(self.dataset_path),
            "namespace": env_data.get("NAMESPACE", "Unknown"),
            "fault_type": injection_data.get("fault_type", "Unknown"),
            "metrics_count": len(metrics_df),
            "logs_count": len(logs_df),
            "traces_count": len(traces_df),
            "available_metrics": len(self.get_available_metrics()),
            "time_range": {
                "normal_start": env_data.get("NORMAL_START"),
                "normal_end": env_data.get("NORMAL_END"),
                "abnormal_start": env_data.get("ABNORMAL_START"),
                "abnormal_end": env_data.get("ABNORMAL_END"),
            },
        }

    def __del__(self) -> None:
        """Cleanup when object is destroyed."""
        pass  # No cleanup needed for Polars-based implementation


@st.cache_resource
def get_data_loader() -> DataLoader:
    return DataLoader()
