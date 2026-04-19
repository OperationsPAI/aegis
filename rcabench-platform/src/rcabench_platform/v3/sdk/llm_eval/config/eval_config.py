from __future__ import annotations

from collections.abc import Callable
from pathlib import Path
from typing import Literal

from pydantic import Field

from ..utils import EnvUtils
from .base_config import ConfigBaseModel
from .model_config import ModelConfigs


class DataConfig(ConfigBaseModel):
    """Data config"""

    dataset: str  # RCABench or custom dataset path
    """Built-in dataset name or custom dataset path"""
    type: Literal["single", "mixed"] = "single"
    """Whether the dataset contains only single benchmark data or multiple benchmarks"""
    question_field: str = "question"
    """Question field name in the dataset"""
    gt_field: str = "answer"
    """Ground truth field name in the dataset"""
    tags: list[str] | None = None
    """Filter samples by tags (OR logic: any matching tag)"""


class EvalConfig(ConfigBaseModel):
    """Evaluation config"""

    exp_id: str = "default"
    """Experiment ID"""

    # data
    db_url: str | None = EnvUtils.get_env("LLM_EVAL_DB_URL", None) or EnvUtils.get_env(
        "UTU_DB_URL", "sqlite:///test.db"
    )
    """Database URL"""
    data: DataConfig | None = None
    """Data config"""

    # rollout
    agent_type: str | None = None
    """Agent type label for tracking"""
    model_name: str | None = None
    """Model name label for tracking"""
    concurrency: int = 1
    """Rollout parallelism"""
    rollout_timeout: float | None = None
    """Per-sample rollout timeout in seconds. Samples exceeding this limit are skipped. None = no timeout."""
    max_samples: int | None = None
    """Maximum number of samples to rollout (None = all)"""
    source_path: str | None = None
    """Root path for dataset sources. Used to build a default source_path_fn when source_path_fn is not set."""
    source_path_pattern: str | None = None
    """Pattern template for resolving source data directories.

    Available placeholders: ``{source_path}``, ``{source}``.

    When set (via YAML or code), the framework builds a resolver from this
    pattern, replacing ``{source_path}`` with *source_path* and ``{source}``
    with the sample's source name at runtime.

    Default (when only *source_path* is set): ``{source_path}/{source}/converted``

    Example YAML::

        source_path: /mnt/jfs/rcabench_dataset
        source_path_pattern: "{source_path}/{source}/processed"
    """
    source_path_fn: Callable[[str], str | Path] | None = Field(default=None, exclude=True)
    """Custom function to resolve a source name to a data directory path (SDK only).

    Signature: ``(source: str) -> str | Path``

    Takes the highest priority.  Cannot be set from YAML — use
    *source_path_pattern* for config-driven customisation.

    Example::

        config = EvalConfig(
            source_path_fn=lambda source: f"/my/data/{source}/processed"
        )
    """

    model_config = {"arbitrary_types_allowed": True}

    _DEFAULT_PATTERN: str = "{source_path}/{source}/converted"

    def get_source_path_fn(self) -> Callable[[str], str | Path] | None:
        """Return the effective source path resolver.

        Priority:
          1. explicit *source_path_fn*  (SDK code)
          2. *source_path_pattern*      (YAML / code)
          3. default pattern            (when only *source_path* is set)
          4. None
        """
        if self.source_path_fn is not None:
            return self.source_path_fn
        if self.source_path is not None:
            _root = self.source_path
            _pattern = self.source_path_pattern or self._DEFAULT_PATTERN

            def _pattern_resolve(source: str) -> str:
                return _pattern.format(source_path=_root, source=source)

            return _pattern_resolve
        return None

    # judgement
    judge_model: ModelConfigs = Field(default_factory=ModelConfigs)
    """Judge model config"""
    judge_concurrency: int = 1
    """Judgement parallelism"""
    eval_method: str | None = None
    """Evaluation method"""
    pass_k: int = 1
    """Number of attempts to consider for pass@k metrics"""
