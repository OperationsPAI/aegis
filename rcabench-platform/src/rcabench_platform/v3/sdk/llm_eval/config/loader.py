from pathlib import Path

import yaml

from .eval_config import EvalConfig


class ConfigLoader:
    """Config loader using plain YAML files (no Hydra dependency)."""

    @classmethod
    def load_eval_config(cls, path: str | Path) -> EvalConfig:
        """Load EvalConfig from a YAML file.

        Args:
            path: Path to the YAML config file.

        Returns:
            EvalConfig instance.
        """
        with open(path) as f:
            data = yaml.safe_load(f)
        return EvalConfig(**data)
