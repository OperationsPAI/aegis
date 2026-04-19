from ....vendor.RCAEval.nsigma import nsigma
from ...algorithms.spec import Algorithm, AlgorithmAnswer, AlgorithmArgs
from ._common import SimpleMetricsAdapter


class NSigma(Algorithm):
    def needs_cpu_count(self) -> int | None:
        return 4

    def __call__(self, args: AlgorithmArgs) -> list[AlgorithmAnswer]:
        adapter = SimpleMetricsAdapter(nsigma)
        return adapter(args)
