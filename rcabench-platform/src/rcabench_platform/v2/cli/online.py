import os
from pathlib import Path

import polars as pl
import typer
from rcabench.openapi import (
    ContainersApi,
    ContainerType,
    DatasetsApi,
    InjectionsApi,
    SearchInjectionReq,
    TracesApi,
)

from ..clients.k8s import download_kube_info
from ..clients.rcabench_ import get_rcabench_client
from ..config import get_config
from ..logging import logger, timeit
from ..utils.dataframe import print_dataframe
from ..utils.serde import save_json

PROTJECT_ROOT = Path(__file__).parent.parent.parent.parent.parent

app = typer.Typer(pretty_exceptions_enable=False)


@app.command()
@timeit()
def show_kube_info(namespace: str = "ts1", save_path: Path | None = None):
    kube_info = download_kube_info(ns=namespace)

    if save_path is None:
        config = get_config()
        save_path = config.temp / "kube_info.json"

    ans = kube_info.to_dict()
    save_json(ans, path=save_path)

    # Convert dict to DataFrame for display
    df = pl.DataFrame([ans])
    print_dataframe(df)


@app.command()
@timeit()
def query_injection(name: str, base_url: str | None = None):
    client = get_rcabench_client(base_url=base_url)
    api = InjectionsApi(client)
    resp = api.search_injections(
        search=SearchInjectionReq(
            name_pattern=name,
        )
    )
    assert resp.code is not None and resp.code < 300 and resp.data is not None
    assert resp.data.items is not None

    ans = [item.model_dump() for item in resp.data.items]
    df = pl.DataFrame(ans)
    print_dataframe(df)


@app.command()
@timeit()
def list_injections(base_url: str | None = None):
    client = get_rcabench_client(base_url=base_url)
    api = InjectionsApi(client)
    resp = api.list_injections()
    assert resp.code is not None and resp.code < 300 and resp.data is not None and resp.data.items is not None

    ans = [item.model_dump() for item in resp.data.items]
    df = pl.DataFrame(ans)
    print_dataframe(df)


@app.command()
@timeit()
def list_datasets(base_url: str | None = None):
    client = get_rcabench_client(base_url=base_url)
    api = DatasetsApi(client)
    resp = api.list_datasets()
    assert resp.code is not None and resp.code < 300 and resp.data is not None and resp.data.items is not None

    ans = [item.model_dump() for item in resp.data.items]
    df = pl.DataFrame(ans)
    print_dataframe(df)


@app.command()
@timeit()
def get_dataset(id: int, base_url: str | None = None):
    client = get_rcabench_client(base_url=base_url)
    api = DatasetsApi(client)
    resp = api.get_dataset_by_id(dataset_id=id)
    assert resp.code is not None and resp.code < 300 and resp.data is not None

    # Return dataset versions if available
    if resp.data.versions:
        return [f"{resp.data.name}@{v.name}" for v in resp.data.versions]
    return []


@app.command()
@timeit()
def list_algorithms(base_url: str | None = None):
    client = get_rcabench_client(base_url=base_url)
    api = ContainersApi(client)
    resp = api.list_containers(type=ContainerType.Algorithm)
    assert resp.code is not None and resp.code < 300 and resp.data is not None and resp.data.items is not None

    ans = [item.model_dump() for item in resp.data.items]
    df = pl.DataFrame(ans)
    print_dataframe(df)


@app.command()
def trace(trace_id: str, base_url: str | None = None, timeout: int = 600):
    base_url = base_url or os.getenv("RCABENCH_BASE_URL")
    assert base_url is not None, "base_url or RCABENCH_BASE_URL is not set"

    client = get_rcabench_client(base_url=base_url)
    api = TracesApi(client)
    sse_client = api.get_trace_events(trace_id=trace_id)
    assert sse_client is not None, "Failed to get SSE client for trace events"

    sse_client.events()
    for event in sse_client.events():
        logger.info("Event: %s", event)


def main():
    app()
