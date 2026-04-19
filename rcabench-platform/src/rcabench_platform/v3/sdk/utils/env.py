import os


def getenv_bool(var_name: str, *, default: bool | None = None) -> bool:
    """
    Get a boolean environment variable.

    Args:
        var_name (str): The name of the environment variable.

    Returns:
        bool: The value of the environment variable as a boolean.
    """
    env = os.getenv(var_name)
    if env is None and default is not None:
        return default
    return (env or "").lower() in ("true", "1")


def getenv_int(var_name: str, *, default: int | None = None) -> int:
    """
    Get an integer environment variable.

    Args:
        var_name (str): The name of the environment variable.

    Returns:
        int: The value of the environment variable as an integer.
    """
    env = os.getenv(var_name)
    if env is None and default is not None:
        return default
    return int(env) if env else 0


DEBUG = getenv_bool("DEBUG", default=False)


def debug() -> bool:
    return DEBUG
