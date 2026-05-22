#!/usr/bin/env -S uv run --extra sdk --extra internal -s
from __future__ import annotations

from pathlib import Path
from typing import Any

import polars as pl

from rcabench_platform.v3.cli.main import app, logger, timeit
from rcabench_platform.v3.internal.sources.convert import DatapackLoader, DatasetLoader, convert_datapack, convert_dataset
from rcabench_platform.v3.internal.sources.rcabench import attach_log_template_columns
from rcabench_platform.v3.sdk.datasets.spec import Label
from rcabench_platform.v3.sdk.utils.serde import load_json


def scan_fse_datapacks(src_root: Path) -> list[str]:
    datapacks: list[str] = []
    skipped_invalid = 0
    skipped_missing = 0

    for path in sorted(src_root.iterdir()):
        if not path.is_dir():
            continue

        converted_dir = path / "converted"
        if not converted_dir.is_dir():
            skipped_missing += 1
            continue

        if (converted_dir / ".invalid").exists():
            skipped_invalid += 1
            continue

        if not (converted_dir / ".finished").exists():
            skipped_missing += 1
            continue

        required_files = [
            converted_dir / "env.json",
            converted_dir / "injection.json",
            converted_dir / "normal_logs.parquet",
            converted_dir / "abnormal_logs.parquet",
            converted_dir / "normal_traces.parquet",
            converted_dir / "abnormal_traces.parquet",
            converted_dir / "normal_metrics.parquet",
            converted_dir / "abnormal_metrics.parquet",
        ]
        if not all(file.exists() for file in required_files):
            skipped_missing += 1
            continue

        datapacks.append(path.name)

    logger.info(
        "FSE scan complete: valid={} skipped_invalid={} skipped_incomplete={}",
        len(datapacks),
        skipped_invalid,
        skipped_missing,
    )
    return datapacks


class FseDatapackLoader(DatapackLoader):
    def __init__(
        self,
        src_folder: Path,
        datapack: str,
        template_root: Path,
    ) -> None:
        self._src_folder = src_folder
        self._converted_folder = src_folder / "converted"
        self._datapack = datapack
        self._template_root = template_root

        self.validate_datapack()

    @property
    def name(self) -> str:
        return self._datapack

    def labels(self) -> list[Label]:
        injection: dict[str, Any] = load_json(path=self._converted_folder / "injection.json")
        raw_ground_truth = injection.get("ground_truth")

        if isinstance(raw_ground_truth, dict):
            ground_truths = [raw_ground_truth]
        elif isinstance(raw_ground_truth, list):
            ground_truths = [gt for gt in raw_ground_truth if isinstance(gt, dict)]
        else:
            ground_truths = []

        labels: list[Label] = []
        seen: set[tuple[str, str]] = set()
        for ground_truth in ground_truths:
            for level, names in ground_truth.items():
                if level == "additional_properties" or not names:
                    continue

                if isinstance(names, str):
                    values = [names]
                elif isinstance(names, list):
                    values = [str(name) for name in names if name]
                else:
                    continue

                for name in values:
                    key = (level, name)
                    if key in seen:
                        continue
                    seen.add(key)
                    labels.append(Label(level=level, name=name))

        if not labels:
            raise ValueError(f"No usable ground truth labels found for {self._datapack}")

        return labels

    def _logs(self, src: Path) -> pl.DataFrame:
        df = pl.read_parquet(src)
        if "service_name" in df.columns:
            df = df.filter(pl.col("service_name") != "ts-ui-dashboard")

        df = attach_log_template_columns(
            df,
            config_path=self._template_root / "drain_ts.ini",
            persistence_path=self._template_root / "drain_ts.bin",
        )
        return df.sort("time")

    def data(self) -> dict[str, Any]:
        data: dict[str, Any] = {
            "env.json": self._converted_folder / "env.json",
            "injection.json": self._converted_folder / "injection.json",
        }

        for optional_name in ["conclusion.parquet", "causal_graph.json"]:
            optional_path = self._converted_folder / optional_name
            if optional_path.exists():
                data[optional_name] = pl.scan_parquet(optional_path) if optional_name.endswith(".parquet") else optional_path

        fault_info_path = self._src_folder / "fault_info.json"
        if fault_info_path.exists():
            data["fault_info.json"] = fault_info_path

        passthrough_names = [
            "normal_traces.parquet",
            "abnormal_traces.parquet",
            "normal_metrics.parquet",
            "abnormal_metrics.parquet",
            "normal_metrics_sum.parquet",
            "abnormal_metrics_sum.parquet",
            "normal_metrics_histogram.parquet",
            "abnormal_metrics_histogram.parquet",
        ]
        for name in passthrough_names:
            path = self._converted_folder / name
            if path.exists():
                data[name] = pl.scan_parquet(path)

        for name in ["normal_logs.parquet", "abnormal_logs.parquet"]:
            path = self._converted_folder / name
            if path.exists():
                data[name] = self._logs(path)

        return data


class FseDatasetLoader(DatasetLoader):
    def __init__(self, src_root: Path, dataset: str, template_root: Path) -> None:
        self._src_root = src_root
        self._dataset = dataset
        self._template_root = template_root
        self._datapacks = scan_fse_datapacks(src_root)

    def name(self) -> str:
        return self._dataset

    def __len__(self) -> int:
        return len(self._datapacks)

    def __getitem__(self, index: int) -> FseDatapackLoader:
        datapack = self._datapacks[index]
        return FseDatapackLoader(
            src_folder=self._src_root / datapack,
            datapack=datapack,
            template_root=self._template_root,
        )


@app.command()
@timeit()
def run(
    src_root: Path = Path("/mnt/jfs/fse"),
    template_root: Path = Path("/mnt/jfs/drain_template"),
    dataset: str = "fse",
    skip_finished: bool = True,
    parallel: int = 4,
    ignore_exceptions: bool = True,
):
    loader = FseDatasetLoader(src_root=src_root, dataset=dataset, template_root=template_root)
    convert_dataset(
        loader,
        skip_finished=skip_finished,
        parallel=parallel,
        ignore_exceptions=ignore_exceptions,
    )


@app.command()
@timeit()
def local_test_1(
    datapack: str = "ts0-mysql-container-kill-9t6n24",
    src_root: Path = Path("/mnt/jfs/fse"),
    template_root: Path = Path("/mnt/jfs/drain_template"),
):
    loader = FseDatapackLoader(
        src_folder=src_root / datapack,
        datapack=datapack,
        template_root=template_root,
    )
    convert_datapack(
        loader,
        dst_folder=Path("temp") / "fse" / datapack,
        skip_finished=False,
    )


if __name__ == "__main__":
    app()
