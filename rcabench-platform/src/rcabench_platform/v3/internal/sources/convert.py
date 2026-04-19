import functools
import re
import shutil
import sys
import tempfile
from abc import ABC, abstractmethod
from pathlib import Path
from typing import Any

import polars as pl
from tqdm.auto import tqdm

from ...sdk.config import get_config
from ...sdk.datasets.spec import (
    Label,
    get_datapack_folder,
    get_dataset_folder,
    get_dataset_index_path,
    get_dataset_labels_path,
    read_dataset_labels,
)
from ...sdk.logging import logger, timeit
from ...sdk.utils.display import human_byte_size
from ...sdk.utils.fmap import fmap_processpool
from ...sdk.utils.fs import running_mark
from ...sdk.utils.serde import save_csv, save_json, save_parquet, save_txt


class DatapackLoader(ABC):
    @property
    @abstractmethod
    def name(self) -> str: ...

    @abstractmethod
    def labels(self) -> list[Label]: ...

    @abstractmethod
    def data(self) -> dict[str, Any]: ...

    def validate_datapack(self) -> None:
        if not re.match(r"^[A-Za-z0-9_-]+$", self.name):
            raise ValueError(f"Invalid datapack name: {self.name}")


class DatasetLoader(ABC):
    @abstractmethod
    def name(self) -> str: ...

    @abstractmethod
    def __len__(self) -> int: ...

    @abstractmethod
    def __getitem__(self, index: int) -> DatapackLoader: ...


@timeit()
def convert_dataset(
    loader: DatasetLoader,
    root: Path | None = None,
    *,
    skip_finished: bool = True,
    parallel: int = 4,
    ignore_exceptions: bool = False,
) -> None:
    if root is None:
        root = get_config().data

    dataset = loader.name()
    validate_dataset_name(dataset)

    data_folder = root / "data" / dataset
    for i in range(len(loader)):
        functools.partial(_convert_datapack, loader, i, data_folder, skip_finished)
    tasks = [functools.partial(_convert_datapack, loader, i, data_folder, skip_finished) for i in range(len(loader))]

    results = fmap_processpool(
        tasks,
        parallel=parallel,
        ignore_exceptions=ignore_exceptions,
    )

    index_rows = []
    labels_rows = []
    for datapack, labels in results:
        index = {"dataset": dataset, "datapack": datapack}
        index_rows.append(index)
        for label in labels:
            labels_rows.append({**index, "gt.level": label.level, "gt.name": label.name})

    index_df = pl.DataFrame(index_rows).sort(by=pl.all())
    labels_df = pl.DataFrame(labels_rows).sort(by=pl.all())

    meta_folder = root / "meta" / dataset
    save_parquet(index_df, path=meta_folder / "index.parquet")
    save_parquet(labels_df, path=meta_folder / "labels.parquet")


def _convert_datapack(
    loader: DatasetLoader,
    index: int,
    data_folder: Path,
    skip_finished: bool,
) -> tuple[str, list[Label]]:
    datapack = loader[index]
    dst_folder = data_folder / datapack.name
    return convert_datapack(datapack, dst_folder, skip_finished=skip_finished)


@timeit()
def convert_datapack(
    loader: DatapackLoader,
    dst_folder: Path,
    *,
    skip_finished: bool = True,
) -> tuple[str, list[Label]]:
    datapack = loader.name

    labels = loader.labels()
    validate_datapack_labels(labels)

    needs_skip = False
    if skip_finished:
        finished = dst_folder / ".finished"
        if finished.exists():
            needs_skip = True

    if not needs_skip:
        with running_mark(dst_folder):
            with tempfile.TemporaryDirectory() as tempdir:
                tempdir = Path(tempdir)

                data = loader.data()
                keys = list(data.keys())

                for i, k in enumerate(keys, start=1):
                    save_data_file(tempdir, k, data[k])
                    del data[k]

                    size = (tempdir / k).stat().st_size
                    logger.debug(f"saved data [{i}/{len(keys)}] {loader.name}/{k} size={human_byte_size(size)}")

                move_files(tempdir, dst_folder)

        (dst_folder / ".finished").touch()

    return datapack, labels


@timeit()
def move_files(src: Path, dst: Path) -> None:
    for file in src.iterdir():
        shutil.move(file, dst / file.name)


