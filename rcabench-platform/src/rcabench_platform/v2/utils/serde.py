import dataclasses
import datetime
import json
import pickle
from pathlib import Path
from typing import Any

import pandas as pd
import polars as pl

from ..logging import logger


def json_default(obj):
    if isinstance(obj, set):
        return list(obj)
    elif isinstance(obj, Path):
        return str(obj)
    elif isinstance(obj, datetime.datetime):
        return obj.isoformat()
    raise TypeError(f"Object of type {obj.__class__.__name__} is not JSON serializable")


def load_json(*, path: str | Path) -> Any:
    logger.opt(colors=True).debug(f"loading json from <green>{path}</green>")
    with open(path) as f:
        return json.loads(f.read())


def save_json(obj: Any, *, path: str | Path) -> None:
    if hasattr(obj, "__dataclass_fields__"):
        obj = dataclasses.asdict(obj)

    file_path = Path(path)
    assert file_path.suffix == ".json"
    file_path.parent.mkdir(parents=True, exist_ok=True)

    with open(path, "w") as f:
        json.dump(obj, f, ensure_ascii=False, indent=4, default=json_default)

    logger.opt(colors=True).debug(f"saved json to <green>{file_path}</green>")


def load_pickle(*, path: str | Path) -> Any:
    logger.opt(colors=True).debug(f"loading pickle from <green>{path}</green>")
    with open(path, "rb") as f:
        return pickle.load(f)


def save_pickle(obj, *, path: str | Path) -> None:
    file_path = Path(path)
    assert file_path.suffix == ".pkl"
    file_path.parent.mkdir(parents=True, exist_ok=True)

    with open(path, "wb") as f:
        pickle.dump(obj, f, pickle.HIGHEST_PROTOCOL)

    logger.opt(colors=True).debug(f"saved pickle to <green>{file_path}</green>")


def save_txt(content: str, *, path: str | Path) -> None:
    file_path = Path(path)
    assert file_path.suffix == ".txt"
    file_path.parent.mkdir(parents=True, exist_ok=True)

    with open(path, "w") as f:
        f.write(content)

    logger.opt(colors=True).debug(f"saved txt to <green>{file_path}</green>")


def save_parquet(df: pl.LazyFrame | pl.DataFrame | pd.DataFrame, *, path: str | Path) -> None:
    file_path = Path(path)
    assert file_path.suffix == ".parquet"
    file_path.parent.mkdir(parents=True, exist_ok=True)

    if isinstance(df, pl.LazyFrame):
        len_df = "?"
        df.sink_parquet(file_path)
    elif isinstance(df, pl.DataFrame):
        len_df = len(df)
        df.write_parquet(file_path)
    elif isinstance(df, pd.DataFrame):
        len_df = len(df)
        df.to_parquet(file_path, index=False)
    else:
        raise TypeError(f"Unsupported type: {type(df)}")

    logger.opt(colors=True).debug(f"saved parquet (len(df)={len_df}) to <green>{file_path}</green>")


def save_csv(df: pl.LazyFrame | pl.DataFrame | pd.DataFrame, *, path: str | Path) -> None:
    file_path = Path(path)
    assert file_path.suffix == ".csv"
    file_path.parent.mkdir(parents=True, exist_ok=True)

    if isinstance(df, pl.LazyFrame):
        len_df = "?"
        df.sink_csv(file_path)
    elif isinstance(df, pl.DataFrame):
        len_df = len(df)
        df.write_csv(file_path)
    elif isinstance(df, pd.DataFrame):
        len_df = len(df)
        df.to_csv(file_path, index=False)
    else:
        raise TypeError(f"Unsupported type: {type(df)}")

    logger.opt(colors=True).debug(f"saved csv (len(df)={len_df}) to <green>{file_path}</green>")
