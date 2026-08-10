from pathlib import Path

from .rcabench import build_sdg_from_rcabench
from .rcaeval import build_sdg_from_rcaeval


def build_sdg(dataset: str, datapack: str, input_folder: Path):
    if dataset.startswith("rcabench"):
        return build_sdg_from_rcabench(dataset, datapack, input_folder)
    elif dataset.startswith("rcaeval"):
        return build_sdg_from_rcaeval(dataset, datapack, input_folder)
    else:
        raise NotImplementedError
