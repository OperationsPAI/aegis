"""Specification for sampler experiments."""

from pathlib import Path

from ...datasets.spec import get_datapack_folder
from ..spec import SamplingMode


def get_sampler_output_folder(
    dataset: str, datapack: str, sampler: str, sampling_rate: float, mode: SamplingMode
) -> Path:
    """
    Get the output folder for sampler results.

    Format: {input_folder}/sampled/{sampler}_{sampling_rate}_{mode}
    Creates a sampled subdirectory in the datapack's input folder.
    """
    input_folder = get_datapack_folder(dataset, datapack)
    mode_str = mode.value
    rate_str = f"{sampling_rate:.3f}".rstrip("0").rstrip(".")
    folder_name = f"{sampler}_{rate_str}_{mode_str}"
    return input_folder / "sampled" / folder_name
