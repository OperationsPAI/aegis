import os

from rcabench.client import RCABenchClient
from rcabench.openapi import ApiClient

from ...sdk.config import get_config


def get_rcabench_client(*, base_url: str | None = None) -> ApiClient:
    if base_url is None:
        base_url = os.getenv("RCABENCH_BASE_URL") or get_config().base_url

    token = os.getenv("RCABENCH_TOKEN")
    return RCABenchClient(base_url=base_url, token=token).get_client()
