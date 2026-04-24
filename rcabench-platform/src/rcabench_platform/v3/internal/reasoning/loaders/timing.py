import functools
import os
import time
from pathlib import Path


def timeit(func):
    """Decorator to measure and log function execution time to .timeit.log

    Only logs when ENABLE_TIMEIT environment variable is set.
    """

    @functools.wraps(func)
    def wrapper(*args, **kwargs):
        if not os.getenv("ENABLE_TIMEIT"):
            return func(*args, **kwargs)

        start = time.perf_counter()
        result = func(*args, **kwargs)
        elapsed = time.perf_counter() - start

        log_path = Path(".timeit.log")
        with log_path.open("a") as f:
            f.write(f"{func.__module__}.{func.__name__}: {elapsed:.6f}s\n")

        return result

    return wrapper
