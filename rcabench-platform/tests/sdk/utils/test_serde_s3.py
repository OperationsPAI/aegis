"""Tests for s3:// support in rcabench_platform.v3.sdk.utils.serde.

Uses moto's mock_aws decorator to stand up an in-process S3 backend. If moto
is not installed we skip the s3 suite; the local-path regression test still
runs because it has no extra deps.
"""

from __future__ import annotations

import json
from pathlib import Path

import pandas as pd
import pytest

from rcabench_platform.v3.sdk.utils.serde import save_csv, save_json, save_parquet

try:
    import boto3  # noqa: F401
    from moto.server import ThreadedMotoServer

    _HAS_MOTO = True
except ImportError:  # pragma: no cover - environment guard
    _HAS_MOTO = False
    ThreadedMotoServer = None  # type: ignore[assignment,misc]


BUCKET = "test-bucket"


@pytest.fixture
def s3_bucket(monkeypatch):
    """Spin up an in-process moto S3 server and wire env so s3fs can reach it.

    s3fs uses aiobotocore, which the in-process ``mock_aws`` decorator cannot
    intercept on every moto/aiobotocore release. Running moto as a real HTTP
    server and pointing s3fs at it via ``AWS_ENDPOINT_URL_S3`` is the robust
    option and also exercises the rustfs-style endpoint override codepath.
    """
    if not _HAS_MOTO or ThreadedMotoServer is None:
        pytest.skip("moto not installed")

    server = ThreadedMotoServer(port=0)
    server.start()
    try:
        host, port = server.get_host_and_port()
        endpoint = f"http://{host}:{port}"

        monkeypatch.setenv("AWS_ACCESS_KEY_ID", "test")
        monkeypatch.setenv("AWS_SECRET_ACCESS_KEY", "test")
        monkeypatch.setenv("AWS_DEFAULT_REGION", "us-east-1")
        monkeypatch.setenv("AWS_ENDPOINT_URL_S3", endpoint)
        # Clear any s3fs-cached instance pinned to a previous endpoint.
        import s3fs

        s3fs.S3FileSystem.clear_instance_cache()

        import boto3 as _boto3

        client = _boto3.client("s3", region_name="us-east-1", endpoint_url=endpoint)
        client.create_bucket(Bucket=BUCKET)
        yield client
        s3fs.S3FileSystem.clear_instance_cache()
    finally:
        server.stop()


def test_save_json_s3_writes_bytes(s3_bucket):
    key = "path/sample.json"
    save_json({"hello": "world", "n": 1}, path=f"s3://{BUCKET}/{key}")

    obj = s3_bucket.get_object(Bucket=BUCKET, Key=key)
    body = json.loads(obj["Body"].read())
    assert body == {"hello": "world", "n": 1}


def test_save_parquet_s3_writes_bytes(s3_bucket):
    key = "path/sample.parquet"
    df = pd.DataFrame({"a": [1, 2], "b": ["x", "y"]})
    save_parquet(df, path=f"s3://{BUCKET}/{key}")

    obj = s3_bucket.get_object(Bucket=BUCKET, Key=key)
    raw = obj["Body"].read()
    assert raw.startswith(b"PAR1"), "parquet magic bytes missing"


def test_save_csv_s3_writes_bytes(s3_bucket):
    key = "path/sample.csv"
    df = pd.DataFrame({"a": [1, 2], "b": ["x", "y"]})
    save_csv(df, path=f"s3://{BUCKET}/{key}")

    obj = s3_bucket.get_object(Bucket=BUCKET, Key=key)
    text = obj["Body"].read().decode("utf-8")
    assert text.splitlines()[0] == "a,b"
    assert "1,x" in text and "2,y" in text


# --- regression: local-path behavior must be unchanged --------------------


def test_save_json_local_path_unchanged(tmp_path: Path):
    out = tmp_path / "nested" / "sample.json"
    save_json({"k": [1, 2, 3]}, path=out)
    assert out.exists()
    assert json.loads(out.read_text()) == {"k": [1, 2, 3]}


def test_save_parquet_local_path_unchanged(tmp_path: Path):
    out = tmp_path / "sample.parquet"
    df = pd.DataFrame({"a": [1, 2]})
    save_parquet(df, path=out)
    assert out.exists()
    assert pd.read_parquet(out)["a"].tolist() == [1, 2]
