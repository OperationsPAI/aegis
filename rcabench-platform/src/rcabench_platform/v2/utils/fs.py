import shutil
import time
from contextlib import contextmanager
from pathlib import Path


@contextmanager
def running_mark(folder: Path, *, clear: bool = False):
    running = folder / ".running"

    if clear or running.exists():
        shutil.rmtree(folder, ignore_errors=True)

    folder.mkdir(parents=True, exist_ok=True)
    running.touch()

    try:
        yield
    except Exception:
        raise
    else:
        running.unlink()


def has_recent_file(file_path: Path, *, seconds: int) -> bool:
    if not file_path.exists():
        return False

    mtime = file_path.stat().st_mtime
    current_time = time.time()
    return (current_time - mtime) <= seconds
