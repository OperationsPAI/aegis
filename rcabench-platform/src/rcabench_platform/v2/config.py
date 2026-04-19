import os
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path


@dataclass(kw_only=True)
class Config:
    env_mode: str

    data: Path
    output: Path
    temp: Path

    base_url: str


_DEFAULT_DATA_ROOT = Path(os.getenv("DATA_ROOT", "data/rcabench-platform-v2"))
_DEFAULT_OUTPUT_ROOT = Path(os.getenv("OUTPUT_ROOT", "output/rcabench-platform-v2"))
_DEFAULT_TEMP_ROOT = Path(os.getenv("TEMP_ROOT", "temp"))

_DEBUG_CONFIG = Config(
    env_mode="debug",
    data=_DEFAULT_DATA_ROOT,
    output=_DEFAULT_OUTPUT_ROOT,
    temp=_DEFAULT_TEMP_ROOT,
    base_url="http://127.0.0.1:8082",
)

_DEV_CONFIG = Config(
    env_mode="dev",
    data=_DEFAULT_DATA_ROOT,
    output=_DEFAULT_OUTPUT_ROOT,
    temp=_DEFAULT_TEMP_ROOT,
    base_url="http://10.10.10.161:8082",
)

_PROD_CONFIG = Config(
    env_mode="prod",
    data=_DEFAULT_DATA_ROOT,
    output=_DEFAULT_OUTPUT_ROOT,
    temp=_DEFAULT_TEMP_ROOT,
    base_url="http://10.10.10.220:32080",
)

_CONFIG_CLASSES = {
    "debug": _DEBUG_CONFIG,
    "dev": _DEV_CONFIG,
    "prod": _PROD_CONFIG,
}

_CONFIG = None


def get_config(env_mode: str | None = None) -> Config:
    if env_mode is not None:
        return _CONFIG_CLASSES[env_mode]

    global _CONFIG

    if _CONFIG is None:
        env_mode = os.getenv("ENV_MODE", "prod").lower()
        _CONFIG = _CONFIG_CLASSES[env_mode]

    return _CONFIG


def set_config(config: Config):
    global _CONFIG
    _CONFIG = config


@contextmanager
def current_config(config: Config):
    global _CONFIG

    old_config = _CONFIG
    _CONFIG = config

    try:
        yield
    finally:
        _CONFIG = old_config
