from pathlib import Path

import polars as pl
import typer

from ..logging import logger, timeit
from ..utils.dataframe import print_dataframe

app = typer.Typer()


@app.command()
@timeit()
def parquet_head(path: Path, n: int = 5):
    df = pl.scan_parquet(path).head(n).collect()
    print_dataframe(df)
