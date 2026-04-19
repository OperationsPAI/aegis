from pathlib import Path

from ..config import get_config


def get_output_folder(dataset: str, datapack: str, algorithm: str) -> Path:
    config = get_config()
    return config.output / "data" / dataset / datapack / algorithm


def get_output_meta_folder(dataset: str) -> Path:
    config = get_config()
    return config.output / "meta" / dataset
