from typing import Literal, cast

from .utils import EnvUtils, setup_logging

log_level = EnvUtils.get_env("LLM_EVAL_LOG_LEVEL", None) or EnvUtils.get_env("UTU_LOG_LEVEL", "WARNING")
_level = cast(Literal["WARNING", "INFO", "DEBUG"], log_level or "WARNING")
setup_logging(_level)
