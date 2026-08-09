import polars as pl
import streamlit as st


class TracesVisualizer:
    def __init__(self) -> None:
        """Initialize the traces visualizer."""
        pass

    def get_service_list(self, df: pl.DataFrame) -> list[str]:
        """Get list of available services using Polars."""
        if df.is_empty():
            return []

        service_col = self._get_service_column(df)
        if not service_col:
            return []

        try:
            services = (
                df.select(pl.col(service_col))
                .filter(pl.col(service_col).is_not_null())
                .unique()
                .sort(pl.col(service_col).str.to_lowercase())
                .to_series()
                .to_list()
            )
            return services
        except Exception:
            return []

    def get_span_aggregated_stats(self, df: pl.DataFrame, service_name: str) -> pl.DataFrame:
        """Get aggregated statistics by span name for a specific service."""
        if df.is_empty():
            return pl.DataFrame()

        service_col = self._get_service_column(df)
        span_col = self._get_operation_column(df)

        if not service_col or not span_col:
            return pl.DataFrame()

        try:
            # Filter data for the specific service
            service_data = df.filter(pl.col(service_col) == service_name)
            if service_data.is_empty():
                return pl.DataFrame()

            # Prepare duration column
            if "duration" in df.columns:
                duration_expr = (pl.col("duration") / 1000000).alias("duration_ms")
            else:
                duration_expr = pl.lit(0).alias("duration_ms")

            error_fields = ["error_message", "errorMessage", "error", "exception"]
            status_fields = ["status", "status_code", "attr.http.response.status_code"]

            # Create error detection expression
            error_conditions = []

            # Check error fields
            for field in error_fields:
                if field in df.columns:
                    error_conditions.append(pl.col(field).is_not_null() & (pl.col(field).cast(pl.Utf8) != ""))

            # Check status codes
            for field in status_fields:
                if field in df.columns:
                    error_conditions.append(pl.col(field).is_not_null() & (pl.col(field) >= 400))

            # Combine error conditions
            if error_conditions:
                is_error_expr = pl.any_horizontal(error_conditions).alias("is_error")
            else:
                is_error_expr = pl.lit(False).alias("is_error")

            # Aggregate by span name
            aggregated = (
                service_data.with_columns([duration_expr, is_error_expr])
                .group_by(span_col)
                .agg(
                    [
                        pl.count().alias("total_calls"),
                        pl.col("duration_ms").mean().alias("avg_duration_ms"),
                        pl.col("duration_ms").median().alias("median_duration_ms"),
                        pl.col("duration_ms").min().alias("min_duration_ms"),
                        pl.col("duration_ms").max().alias("max_duration_ms"),
                        pl.col("duration_ms").quantile(0.9, interpolation="linear").alias("p90_duration_ms"),
                        pl.col("duration_ms").quantile(0.95, interpolation="linear").alias("p95_duration_ms"),
                        pl.col("duration_ms").quantile(0.99, interpolation="linear").alias("p99_duration_ms"),
                        pl.col("is_error").sum().alias("error_count"),
                        pl.col("is_error").mean().alias("error_rate"),
                    ]
                )
                .with_columns([(pl.col("error_rate") * 100).round(2).alias("error_rate_pct")])
                .sort("total_calls", descending=True)
            )

            # Rename span column for display
            result = aggregated.rename({span_col: "span_name"})

            return result

        except Exception as e:
            st.warning(f"Error aggregating span stats: {str(e)}")
            return pl.DataFrame()

    def format_aggregated_stats_for_display(self, stats_df: pl.DataFrame) -> pl.DataFrame:
        """Format aggregated statistics for display in Streamlit."""
        if stats_df.is_empty():
            return pl.DataFrame()

        try:
            # Create display DataFrame with formatted columns
            display_data = []
            for row in stats_df.iter_rows(named=True):
                display_row = {
                    "Span Name": row["span_name"],
                    "Total Calls": f"{row['total_calls']:,}",
                    "Avg Duration (ms)": f"{row['avg_duration_ms']:.2f}" if row["avg_duration_ms"] else "0.00",
                    "Median Duration (ms)": f"{row['median_duration_ms']:.2f}" if row["median_duration_ms"] else "0.00",
                    "P90 Duration (ms)": f"{row['p90_duration_ms']:.2f}" if row["p90_duration_ms"] else "0.00",
                    "P95 Duration (ms)": f"{row['p95_duration_ms']:.2f}" if row["p95_duration_ms"] else "0.00",
                    "P99 Duration (ms)": f"{row['p99_duration_ms']:.2f}" if row["p99_duration_ms"] else "0.00",
                    "Min Duration (ms)": f"{row['min_duration_ms']:.2f}" if row["min_duration_ms"] else "0.00",
                    "Max Duration (ms)": f"{row['max_duration_ms']:.2f}" if row["max_duration_ms"] else "0.00",
                    "Error Count": f"{row['error_count']:,}",
                    "Error Rate (%)": f"{row['error_rate_pct']:.2f}%",
                }
                display_data.append(display_row)

            return pl.DataFrame(display_data)

        except Exception as e:
            st.warning(f"Error formatting display data: {str(e)}")
            return pl.DataFrame()

    def _get_service_column(self, df: pl.DataFrame) -> str | None:
        """Get service column name."""
        service_columns = [
            "service_name",
            "serviceName",
            "service",
            "application",
            "app_name",
            "microservice",
            "component",
            "resource.service.name",
        ]
        for col in service_columns:
            if col in df.columns:
                return col
        return None

    def _get_operation_column(self, df: pl.DataFrame) -> str | None:
        """Get operation/API column name."""
        operation_columns = [
            "span_name",
            "operation_name",
            "operationName",
            "operation",
            "api",
            "endpoint",
            "method",
            "http.route",
            "http.target",
        ]
        for col in operation_columns:
            if col in df.columns:
                return col
        return None

    def _get_time_column(self, df: pl.DataFrame) -> str | None:
        """Get timestamp column name."""
        time_columns = [
            "timestamp",
            "start_time",
            "startTime",
            "start_timestamp",
            "time",
            "event_time",
            "span_start_time",
        ]
        for col in time_columns:
            if col in df.columns:
                return col
        return None

    def _get_trace_id_column(self, df: pl.DataFrame) -> str | None:
        """Get trace ID column name."""
        trace_columns = ["trace_id", "traceId", "traceID", "trace"]
        for col in trace_columns:
            if col in df.columns:
                return col
        return None

    def _determine_status_from_row(self, row) -> str:
        """Determine status from query result row."""
        # Check various status fields
        status_fields = ["status", "status_code", "attr_http_response_status_code"]
        error_fields = ["error_message", "errorMessage", "error", "exception"]

        # Check for explicit error indicators
        for field in error_fields:
            if field in row and row[field] and str(row[field]).strip():
                return "Error"

        # Check status codes
        for field in status_fields:
            if field in row and row[field] is not None:
                status_value = row[field]
                if isinstance(status_value, (int, float)):
                    if status_value >= 400:
                        return "Error"
                    elif status_value >= 200 and status_value < 400:
                        return "Success"
                elif isinstance(status_value, str):
                    status_lower = status_value.lower()
                    if any(error_word in status_lower for error_word in ["error", "fail", "exception"]):
                        return "Error"
                    elif any(success_word in status_lower for success_word in ["ok", "success", "complete"]):
                        return "Success"

        return "Unknown"

    def _extract_error_details_from_row(self, row) -> str:
        """Extract error details from query result row."""
        error_fields = ["error_message", "errorMessage", "error", "exception"]

        for field in error_fields:
            if field in row and row[field] and str(row[field]).strip():
                error_msg = str(row[field])
                return error_msg[:100] + "..." if len(error_msg) > 100 else error_msg

        # Check if it's an HTTP error
        if "attr_http_response_status_code" in row and row["attr_http_response_status_code"] is not None:
            status_code = row["attr_http_response_status_code"]
            if isinstance(status_code, (int, float)) and status_code >= 400:
                return f"HTTP {int(status_code)}"

        return ""

    def _get_span_status(self, row) -> str:
        """Get span status from row."""
        return self._determine_status_from_row(row)

    def _get_error_details(self, row) -> str:
        """Extract error details from a span."""
        return self._extract_error_details_from_row(row)

    def _format_timestamp(self, timestamp) -> str:
        """Format timestamp for display."""
        if not timestamp or (isinstance(timestamp, str) and not timestamp.strip()):
            return ""

        try:
            from datetime import datetime

            if isinstance(timestamp, str):
                # Try to parse string timestamp
                try:
                    # Try common formats
                    for fmt in [
                        "%Y-%m-%d %H:%M:%S",
                        "%Y-%m-%dT%H:%M:%S",
                        "%Y-%m-%dT%H:%M:%S.%f",
                    ]:
                        try:
                            dt = datetime.strptime(timestamp, fmt)
                            return dt.strftime("%Y-%m-%d %H:%M:%S")
                        except ValueError:
                            continue
                    # If none work, just return the string
                    return str(timestamp)
                except Exception:
                    return str(timestamp)
            elif isinstance(timestamp, (int, float)):
                # Assume it's a Unix timestamp (nanoseconds or seconds)
                if timestamp > 1e12:  # Nanoseconds
                    dt = datetime.fromtimestamp(timestamp / 1e9)
                elif timestamp > 1e9:  # Milliseconds
                    dt = datetime.fromtimestamp(timestamp / 1e3)
                else:  # Seconds
                    dt = datetime.fromtimestamp(timestamp)
                return dt.strftime("%Y-%m-%d %H:%M:%S")
            else:
                return str(timestamp)
        except Exception:
            return str(timestamp)

    def get_upstream_services(self, df: pl.DataFrame, selected_service: str) -> list[str]:
        """Get upstream services that call the selected service."""
        if df.is_empty():
            return []

        service_col = self._get_service_column(df)
        span_id_col = "span_id"
        parent_span_id_col = "parent_span_id"

        if not service_col or span_id_col not in df.columns or parent_span_id_col not in df.columns:
            return []

        try:
            # Get all spans for the selected service
            selected_service_spans = df.filter(pl.col(service_col) == selected_service)

            if selected_service_spans.is_empty():
                return []

            # Get parent span IDs of the selected service spans
            parent_span_ids = (
                selected_service_spans.select(parent_span_id_col)
                .filter(pl.col(parent_span_id_col).is_not_null() & (pl.col(parent_span_id_col) != ""))
                .unique()
                .to_series()
                .to_list()
            )

            if not parent_span_ids:
                return []

            # Find services that own these parent spans
            upstream_services = (
                df.filter(pl.col(span_id_col).is_in(parent_span_ids))
                .select(service_col)
                .filter(pl.col(service_col).is_not_null() & (pl.col(service_col) != selected_service))
                .unique()
                .sort(service_col)
                .to_series()
                .to_list()
            )

            return upstream_services

        except Exception as e:
            st.warning(f"Error getting upstream services: {str(e)}")
            return []

    def get_downstream_services(self, df: pl.DataFrame, selected_service: str) -> list[str]:
        """Get downstream services that are called by the selected service."""
        if df.is_empty():
            return []

        service_col = self._get_service_column(df)
        span_id_col = "span_id"
        parent_span_id_col = "parent_span_id"

        if not service_col or span_id_col not in df.columns or parent_span_id_col not in df.columns:
            return []

        try:
            # Get all span IDs for the selected service
            selected_service_span_ids = (
                df.filter(pl.col(service_col) == selected_service).select(span_id_col).to_series().to_list()
            )

            if not selected_service_span_ids:
                return []

            # Find services whose spans have the selected service spans as parents
            downstream_services = (
                df.filter(pl.col(parent_span_id_col).is_in(selected_service_span_ids))
                .select(service_col)
                .filter(pl.col(service_col).is_not_null() & (pl.col(service_col) != selected_service))
                .unique()
                .sort(service_col)
                .to_series()
                .to_list()
            )

            return downstream_services

        except Exception as e:
            st.warning(f"Error getting downstream services: {str(e)}")
            return []
