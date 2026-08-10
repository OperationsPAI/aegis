#!/usr/bin/env python3
import multiprocessing

import typer
from tqdm.auto import tqdm

from ..algorithms.spec import global_algorithm_registry
from ..logging import logger, timeit
from ..utils.env import getenv_bool

app = typer.Typer(pretty_exceptions_show_locals=False)


@app.callback()
def _():
    logger.remove()
    logger.add(
        lambda msg: tqdm.write(msg, end=""),
        colorize=getenv_bool("LOGURU_COLORIZE", default=True),
        enqueue=True,
        context=multiprocessing.get_context("spawn"),
    )


def main(*, enable_builtin_algorithms: bool = True) -> None:
    from . import container, eval, online, sample, sdg, self_, tools

    app.add_typer(self_.app, name="self")
    app.add_typer(tools.app, name="tools")
    app.add_typer(online.app, name="online")
    app.add_typer(sdg.app, name="sdg")
    app.add_typer(eval.app, name="eval")
    app.add_typer(sample.app, name="sample")
    app.add_typer(container.app, name="container")
    if enable_builtin_algorithms:
        register_builtin_algorithms()

    app()


def register_builtin_algorithms():
    from ..algorithms.random_ import Random
    from ..algorithms.rcaeval.baro import Baro
    from ..algorithms.rcaeval.nsigma import NSigma
    from ..algorithms.traceback.A7 import TraceBackA7
    from ..algorithms.traceback.A8 import TraceBackA8
    from ..algorithms.traceback.A9 import TraceBackA9
    from ..algorithms.traceback.A10 import TraceBackA10

    getters = {
        "random": Random,
        "traceback-A7": TraceBackA7,
        "traceback-A8": TraceBackA8,
        "traceback-A9": TraceBackA9,
        "traceback-A10": TraceBackA10,
        "baro": Baro,
        "nsigma": NSigma,
    }

    registry = global_algorithm_registry()
    for name, getter in getters.items():
        registry[name] = getter
