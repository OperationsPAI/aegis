from typing import Any

import plotly.graph_objects as go
import polars as pl
import streamlit as st
from plotly.subplots import make_subplots

from .utils import get_injection_time_markers


class MetricsVisualizer:
    """Simplified metrics visualizer with Polars acceleration."""

    def __init__(self) -> None:
        """Initialize the metrics visualizer."""
        self.colors = {
            "normal": "#1f77b4",
            "abnormal": "#ff7f0e",
            "injection_start": "#d62728",
            "injection_end": "#2ca02c",
        }

    def _get_time_range(self, df: pl.DataFrame, time_col: str) -> tuple[Any | None, Any | None]:
        """Get the actual time range from the data."""
        try:
            min_time = df[time_col].min()
            max_time = df[time_col].max()
            return min_time, max_time
        except Exception:
            return None, None

    def create_time_series_plot(
        self,
        df: pl.DataFrame,
        metrics: list[str],
        env_data: dict[str, Any],
        title: str = "Time Series Metrics Visualization",
        subplot_height: int = 400,  # 调整默认高度为更合理的400像素
    ) -> go.Figure:
        if len(df) == 0 or not metrics:
            return self._create_empty_figure()

        # Get time column
        time_col = self._get_time_column(df)
        if not time_col:
            st.error("Timestamp column not found")
            return self._create_empty_figure()

        return self._create_long_format_plot(df, metrics, env_data, title, time_col, subplot_height)

    def _create_empty_figure(self) -> go.Figure:
        """Create empty figure with message."""
        fig = go.Figure()
        fig.add_annotation(
            text="No data to display",
            xref="paper",
            yref="paper",
            x=0.5,
            y=0.5,
            showarrow=False,
            font=dict(size=16, color="gray"),
        )
        return fig

    def _create_long_format_plot(
        self,
        df: pl.DataFrame,
        metrics: list[str],
        env_data: dict[str, Any],
        title: str,
        time_col: str,
        subplot_height: int = 400,  # 调整默认高度
    ) -> go.Figure:
        """Create plot for long format data using Polars."""

        # Calculate subplot layout
        import math

        cols = min(3, len(metrics))
        rows = math.ceil(len(metrics) / cols)

        # 根据行数调整间距
        if rows <= 2:
            vertical_spacing = 0.08
        elif rows <= 4:
            vertical_spacing = 0.06
        else:
            vertical_spacing = 0.04

        fig = make_subplots(
            rows=rows,
            cols=cols,
            shared_xaxes=True,
            subplot_titles=metrics,
            vertical_spacing=vertical_spacing,
            horizontal_spacing=0.03,
        )

        # Simple color palette
        colors = ["#1f77b4", "#ff7f0e", "#2ca02c", "#d62728", "#9467bd", "#8c564b"]

        for i, metric in enumerate(metrics):
            row = (i // cols) + 1
            col = (i % cols) + 1

            try:
                # Filter data for this metric
                metric_data = (
                    df.filter(pl.col("metric") == metric)
                    .with_columns(pl.col("data_type").fill_null("normal"))
                    .sort([time_col, "service_name"])
                )

                if len(metric_data) == 0:
                    continue

                # Plot each service
                services = metric_data["service_name"].unique().to_list()
                for j, service in enumerate(services):
                    service_data = metric_data.filter(pl.col("service_name") == service)
                    color = colors[j % len(colors)]

                    # Separate normal and abnormal data if available
                    if "data_type" in service_data.columns:
                        normal_data = service_data.filter(pl.col("data_type") == "normal")
                        abnormal_data = service_data.filter(pl.col("data_type") == "abnormal")

                        if len(normal_data) > 0:
                            fig.add_trace(
                                go.Scatter(
                                    x=normal_data[time_col].to_list(),
                                    y=normal_data["value"].to_list(),
                                    mode="lines",
                                    name=f"{service} (Normal)" if i == 0 else None,
                                    line=dict(color=color),
                                    showlegend=(i == 0),
                                    hovertemplate=f"<b>{service}</b><br>%{{x}}<br>%{{y}}<extra></extra>",
                                ),
                                row=row,
                                col=col,
                            )

                        if len(abnormal_data) > 0:
                            fig.add_trace(
                                go.Scatter(
                                    x=abnormal_data[time_col].to_list(),
                                    y=abnormal_data["value"].to_list(),
                                    mode="lines",
                                    name=f"{service} (Abnormal)" if i == 0 else None,
                                    line=dict(color=color, width=3),
                                    showlegend=(i == 0),
                                    hovertemplate=f"<b>{service}</b><br>%{{x}}<br>%{{y}}<extra></extra>",
                                ),
                                row=row,
                                col=col,
                            )
                    else:
                        fig.add_trace(
                            go.Scatter(
                                x=service_data[time_col].to_list(),
                                y=service_data["value"].to_list(),
                                mode="lines",
                                name=service if i == 0 else None,
                                line=dict(color=color),
                                showlegend=(i == 0),
                                hovertemplate=f"<b>{service}</b><br>%{{x}}<br>%{{y}}<extra></extra>",
                            ),
                            row=row,
                            col=col,
                        )

            except Exception as e:
                st.warning(f"Failed to plot metric {metric}: {str(e)}")
                continue

        time_markers = get_injection_time_markers(env_data)
        if time_markers:
            self._add_injection_markers(fig, time_markers, rows, cols)

        min_time, max_time = self._get_time_range(df, time_col)

        ideal_subplot_height = subplot_height
        max_total_height = 3000
        min_subplot_height = 300

        total_height = ideal_subplot_height * rows + 100
        if total_height > max_total_height:
            available_height = max_total_height - 100
            actual_subplot_height = max(min_subplot_height, available_height // rows)
            total_height = actual_subplot_height * rows + 100
        else:
            actual_subplot_height = ideal_subplot_height

        # Update layout
        fig.update_layout(title=title, height=total_height, showlegend=True, hovermode="x unified")

        # Update x-axes with time range for all subplots
        for row_idx in range(1, rows + 1):
            for col_idx in range(1, cols + 1):
                fig.update_xaxes(
                    title_text="Timestamp" if row_idx == rows else None,
                    range=[min_time, max_time] if min_time and max_time else None,
                    row=row_idx,
                    col=col_idx,
                )

        return fig

    def _get_time_column(self, df: pl.DataFrame) -> str | None:
        """Get timestamp column name."""
        time_columns = ["timestamp", "time", "datetime", "ts"]

        for col in time_columns:
            if col in df.columns:
                return col

        # Look for columns with 'time' in name
        for col in df.columns:
            if "time" in col.lower():
                col_dtype = str(df[col].dtype)
                if any(dtype in col_dtype.lower() for dtype in ["int", "float", "datetime"]):
                    return col

        return None

    def _add_injection_markers(self, fig: go.Figure, time_markers: dict[str, Any], rows: int, cols: int):
        """Add fault injection markers to all subplots."""

        if not time_markers:
            return

        # Add start marker
        if "abnormal_start" in time_markers:
            timestamp = time_markers["abnormal_start"]

            for row in range(1, rows + 1):
                for col in range(1, cols + 1):
                    fig.add_shape(
                        type="line",
                        x0=timestamp,
                        x1=timestamp,
                        y0=0,
                        y1=1,
                        yref="paper",
                        line=dict(color=self.colors["injection_start"], dash="dash", width=2),
                        row=row,
                        col=col,
                    )

            fig.add_annotation(
                x=timestamp,
                y=0.95,
                yref="paper",
                text="Fault start",
                showarrow=False,
                font=dict(color=self.colors["injection_start"], size=10),
                bgcolor="white",
                bordercolor=self.colors["injection_start"],
                borderwidth=1,
            )

        # Add end marker
        if "abnormal_end" in time_markers:
            timestamp = time_markers["abnormal_end"]

            for row in range(1, rows + 1):
                for col in range(1, cols + 1):
                    fig.add_shape(
                        type="line",
                        x0=timestamp,
                        x1=timestamp,
                        y0=0,
                        y1=1,
                        yref="paper",
                        line=dict(color=self.colors["injection_end"], dash="dash", width=2),
                        row=row,
                        col=col,
                    )

            fig.add_annotation(
                x=timestamp,
                y=0.90,
                yref="paper",
                text="Fault end",
                showarrow=False,
                font=dict(color=self.colors["injection_end"], size=10),
                bgcolor="white",
                bordercolor=self.colors["injection_end"],
                borderwidth=1,
            )

    def _add_single_plot_markers(self, fig: go.Figure, time_markers: dict[str, Any]):
        """Add fault injection markers to single plot."""

        if not time_markers:
            return

        # Add start marker
        if "abnormal_start" in time_markers:
            timestamp = time_markers["abnormal_start"]
            fig.add_shape(
                type="line",
                x0=timestamp,
                x1=timestamp,
                y0=0,
                y1=1,
                yref="paper",
                line=dict(color=self.colors["injection_start"], dash="dash", width=2),
            )
            fig.add_annotation(
                x=timestamp,
                y=0.95,
                yref="paper",
                text="Fault start",
                showarrow=False,
                font=dict(color=self.colors["injection_start"], size=10),
                bgcolor="white",
                bordercolor=self.colors["injection_start"],
                borderwidth=1,
            )

        # Add end marker
        if "abnormal_end" in time_markers:
            timestamp = time_markers["abnormal_end"]
            fig.add_shape(
                type="line",
                x0=timestamp,
                x1=timestamp,
                y0=0,
                y1=1,
                yref="paper",
                line=dict(color=self.colors["injection_end"], dash="dash", width=2),
            )
            fig.add_annotation(
                x=timestamp,
                y=0.90,
                yref="paper",
                text="Fault end",
                showarrow=False,
                font=dict(color=self.colors["injection_end"], size=10),
                bgcolor="white",
                bordercolor=self.colors["injection_end"],
                borderwidth=1,
            )
