import os
from pathlib import Path
from typing import TypeAlias

import clickhouse_connect
import clickhouse_connect.driver.client

ClickHouseClient: TypeAlias = clickhouse_connect.driver.client.Client


def get_clickhouse_client() -> ClickHouseClient:
    host = os.environ.get("DB_HOST", "localhost")
    port = int(os.environ.get("DB_PORT", "8123"))
    username = os.environ.get("DB_USER", None)
    password = os.environ.get("DB_PASSWORD", "")
    database = os.environ.get("DB_DATABASE", "default")

    client = clickhouse_connect.get_client(
        host=host,
        username=username,
        password=password,
        database=database,
        port=port,
    )

    return client


def query_parquet_stream(client: ClickHouseClient, query: str, save_path: Path):
    assert save_path.suffix == ".parquet", "save_path must be a parquet file"
    assert save_path.parent.is_dir(), "save_path parent must be a directory"

    stream = client.raw_stream(query=query, fmt="Parquet")
    with open(save_path, "wb") as f:
        for chunk in stream:
            f.write(chunk)
        f.flush()