@timeit(log_args={"dst_folder", "name"})
def save_data_file(dst_folder: Path, name: str, value: Any) -> None:
    file_path = dst_folder / name
    ext = file_path.suffix
    stem = file_path.stem

    if stem.endswith("traces"):
        validate_traces(value)
    elif stem.endswith("metrics"):
        validate_metrics(value)
    elif stem.endswith("logs"):
        validate_logs(value)

    sys.stdout.flush()

    if isinstance(value, Path):
        assert value.exists()
        shutil.copyfile(value, file_path)

    elif ext == ".parquet":
        save_parquet(value, path=file_path)

    elif ext == ".csv":
        save_csv(value, path=file_path)

    elif ext == ".txt":
        save_txt(value, path=file_path)

    elif ext == ".json":
        save_json(value, path=file_path)

    else:
        raise NotImplementedError(f"Unsupported file type: {ext}")


def validate_dataset_name(name: str) -> None:
    if not name:
        raise ValueError("Dataset name cannot be empty")
    if not re.match(r"^[A-Za-z0-9_-]+$", name):
        raise ValueError(f"Invalid dataset name: {name}")


def validate_datapack_labels(labels: list[Label]) -> None:
    if not labels:
        raise ValueError("Labels cannot be empty")

    for label in labels:
        if not label.name or not label.level:
            raise ValueError(f"Label must have a name and level: {label}")


def validate_traces(value: Any):
    if isinstance(value, (pl.LazyFrame, pl.DataFrame)):
        df = value

        required = {
            "time": pl.Datetime,
            "trace_id": pl.String,
            "span_id": pl.String,
            "parent_span_id": pl.String,
            "service_name": pl.String,
            "span_name": pl.String,
            "duration": pl.UInt64,
        }

        validate_by_model(df, required, extra_prefix="attr.")
    else:
        raise NotImplementedError


def validate_metrics(value: Any):
    if isinstance(value, (pl.LazyFrame, pl.DataFrame)):
        df = value

        required = {
            "time": pl.Datetime,
            "metric": pl.String,
            "value": pl.Float64,
            "service_name": pl.String,
        }

        validate_by_model(df, required, extra_prefix="attr.")
    else:
        raise NotImplementedError


def validate_logs(value: Any):
    if isinstance(value, (pl.LazyFrame, pl.DataFrame)):
        df = value

        required = {
            "time": pl.Datetime,
            "trace_id": pl.String,
            "span_id": pl.String,
            "service_name": pl.String,
            "level": pl.String,
            "message": pl.String,
        }

        validate_by_model(df, required, extra_prefix="attr.")

    else:
        raise NotImplementedError


def validate_by_model(df: pl.LazyFrame | pl.DataFrame, model: dict[str, pl.DataType], extra_prefix: str):
    if isinstance(df, pl.LazyFrame):
        schema = df.collect_schema()
    elif isinstance(df, pl.DataFrame):
        schema = df.schema
    else:
        raise TypeError(f"Unsupported type: {type(df)}")

    for name, dtype in model.items():
        if name not in schema:
            raise ValueError(f"Missing required column: {name} {dtype}")
        if schema[name] != dtype:
            raise ValueError(f"Column {name} has incorrect type: {schema[name]}")

    for name, dtype in schema.items():
        if name not in model:
            if not name.startswith(extra_prefix):
                raise ValueError(f"Unexpected column: {name} {dtype}")


@timeit(log_args={"src_dataset", "dst_dataset"})
def link_subset(src_dataset: str, dst_dataset: str, datapacks: list[str]):
    src_dataset_folder = get_dataset_folder(src_dataset)
    dst_dataset_folder = get_dataset_folder(dst_dataset)

    assert src_dataset_folder.exists(), f"Source dataset folder {src_dataset_folder} does not exist"
    dst_dataset_folder.mkdir(parents=True, exist_ok=True)

    for datapack in tqdm(datapacks, desc=link_subset.__name__):
        src_path = get_datapack_folder(src_dataset, datapack)
        dst_path = get_datapack_folder(dst_dataset, datapack)

        assert src_path.exists(), f"Source datapack {src_path} does not exist"
        dst_path.unlink(missing_ok=True)

        dst_path.symlink_to(Path("..") / src_dataset / datapack, target_is_directory=True)

        assert dst_path.resolve() == src_path.resolve()

    index_df = pl.DataFrame({"dataset": dst_dataset, "datapack": datapacks})

    labels_df = (
        read_dataset_labels(src_dataset)
        .join(index_df.select("datapack"), on="datapack", how="inner")
        .with_columns(pl.lit(dst_dataset).alias("dataset"))
    )

    save_parquet(index_df, path=get_dataset_index_path(dst_dataset))
    save_parquet(labels_df, path=get_dataset_labels_path(dst_dataset))
