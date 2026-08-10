from datetime import datetime


def human_byte_size(size_bytes: int) -> str:
    if size_bytes < 1024:
        return f"{size_bytes} B"

    s = float(size_bytes)
    for unit in ["KiB", "MiB"]:
        s /= 1024
        if s < 1024:
            return f"{s:.3f} {unit}"

    return f"{s:.3f} GiB"


def get_timestamp() -> str:
    time_format = "%Y-%m-%d_%H-%M-%S"
    return datetime.now().strftime(time_format)
