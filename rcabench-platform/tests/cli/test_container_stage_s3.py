"""Regression: ``rca container run`` must stage s3:// INPUT_PATH locally.

Production bug: orchestrator passes ``INPUT_PATH=s3://aegis-datapack/<name>``
and the algorithm container crashed with ``AssertionError: input_path:
s3:/aegis-datapack/...`` because typer coerced the URL to ``Path``, collapsing
``s3://`` to ``s3:/``, and ``Path.is_dir()`` then returned False.
"""

from __future__ import annotations

import pytest

from rcabench_platform.v3.cli.container import _stage_s3_input_locally

try:
    import boto3  # noqa: F401
    from moto.server import ThreadedMotoServer

    _HAS_MOTO = True
except ImportError:  # pragma: no cover
    _HAS_MOTO = False
    ThreadedMotoServer = None  # type: ignore[assignment,misc]


BUCKET = "aegis-datapack"


@pytest.fixture
def s3_bucket(monkeypatch):
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

        import s3fs

        s3fs.S3FileSystem.clear_instance_cache()

        import boto3 as _boto3

        client = _boto3.client("s3", region_name="us-east-1", endpoint_url=endpoint)
        client.create_bucket(Bucket=BUCKET)
        yield client
        s3fs.S3FileSystem.clear_instance_cache()
    finally:
        server.stop()


def test_stage_s3_input_locally_mirrors_prefix(s3_bucket):
    datapack = "ts0-cpu-frontend-1700000000"
    s3_bucket.put_object(Bucket=BUCKET, Key=f"{datapack}/injection.json", Body=b'{"name":"x"}')
    s3_bucket.put_object(Bucket=BUCKET, Key=f"{datapack}/trace.parquet", Body=b"PAR1payload")

    local_dir = _stage_s3_input_locally(f"s3://{BUCKET}/{datapack}")

    assert local_dir.is_dir(), f"expected a local directory, got {local_dir}"
    assert (local_dir / "injection.json").is_file()
    assert (local_dir / "trace.parquet").is_file()
    assert (local_dir / "injection.json").read_bytes() == b'{"name":"x"}'
