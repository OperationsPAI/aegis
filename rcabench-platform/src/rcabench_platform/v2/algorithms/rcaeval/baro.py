from ....vendor.RCAEval.baro import baro
from ...algorithms.spec import Algorithm, AlgorithmAnswer, AlgorithmArgs
from ._common import SimpleMetricsAdapter


class Baro(Algorithm):
    def needs_cpu_count(self) -> int | None:
        return 4

    def __call__(self, args: AlgorithmArgs) -> list[AlgorithmAnswer]:
        adapter = SimpleMetricsAdapter(baro)
        return adapter(args)
