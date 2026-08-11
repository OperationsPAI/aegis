import resource

import typer

from ..config import get_config
from ..datasets.spec import get_datapack_folder
from ..graphs.sdg.build_ import build_sdg
from ..graphs.sdg.dump import dump_place_indicators
from ..graphs.sdg.neo4j import export_sdg_to_neo4j
from ..logging import logger, timeit
from ..utils.serde import save_parquet, save_pickle

app = typer.Typer()


@app.command()
@timeit()
def build(
    dataset: str,
    datapack: str,
    neo4j: bool = True,
) -> None:
    sdg = build_sdg(dataset, datapack, get_datapack_folder(dataset, datapack))

    maxrss_kib = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    maxrss_mib = maxrss_kib / 1024
    logger.info(f"Peak memory usage: {maxrss_mib:.3f} MiB")

    temp_sdg = get_config().temp / "sdg"

    sdg_pkl_path = temp_sdg / "sdg.pkl"
    save_pickle(sdg, path=sdg_pkl_path)

    sdg_pkl_size = sdg_pkl_path.stat().st_size / 1024 / 1024
    logger.info(f"SDG pickle size: {sdg_pkl_size:.3f} MiB")

    df = dump_place_indicators(sdg)
    save_parquet(df, path=temp_sdg / "indicators.parquet")

    if neo4j:
        export_sdg_to_neo4j(sdg)
