import re
from typing import Any

import polars as pl
import streamlit as st

from .utils import format_timestamp, truncate_string


class LogsSearcher:
    def __init__(self) -> None:
        self.search_columns = ["message", "log", "content", "text"]

    def search_logs_advanced(
        self,
        df: pl.DataFrame,
        search_term: str = "",
        use_regex: bool = False,
        case_sensitive: bool = False,
        log_level: str = "all",
        service_filter: str = "all",
        time_range: tuple[float, float] | None = None,
    ) -> pl.DataFrame:
        if len(df) == 0:
            return df

        filtered_df = df

        if time_range:
            time_col = self._get_time_column(df)
            if time_col:
                start_time, end_time = time_range
                filtered_df = filtered_df.filter((pl.col(time_col) >= start_time) & (pl.col(time_col) <= end_time))

        if log_level != "all":
            level_col = self._get_level_column(df)
            if level_col:
                filtered_df = filtered_df.filter(pl.col(level_col).str.to_lowercase() == log_level.lower())

        # Service filtering
        if service_filter != "all":
            service_col = self._get_service_column(df)
            if service_col:
                filtered_df = filtered_df.filter(
                    pl.col(service_col).str.to_lowercase().str.contains(service_filter.lower())
                )

        # Text search
        if search_term.strip():
            text_columns = self._get_text_columns(df)
            if text_columns:
                if use_regex:
                    # Regex search
                    text_conditions = []
                    for col in text_columns:
                        try:
                            if case_sensitive:
                                text_conditions.append(pl.col(col).str.contains(f"(?-i){search_term}"))
                            else:
                                text_conditions.append(pl.col(col).str.contains(f"(?i){search_term}"))
                        except Exception:
                            continue

                    if text_conditions:
                        # Combine with OR logic
                        combined_condition = text_conditions[0]
                        for condition in text_conditions[1:]:
                            combined_condition = combined_condition | condition
                        filtered_df = filtered_df.filter(combined_condition)
                else:
                    # Simple text search
                    text_conditions = []
                    for col in text_columns:
                        if case_sensitive:
                            text_conditions.append(pl.col(col).str.contains(search_term))
                        else:
                            text_conditions.append(pl.col(col).str.to_lowercase().str.contains(search_term.lower()))

                    if text_conditions:
                        # Combine with OR logic
                        combined_condition = text_conditions[0]
                        for condition in text_conditions[1:]:
                            combined_condition = combined_condition | condition
                        filtered_df = filtered_df.filter(combined_condition)

        return filtered_df

    def get_log_statistics(self, df: pl.DataFrame) -> dict[str, Any]:
        """Get log statistics using Polars for performance."""
        if len(df) == 0:
            return {}

        stats = {
            "total_logs": len(df),
            "time_range": self._get_time_range(df),
            "log_levels": self._get_log_levels(df),
            "services": self._get_services(df),
            "data_types": self._get_data_types(df),
        }

        return stats

    def create_logs_table(
        self, df: pl.DataFrame, page_size: int = 50, page_num: int = 0
    ) -> tuple[pl.DataFrame, dict[str, Any]]:
        """Create paginated logs table using Polars for improved performance."""
        if len(df) == 0:
            return pl.DataFrame(), {
                "total_pages": 0,
                "current_page": 0,
                "total_logs": 0,
            }

        # Calculate pagination
        total_logs = len(df)
        total_pages = (total_logs - 1) // page_size + 1
        offset = page_num * page_size

        # Get current page data using Polars
        page_df = df.slice(offset, page_size)

        # Format display columns
        display_df = self._format_display_columns(page_df)

        pagination_info = {
            "total_pages": total_pages,
            "current_page": page_num,
            "total_logs": total_logs,
            "start_idx": offset + 1,
            "end_idx": min(offset + page_size, total_logs),
        }

        return display_df, pagination_info

    def highlight_search_term(self, text: str, search_term: str, use_regex: bool = False) -> str:
        """Highlight search keywords"""
        if not search_term.strip() or text is None:
            return str(text)

        text_str = str(text)

        try:
            if use_regex:
                # Regex highlighting
                pattern = re.compile(search_term, re.IGNORECASE)
                highlighted = pattern.sub(lambda m: f"**{m.group()}**", text_str)
            else:
                # Simple text highlighting
                highlighted = text_str.replace(search_term, f"**{search_term}**")
            return highlighted
        except Exception:
            return text_str

    def export_search_results(self, df: pl.DataFrame, filename: str = "search_results.csv") -> str:
        """Export search results using Polars for efficient processing."""
        if len(df) == 0:
            return ""

        try:
            # Prepare export data using Polars
            time_col = self._get_time_column(df)

            if time_col and time_col in df.columns:
                # Format timestamps
                def safe_format_timestamp(x):
                    if x is None:
                        return ""
                    try:
                        if isinstance(x, (int, float)):
                            return format_timestamp(int(x))
                        else:
                            return str(x)
                    except Exception:
                        return str(x)

                export_df = df.with_columns(
                    pl.col(time_col)
                    .map_elements(safe_format_timestamp, return_dtype=pl.Utf8)
                    .alias(f"{time_col}_formatted")
                )
            else:
                export_df = df

            # Export as CSV
            csv_data = export_df.write_csv()
            return csv_data

        except Exception as e:
            st.warning(f"Polars export failed: {str(e)}")
            return ""

    def _get_text_columns(self, df: pl.DataFrame) -> list[str]:
        """Get text search columns"""
        text_columns = []

        # Prefer predefined search columns
        for col in self.search_columns:
            if col in df.columns:
                text_columns.append(col)

        if not text_columns:
            # Get string columns
            text_columns = [col for col in df.columns if df[col].dtype == pl.Utf8]

        return text_columns

    def _get_time_column(self, df: pl.DataFrame) -> str | None:
        time_columns = ["timestamp", "time", "datetime", "ts", "@timestamp"]
        for col in time_columns:
            if col in df.columns:
                return col
        return None

    def _get_level_column(self, df: pl.DataFrame) -> str | None:
        level_columns = ["level", "severity", "log_level", "priority"]
        for col in level_columns:
            if col in df.columns:
                return col
        return None

    def _get_service_column(self, df: pl.DataFrame) -> str | None:
        service_columns = ["service", "service_name", "application", "app", "component"]
        for col in service_columns:
            if col in df.columns:
                return col
        return None

    def _get_time_range(self, df: pl.DataFrame) -> dict[str, Any]:
        """Get time range"""
        time_col = self._get_time_column(df)
        if not time_col or time_col not in df.columns:
            return {}

        try:
            time_series = df[time_col].drop_nulls()
            if len(time_series) == 0:
                return {}

            min_time = time_series.min()
            max_time = time_series.max()

            # Handle different time formats
            try:
                if min_time is not None:
                    if isinstance(min_time, (int, float)):
                        start_formatted = format_timestamp(int(min_time))
                    else:
                        start_formatted = str(min_time)
                else:
                    start_formatted = ""

                if max_time is not None:
                    if isinstance(max_time, (int, float)):
                        end_formatted = format_timestamp(int(max_time))
                    else:
                        end_formatted = str(max_time)
                else:
                    end_formatted = ""
            except Exception:
                start_formatted = str(min_time) if min_time is not None else ""
                end_formatted = str(max_time) if max_time is not None else ""

            return {
                "start": min_time,
                "end": max_time,
                "start_formatted": start_formatted,
                "end_formatted": end_formatted,
            }
        except Exception:
            return {}

    def _get_log_levels(self, df: pl.DataFrame) -> dict[str, int]:
        level_col = self._get_level_column(df)
        if not level_col or level_col not in df.columns:
            return {}

        try:
            counts = df[level_col].value_counts()
            return dict(zip(counts[level_col].to_list(), counts["count"].to_list()))
        except Exception:
            return {}

    def _get_services(self, df: pl.DataFrame) -> dict[str, int]:
        service_col = self._get_service_column(df)
        if not service_col or service_col not in df.columns:
            return {}

        try:
            counts = df[service_col].value_counts()
            return dict(zip(counts[service_col].to_list(), counts["count"].to_list()))
        except Exception:
            return {}

    def _get_data_types(self, df: pl.DataFrame) -> dict[str, int]:
        if "data_type" not in df.columns:
            return {}

        try:
            counts = df["data_type"].value_counts()
            return dict(zip(counts["data_type"].to_list(), counts["count"].to_list()))
        except Exception:
            return {}

    def _format_display_columns(self, df: pl.DataFrame) -> pl.DataFrame:
        """Format display columns"""
        display_df = df.clone()

        # Format timestamps
        time_col = self._get_time_column(display_df)
        if time_col and time_col in display_df.columns:

            def safe_format_timestamp(x):
                if x is None:
                    return ""
                try:
                    if isinstance(x, (int, float)):
                        return format_timestamp(int(x))
                    else:
                        return str(x)
                except Exception:
                    return str(x)

            display_df = display_df.with_columns(
                pl.col(time_col).map_elements(safe_format_timestamp, return_dtype=pl.Utf8).alias("Time")
            )

        # Truncate long text
        text_columns = self._get_text_columns(display_df)
        for col in text_columns:
            if col in display_df.columns:
                display_df = display_df.with_columns(
                    pl.col(col).map_elements(
                        lambda x: truncate_string(str(x), 200) if x is not None else "",
                        return_dtype=pl.Utf8,
                    )
                )

        # Reorder columns
        ordered_columns = []
        if "Time" in display_df.columns:
            ordered_columns.append("Time")

        # Add important columns
        important_cols = ["level", "service", "message", "data_type"]
        for col in important_cols:
            if col in display_df.columns and col not in ordered_columns:
                ordered_columns.append(col)

        # Add remaining columns
        for col in display_df.columns:
            if col not in ordered_columns:
                ordered_columns.append(col)

        return display_df.select(ordered_columns)
