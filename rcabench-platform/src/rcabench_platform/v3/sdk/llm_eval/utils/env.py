import importlib.metadata
import os

from dotenv import find_dotenv, load_dotenv

# Load .env file but don't override existing environment variables
# This allows command-line env vars to take precedence
load_dotenv(find_dotenv(raise_error_if_not_found=False), verbose=True, override=False)


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
