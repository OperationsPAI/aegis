import dataclasses
import datetime
import json
import os
import pickle
from pathlib import Path
from typing import Any

import pandas as pd
import polars as pl

from ..logging import logger


def _is_s3(path: str | Path) -> bool:
    """Return True iff ``path`` is an S3 URL (``s3://...``)."""
    return isinstance(path, str) and path.startswith("s3://")


def _s3_storage_options() -> dict[str, Any] | None:
    """Return s3fs storage_options derived from env.

    We rely on s3fs's native env detection wherever possible
    (``AWS_ACCESS_KEY_ID``, ``AWS_SECRET_ACCESS_KEY``, ``AWS_REGION``).
    The one rustfs/MinIO-specific knob is ``AWS_ENDPOINT_URL_S3`` (or its
    boto3 equivalent ``AWS_ENDPOINT_URL``) which we forward explicitly as
    ``client_kwargs.endpoint_url`` so the default-endpoint behavior of s3fs
    works against on-prem object stores. Returns ``None`` when nothing
    needs overriding so callers can drop the kwarg entirely.
    """
    endpoint = os.environ.get("AWS_ENDPOINT_URL_S3") or os.environ.get("AWS_ENDPOINT_URL")
    if not endpoint:
        return None
    return {"client_kwargs": {"endpoint_url": endpoint}}


def json_default(obj):
    if isinstance(obj, (set, frozenset)):
        return list(obj)
    elif isinstance(obj, Path):
        return str(obj)
    elif isinstance(obj, datetime.datetime):
        return obj.isoformat()
    elif hasattr(obj, "model_dump"):
        return obj.model_dump()
    try:
        import numpy as np

        if isinstance(obj, np.ndarray):
            return obj.tolist()
        if isinstance(obj, np.integer):
            return int(obj)
        if isinstance(obj, np.floating):
            return float(obj)
    except ImportError:
        pass
    raise TypeError(f"Object of type {obj.__class__.__name__} is not JSON serializable")


def load_json(*, path: str | Path) -> Any:
    logger.opt(colors=True).debug(f"loading json from <green>{path}</green>")
    with open(path) as f:
        return json.loads(f.read())


def save_json(obj: Any, *, path: str | Path) -> None:
    if hasattr(obj, "__dataclass_fields__"):
        obj = dataclasses.asdict(obj)

    if _is_s3(path):
        assert str(path).endswith(".json")
        import fsspec  # lazy: fsspec is a pandas transitive dep, s3fs is optional

        storage_options = _s3_storage_options() or {}
        with fsspec.open(str(path), "w", **storage_options) as f:
            json.dump(obj, f, ensure_ascii=False, indent=4, default=json_default)  # type: ignore[arg-type]
        logger.opt(colors=True).debug(f"saved json to <green>{path}</green>")
        return

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
    if _is_s3(path):
        assert str(path).endswith(".parquet")
        storage_options = _s3_storage_options()
        if isinstance(df, pl.LazyFrame):
            len_df = "?"
            df = df.collect()
        if isinstance(df, pl.DataFrame):
            # polars doesn't dispatch on fsspec; route via pandas which does.
            len_df = len(df)
            df.to_pandas().to_parquet(str(path), index=False, storage_options=storage_options)
        elif isinstance(df, pd.DataFrame):
            len_df = len(df)
            df.to_parquet(str(path), index=False, storage_options=storage_options)
        else:
            raise TypeError(f"Unsupported type: {type(df)}")
        logger.opt(colors=True).debug(f"saved parquet (len(df)={len_df}) to <green>{path}</green>")
        return

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
    if _is_s3(path):
        assert str(path).endswith(".csv")
        storage_options = _s3_storage_options()
        if isinstance(df, pl.LazyFrame):
            len_df = "?"
            df = df.collect()
        if isinstance(df, pl.DataFrame):
            len_df = len(df)
            df.to_pandas().to_csv(str(path), index=False, storage_options=storage_options)
        elif isinstance(df, pd.DataFrame):
            len_df = len(df)
            df.to_csv(str(path), index=False, storage_options=storage_options)
        else:
            raise TypeError(f"Unsupported type: {type(df)}")
        logger.opt(colors=True).debug(f"saved csv (len(df)={len_df}) to <green>{path}</green>")
        return

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
