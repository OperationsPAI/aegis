from pathlib import Path

import yaml

from ..utils import expand_env_refs
from .eval_config import EvalConfig


class ConfigLoader:
    """Config loader using plain YAML files (no Hydra dependency)."""

    @classmethod
    def load_eval_config(cls, path: str | Path) -> EvalConfig:
        """Load EvalConfig from a YAML file.

        ``${VAR}`` references in any string field are expanded from the process
        environment (after ``.env`` is loaded). Unset references raise a
        ``ValueError`` rather than being passed through, so misconfiguration
        surfaces at load time instead of as a downstream connection error.

        Args:
            path: Path to the YAML config file.

        Returns:
            EvalConfig instance.
        """
        with open(path) as f:
            data = yaml.safe_load(f)
        data = expand_env_refs(data)
        return EvalConfig(**data)
