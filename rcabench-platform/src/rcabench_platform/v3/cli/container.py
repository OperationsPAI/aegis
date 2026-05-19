import atexit
import os
import shutil
import tempfile
from datetime import datetime
from pathlib import Path
from typing import Annotated, Any

import pandas as pd
import typer
from rcabench.openapi import (
    ExecutionsApi,
    GranularityResultItem,
    UploadGranularityResultReq,
)

from ..internal.clients.rcabench_ import get_rcabench_client
from ..internal.sources.convert import convert_datapack
from ..internal.sources.rcabench import RCABenchDatapackLoader
from ..sdk.algorithms.spec import AlgorithmArgs, global_algorithm_registry
from ..sdk.config import get_config
from ..sdk.logging import logger, timeit
from ..sdk.utils.serde import load_json, save_csv

app = typer.Typer()


def _stage_s3_input_locally(s3_url: str) -> Path:
    import fsspec

    base = s3_url.rstrip("/")
    endpoint = os.environ.get("AWS_ENDPOINT_URL_S3") or os.environ.get("AWS_ENDPOINT_URL")
    storage_options: dict[str, Any] = {"client_kwargs": {"endpoint_url": endpoint}} if endpoint else {}

    fs, urlpath = fsspec.core.url_to_fs(base, **storage_options)
    name = base.rsplit("/", 1)[-1]

    staging_root = Path(tempfile.mkdtemp(prefix="aegis-datapack-"))
    atexit.register(shutil.rmtree, str(staging_root), ignore_errors=True)

    local_dir = staging_root / name
    local_dir.mkdir(parents=True, exist_ok=True)
    fs.get(f"{str(urlpath).rstrip('/')}/", f"{str(local_dir).rstrip('/')}/", recursive=True)

    logger.info("Staged s3 datapack from {} to {}", s3_url, local_dir)
    return local_dir


@app.command()
@timeit()
def run(
    algorithm: Annotated[str, typer.Option("-a", "--algorithm", envvar="ALGORITHM")],
    input_path: Annotated[str, typer.Option("-i", "--input-path", envvar="INPUT_PATH")],
    output_path: Annotated[Path, typer.Option("-o", "--output-path", envvar="OUTPUT_PATH")],
):
    assert algorithm in global_algorithm_registry(), f"Unknown algorithm: {algorithm}"

    local_input_path: Path = (
        _stage_s3_input_locally(input_path) if "://" in input_path else Path(input_path)
    )

    assert local_input_path.is_dir(), f"input_path: {local_input_path}"
    assert output_path.is_dir(), f"output_path: {output_path}"

    injection = load_json(path=local_input_path / "injection.json")
    injection_name = injection.get("injection_name") or injection.get("name")
    assert isinstance(injection_name, str) and injection_name

    converted_input_path = local_input_path / "converted"

    convert_datapack(
        loader=RCABenchDatapackLoader(src_folder=local_input_path, datapack=injection_name),
        dst_folder=converted_input_path,
        skip_finished=True,
    )

    a = global_algorithm_registry()[algorithm]()

    start_time = datetime.now()
    answers = a(
        AlgorithmArgs(
            dataset="rcabench",
            datapack=injection_name,
            input_folder=converted_input_path,
            output_folder=output_path,
        )
    )
    duration = datetime.now() - start_time

    result_rows = [{"level": ans.level, "result": ans.name, "rank": ans.rank, "confidence": 0} for ans in answers]

    # Check if submission is enabled
    submission_enabled = os.environ.get("RCABENCH_SUBMITION", "true").lower() != "false"

    if not submission_enabled:
        logger.info("Submission disabled by RCABENCH_SUBMITION environment variable")
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        result_file = output_path / f"{algorithm}_result_{timestamp}.csv"
        result_df = pd.DataFrame(result_rows)
        save_csv(result_df, path=result_file)
        logger.info(f"Results saved to {result_file}")
        return

    execution_id_str = os.environ.get("EXECUTION_ID")
    assert execution_id_str is not None, "EXECUTION_ID is not set"
    execution_id = int(execution_id_str)

    client = get_rcabench_client(base_url=os.environ.get("RCABENCH_BASE_URL"))
    exec_api = ExecutionsApi(client)

    resp = exec_api.upload_localization_results(
        execution_id=execution_id,
        request=UploadGranularityResultReq(
            duration=duration.total_seconds(),
            results=[
                GranularityResultItem(
                    level=row["level"],
                    result=row["result"],
                    rank=row["rank"],
                    confidence=row["confidence"],
                )
                for row in result_rows
            ],
        ),
    )
    logger.info(f"Submit localization result: response code: {resp.code}, message: {resp.message}")


@app.command()
@timeit()
def local_test(algorithm: str, datapack: str):
    input_path = Path("data") / "rcabench_dataset" / datapack

    output_path = get_config().temp / "run_exp_platform" / datapack / algorithm
    output_path.mkdir(parents=True, exist_ok=True)

    run(algorithm, str(input_path), output_path)
