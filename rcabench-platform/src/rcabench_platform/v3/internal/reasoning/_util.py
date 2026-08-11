"""Logging helper ported from rca_label/src/common/logging.py.

Reasoning uses stdlib ``logging`` internally (logger = logging.getLogger(__name__)).
This helper lets CLI entrypoints configure a reasonable root-logger handler once.
"""

import logging
from pathlib import Path

try:
    from rich.console import Console
    from rich.logging import RichHandler

    _HAS_RICH = True
except ImportError:
    Console = None  # type: ignore[assignment,misc]
    RichHandler = None  # type: ignore[assignment,misc]
    _HAS_RICH = False


_LINE_FMT = "%(asctime)s - %(name)s - %(levelname)s - %(message)s"
_DATE_FMT = "%Y-%m-%d %H:%M:%S"


def setup_logging(verbose: bool = False, log_file: Path | None = None) -> None:
    level = logging.DEBUG if verbose else logging.INFO

    root_logger = logging.getLogger()
    root_logger.setLevel(level)
    root_logger.handlers.clear()

    if _HAS_RICH and Console is not None and RichHandler is not None:
        console = Console(stderr=True, force_terminal=True)
        console_handler: logging.Handler = RichHandler(
            console=console,
            show_time=True,
            show_path=False,
            markup=True,
            rich_tracebacks=True,
            tracebacks_show_locals=False,
            log_time_format=f"[{_DATE_FMT}]",
        )
    else:
        console_handler = logging.StreamHandler()
        console_handler.setFormatter(logging.Formatter(fmt=_LINE_FMT, datefmt=_DATE_FMT))
    console_handler.setLevel(level)
    root_logger.addHandler(console_handler)

    if log_file:
        log_file.parent.mkdir(parents=True, exist_ok=True)
        file_handler = logging.FileHandler(log_file, mode="a", encoding="utf-8")
        file_handler.setLevel(level)
        file_handler.setFormatter(logging.Formatter(fmt=_LINE_FMT, datefmt=_DATE_FMT))
        root_logger.addHandler(file_handler)
        logging.getLogger(__name__).info(f"Logging to file: {log_file}")
