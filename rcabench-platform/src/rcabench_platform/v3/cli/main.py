#!/usr/bin/env python3
import multiprocessing

import typer
from tqdm.auto import tqdm

from ..sdk.algorithms.spec import global_algorithm_registry
from ..sdk.logging import logger, timeit
from ..sdk.utils.env import getenv_bool

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
    # SDK-based CLI modules require optional dependencies (rcabench-platform[sdk])
    try:
        from . import eval, sample, tools

        app.add_typer(tools.app, name="tools")
        app.add_typer(eval.app, name="eval")
        app.add_typer(sample.app, name="sample")
    except ImportError:
        pass

    # Platform-internal CLI modules require optional dependencies (rcabench-platform[internal])
    try:
        from . import container, online, sdg, self_

        app.add_typer(self_.app, name="self")
        app.add_typer(online.app, name="online")
        app.add_typer(sdg.app, name="sdg")
        app.add_typer(container.app, name="container")
    except ImportError:
        pass

    # LLM agent eval CLI (requires rcabench-platform[llm-eval])
    try:
        from . import llm_eval

        app.add_typer(llm_eval.app, name="llm-eval")
    except ImportError:
        pass

    if enable_builtin_algorithms:
        try:
            register_builtin_algorithms()
        except ImportError:
            pass

    app()


def register_builtin_algorithms():
    from ..sdk.algorithms.random_ import Random
    from ..sdk.algorithms.rcaeval.baro import Baro
    from ..sdk.algorithms.rcaeval.nsigma import NSigma
    from ..sdk.algorithms.traceback.A7 import TraceBackA7
    from ..sdk.algorithms.traceback.A8 import TraceBackA8
    from ..sdk.algorithms.traceback.A9 import TraceBackA9
    from ..sdk.algorithms.traceback.A10 import TraceBackA10

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
