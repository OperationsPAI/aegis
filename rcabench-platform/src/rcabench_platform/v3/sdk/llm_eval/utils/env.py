import importlib.metadata
import os
import re
from typing import Any

from dotenv import find_dotenv, load_dotenv

# Load .env file but don't override existing environment variables
# This allows command-line env vars to take precedence
load_dotenv(find_dotenv(raise_error_if_not_found=False), verbose=True, override=False)


_ENV_REF_RE = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}")


def _expand_one(value: str) -> str:
    """Replace ``${VAR}`` references with the env var's value.

    Unset references raise a ValueError so misconfiguration fails loudly
    instead of being silently passed through to a downstream API client.
    """

    def repl(match: re.Match[str]) -> str:
        key = match.group(1)
        env = os.getenv(key)
        if env is None:
            raise ValueError(
                f"Config references env var ${{{key}}} but it is not set (check your .env / shell environment)."
            )
        return env

    return _ENV_REF_RE.sub(repl, value)


def expand_env_refs(data: Any) -> Any:
    """Recursively expand ``${VAR}`` references in any string leaves.

    Walks dicts and lists; non-string scalars are passed through. Designed for
    YAML-loaded config trees so users can write ``base_url: ${UTU_LLM_BASE_URL}``
    in a config file and have it resolved at load time.
    """
    if isinstance(data, dict):
        return {k: expand_env_refs(v) for k, v in data.items()}
    if isinstance(data, list):
        return [expand_env_refs(v) for v in data]
    if isinstance(data, str):
        return _expand_one(data)
    return data


class EnvUtils:
    @staticmethod
    def get_env(key: str, default: str | None = None) -> str | None:
        """Get the value of an environment variable.

        Supports fallback from LLM_EVAL_* to UTU_* env vars for backward compatibility.
        If default is None and the env var is not set, returns None (no error).
        """
        value = os.getenv(key)
        if value is not None:
            return value
        if default is not None:
            return default
        return None

    @staticmethod
    def assert_env(key: str | list[str]) -> None:
        if isinstance(key, list):
            for k in key:
                EnvUtils.assert_env(k)
        else:
            if not os.getenv(key):
                raise ValueError(f"Environment variable {key} is not set")

    @staticmethod
    def ensure_package(package_name: str) -> None:
        try:
            importlib.metadata.version(package_name)
        except importlib.metadata.PackageNotFoundError:
            raise ValueError(f"Package {package_name} is required but not installed!") from None
