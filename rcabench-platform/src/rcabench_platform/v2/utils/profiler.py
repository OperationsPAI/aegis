import functools
import time
from collections import defaultdict
from collections.abc import Callable, Generator
from contextlib import contextmanager
from typing import Any, TypeVar

F = TypeVar("F", bound=Callable[..., Any])


class ProfilerResult:
    def __init__(self):
        self.timings: dict[str, list[float]] = defaultdict(list)
        self.call_counts: dict[str, int] = defaultdict(int)

    def record(self, name: str, duration: float) -> None:
        self.timings[name].append(duration)
        self.call_counts[name] += 1

    def get_stats(self) -> dict[str, dict[str, float]]:
        stats = {}
        for name, times in self.timings.items():
            stats[name] = {
                "count": self.call_counts[name],
                "total": sum(times),
                "avg": sum(times) / len(times),
                "min": min(times),
                "max": max(times),
            }
        return stats

    def print_stats(self, sort_by: str = "total") -> None:
        stats = self.get_stats()

        if not stats:
            print("No profiling data available.")
            return

        sorted_stats = sorted(stats.items(), key=lambda x: x[1][sort_by], reverse=True)

        print(f"\n{'Function':<40} {'Count':<8} {'Total(s)':<10} {'Avg(s)':<10} {'Min(s)':<10} {'Max(s)':<10}")
        print("-" * 88)

        for name, data in sorted_stats:
            print(
                f"{name:<40} {data['count']:<8} {data['total']:<10.4f} {data['avg']:<10.4f} "
                f"{data['min']:<10.4f} {data['max']:<10.4f}"
            )

        print("-" * 88)
        total_time = sum(data["total"] for data in stats.values())
        print(f"{'Total execution time:':<40} {total_time:.4f}s")


class FunctionProfiler:
    def __init__(self):
        self.result = ProfilerResult()
        self.enabled = True

    def enable(self) -> None:
        self.enabled = True

    def disable(self) -> None:
        self.enabled = False

    def clear(self) -> None:
        self.result = ProfilerResult()

    @contextmanager
    def profile(self, name: str) -> Generator[None, None, None]:
        if not self.enabled:
            yield
            return

        start_time = time.perf_counter()
        try:
            yield
        finally:
            end_time = time.perf_counter()
            duration = end_time - start_time
            self.result.record(name, duration)

    def profile_function(self, name: str | None = None) -> Callable[[F], F]:
        def decorator(func: F) -> F:
            func_name = name or f"{func.__module__}.{func.__qualname__}"

            @functools.wraps(func)
            def wrapper(*args, **kwargs):
                if not self.enabled:
                    return func(*args, **kwargs)

                with self.profile(func_name):
                    return func(*args, **kwargs)

            return wrapper  # type: ignore

        return decorator

    def profile_method_calls(self, obj: Any, method_names: list[str]) -> None:
        for method_name in method_names:
            if hasattr(obj, method_name):
                original_method = getattr(obj, method_name)
                profiled_method = self.profile_function(f"{obj.__class__.__name__}.{method_name}")(original_method)
                setattr(obj, method_name, profiled_method)


global_profiler = FunctionProfiler()


def profile(name: str | None = None):
    return global_profiler.profile_function(name)


@contextmanager
def profile_block(name: str):
    with global_profiler.profile(name):
        yield


def get_profiler_stats():
    return global_profiler.result.get_stats()


def print_profiler_stats(sort_by: str = "total"):
    global_profiler.result.print_stats(sort_by)


def clear_profiler():
    global_profiler.clear()


def enable_profiler():
    global_profiler.enable()


def disable_profiler():
    global_profiler.disable()
