"""Regression: detector platform_convert must persist converted/ to s3.

Commit 0b0ea6b4 moved the converted output under ``input_path / "converted"``
so the in-process RCA loader picks it up via its short-circuit -- correct -- but
in byte-cluster s3 mode ``input_path`` is an atexit-deleted tempdir staged from
``INPUT_PATH=s3://.../<name>``, so converted/ vanished at process exit and never
reached the datapack blob. The fix additionally uploads converted/ back to
``<input_url>/converted/``. This exercises that upload (including the 0-byte
``.finished`` marker NUL stub) end-to-end against a moto bucket.
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

# cli/ is a script dir, not an installed package; tests/ rootdir doesn't have
# the repo root on sys.path, so add it before importing the detector helper.
sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from cli.detector import _upload_dir_to_s3  # type: ignore[import-not-found]  # noqa: E402

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


def test_upload_dir_to_s3_persists_converted_files(s3_bucket, tmp_path):
    datapack = "ts0-cpu-frontend-1700000000"
    converted = tmp_path / "converted"
    converted.mkdir()
    (converted / "trace.parquet").write_bytes(b"PAR1payload")
    (converted / "injection.json").write_bytes(b'{"name":"x"}')
    (converted / ".finished").touch()  # 0-byte marker -> NUL stub

    n = _upload_dir_to_s3(converted, f"s3://{BUCKET}/{datapack}/converted")
    assert n == 3

    listed = s3_bucket.list_objects_v2(Bucket=BUCKET, Prefix=f"{datapack}/converted/")
    keys = {obj["Key"] for obj in listed.get("Contents", [])}
    assert keys == {
        f"{datapack}/converted/trace.parquet",
        f"{datapack}/converted/injection.json",
        f"{datapack}/converted/.finished",
    }

    body = s3_bucket.get_object(Bucket=BUCKET, Key=f"{datapack}/converted/trace.parquet")["Body"].read()
    assert body == b"PAR1payload"

    marker = s3_bucket.get_object(Bucket=BUCKET, Key=f"{datapack}/converted/.finished")["Body"].read()
    assert marker == b"\x00"
