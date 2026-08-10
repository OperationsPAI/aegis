from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any

import altair as alt
import numpy as np
import polars as pl

from ..sdk.logging import logger


@dataclass
class BarMeta:
    """
    Metadata for chart visualization.
    """

    data: list[dict[str, Any]]
    x_label: str
    y_label: str
    title: str

    def to_dict(self) -> dict[str, Any]:
        return {
            "data": self.data,
            "x_label": self.x_label,
            "y_label": self.y_label,
            "title": self.title,
        }


@dataclass
class HeatmapMeta:
    """
    Metadata for heatmap visualization.
    """

    x: list[str]
    y: list[str]
    x_label: str
    y_label: str
    title: str
    matrix: np.ndarray
    save_path: Path

    def __post_init__(self):
        if not isinstance(self.matrix, np.ndarray):
            raise ValueError("Matrix must be a numpy ndarray")
        if not self.save_path.parent.exists():
            self.save_path.parent.mkdir(parents=True, exist_ok=True)

    def to_dict(self) -> dict[str, Any]:
        return {
            "x": self.x,
            "y": self.y,
            "x_label": self.x_label,
            "y_label": self.y_label,
            "title": self.title,
            "matrix": self.matrix.tolist(),
            "save_path": str(self.save_path),
        }


@dataclass
class BarConfig:
    """
    Configuration for common distribution visualization.
    """

    x_label: str
    title: str
    filename: str
    output_csv: bool

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass
class HeatmapConfig:
    """
    Configuration for heatmap chart visualization.
    """

    y_label: str
    title: str
    filename: str

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass
class VisInjectionsConfig:
    """
    Configuration for visualization of injections.
    """

    bar_configs: dict[str, BarConfig]
    heatmap_configs: dict[str, HeatmapConfig]

    def to_dict(self) -> dict[str, Any]:
        return {
            "bar_configs": {key: config.to_dict() for key, config in self.bar_configs.items()},
            "heatmap_configs": {key: config.to_dict() for key, config in self.heatmap_configs.items()},
        }


def NewVisInjectionsConfig() -> VisInjectionsConfig:
    return VisInjectionsConfig(
        bar_configs={
            "faults": BarConfig(
                x_label="Fault Type",
                title="Fault Distribution",
                filename="faults",
                output_csv=True,
            ),
            "services": BarConfig(
                x_label="Service Name",
                title="Service Distribution",
                filename="services",
                output_csv=True,
            ),
        },
        heatmap_configs={
            "fault_services": HeatmapConfig(
                y_label="Service Name",
                title="Fault-Service Count Distribution",
                filename="fault_services",
            ),
            "fault_pair_attribute_coverages": HeatmapConfig(
                y_label="Pair Name",
                title="Fault-Pair Attribute Coverage Distribution",
                filename="fault_pair_attribute_coverages",
            ),
            "fault_service_attribute_coverages": HeatmapConfig(
                y_label="Service Name",
                title="Fault-Service Attribute Coverage Distribution",
                filename="fault_service_attribute_coverages",
            ),
        },
    )


def plot_bar(meta: BarMeta) -> alt.Chart:
    df = pl.DataFrame(meta.data)
    base = alt.Chart(data=df.to_pandas())

    degree_selection = alt.selection_point(fields=["degree"], bind="legend")
    degree_color = alt.Color(
        "degree",
        title="Degree",
        scale=alt.Scale(
            domain=["absolute_anomaly", "may_anomaly", "no_anomaly"],
            range=["#e74c3c", "#f39c12", "#2ecc71"],
        ),
    )

    degree_opacity = alt.when(degree_selection).then(alt.value(0.7)).otherwise(alt.value(0.1))

    tooltip_fields = ["degree", "name", "count"]

    if "chaos_type" in df.columns:
        tooltip_fields.append("chaos_type:N")

    bar = (
        base.mark_bar(stroke="navy", strokeWidth=1, opacity=0.7)
        .encode(
            x=alt.X(
                "name",
                title=meta.x_label,
                axis=alt.Axis(labelAngle=-45),
                sort="-y",
            ),
            y=alt.Y("count", title="Injection Count"),
            color=degree_color,
            opacity=degree_opacity,
            xOffset="degree",
            tooltip=tooltip_fields,
        )
        .properties(width=850, height=300, title=meta.title)
        .add_params(degree_selection)
    )

    return bar


def plot_heatmap(meta: HeatmapMeta) -> None:
    pass


class VisDatapacks:
    BAR_DATA_KEY = ["degree", "name", "count"]

    def __init__(
        self,
        config: VisInjectionsConfig,
        distributions_dict: dict[str, dict[str, Any]],
        metrics: list[str],
    ):
        self.config = config
        self.distributions_dict = distributions_dict
        self.metrics = metrics

    def display_bar(self, bar_data: list[dict[str, Any]], key: str) -> alt.Chart | None:
        """
        Display bar charts for faults, services distribution.
        """

        for item in bar_data:
            if not all(k in item for k in self.BAR_DATA_KEY):
                logger.error(f"Bar data item {item} is missing required keys: {self.BAR_DATA_KEY}")
                return None

        if key not in self.config.bar_configs:
            logger.error(f"Key {key} not found in bar_configs")
            return None

        bar_config = self.config.bar_configs[key]

        return plot_bar(
            BarMeta(
                data=bar_data,
                x_label=bar_config.x_label,
                y_label="Injection Count",
                title=bar_config.title,
            )
        )

    def get_bar_data(self, key: str) -> list[dict[str, Any]]:
        combined_data = []

        for degree, distributions in self.distributions_dict.items():
            data: dict[str, int] = distributions.get(key, {})
            if not data:
                continue

            for name, count in data.items():
                combined_data.append(
                    {
                        "degree": degree,
                        "name": name,
                        "count": count,
                    }
                )

        return combined_data
