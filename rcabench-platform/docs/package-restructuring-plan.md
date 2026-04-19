# Package Restructuring Plan

## Status: Implemented ✅ (v3)

The restructured codebase lives under `v3/`. The original `v2/` remains unchanged for backward compatibility.

## v2 → v3 Migration Guide

### Directory Structure Changes

v3 reorganizes the flat `v2/` layout into audience-based subpackages:

```
v2/ (original, unchanged)              v3/ (new, restructured)
├── algorithms/                        ├── sdk/
├── samplers/                          │   ├── algorithms/
├── datasets/                          │   ├── samplers/
├── evaluation/                        │   ├── datasets/
├── experiments/                       │   ├── evaluation/
├── graphs/                            │   ├── experiments/
├── pedestals/                         │   ├── graphs/
├── utils/                             │   ├── pedestals/
├── config.py                          │   ├── utils/
├── logging.py                         │   ├── config.py
├── clients/                           │   └── logging.py
├── cloud/                             ├── internal/
├── sources/                           │   ├── clients/
├── metrics/                           │   ├── cloud/
├── analysis/                          │   ├── sources/
├── tools/                             │   └── metrics/
└── cli/                               ├── analysis/
                                       ├── tools/
                                       └── cli/
```

### Import Path Changes

| v2 Import | v3 Import |
|-----------|-----------|
| `v2.algorithms.spec` | `v3.sdk.algorithms.spec` |
| `v2.samplers.spec` | `v3.sdk.samplers.spec` |
| `v2.datasets.spec` | `v3.sdk.datasets.spec` |
| `v2.evaluation.ranking` | `v3.sdk.evaluation.ranking` |
| `v2.experiments.single` | `v3.sdk.experiments.single` |
| `v2.graphs.sdg.*` | `v3.sdk.graphs.sdg.*` |
| `v2.pedestals.*` | `v3.sdk.pedestals.*` |
| `v2.utils.*` | `v3.sdk.utils.*` |
| `v2.config` | `v3.sdk.config` |
| `v2.logging` | `v3.sdk.logging` |
| `v2.clients.*` | `v3.internal.clients.*` |
| `v2.cloud.*` | `v3.internal.cloud.*` |
| `v2.sources.*` | `v3.internal.sources.*` |
| `v2.metrics.*` | `v3.internal.metrics.*` |
| `v2.analysis.*` | `v3.analysis.*` |
| `v2.tools.*` | `v3.tools.*` |
| `v2.cli.*` | `v3.cli.*` |

### Quick Migration

For most codebases, run these find-and-replace operations:

```bash
# SDK modules (algorithms, samplers, datasets, evaluation, experiments, graphs, pedestals, utils, config, logging)
sed -i 's/rcabench_platform\.v2\.algorithms\./rcabench_platform.v3.sdk.algorithms./g' your_code.py
sed -i 's/rcabench_platform\.v2\.samplers\./rcabench_platform.v3.sdk.samplers./g' your_code.py
sed -i 's/rcabench_platform\.v2\.datasets\./rcabench_platform.v3.sdk.datasets./g' your_code.py
sed -i 's/rcabench_platform\.v2\.evaluation\./rcabench_platform.v3.sdk.evaluation./g' your_code.py
sed -i 's/rcabench_platform\.v2\.experiments\./rcabench_platform.v3.sdk.experiments./g' your_code.py
sed -i 's/rcabench_platform\.v2\.graphs\./rcabench_platform.v3.sdk.graphs./g' your_code.py
sed -i 's/rcabench_platform\.v2\.pedestals/rcabench_platform.v3.sdk.pedestals/g' your_code.py
sed -i 's/rcabench_platform\.v2\.utils\./rcabench_platform.v3.sdk.utils./g' your_code.py
sed -i 's/rcabench_platform\.v2\.config/rcabench_platform.v3.sdk.config/g' your_code.py
sed -i 's/rcabench_platform\.v2\.logging/rcabench_platform.v3.sdk.logging/g' your_code.py

# Internal modules (clients, cloud, sources, metrics)
sed -i 's/rcabench_platform\.v2\.clients\./rcabench_platform.v3.internal.clients./g' your_code.py
sed -i 's/rcabench_platform\.v2\.cloud\./rcabench_platform.v3.internal.cloud./g' your_code.py
sed -i 's/rcabench_platform\.v2\.sources\./rcabench_platform.v3.internal.sources./g' your_code.py
sed -i 's/rcabench_platform\.v2\.metrics\./rcabench_platform.v3.internal.metrics./g' your_code.py

# Analysis, tools, CLI
sed -i 's/rcabench_platform\.v2\.analysis\./rcabench_platform.v3.analysis./g' your_code.py
sed -i 's/rcabench_platform\.v2\.tools\./rcabench_platform.v3.tools./g' your_code.py
sed -i 's/rcabench_platform\.v2\.cli\./rcabench_platform.v3.cli./g' your_code.py
```

### Package Substructure

#### `v3/sdk/` — SDK Core (Algorithm & Sampler Developers)

Install: `pip install rcabench-platform`

| Module | Description |
|--------|-------------|
| `sdk/algorithms/` | `Algorithm` base class, TraceBACK (A7–A10), RCAEval (baro, nsigma) |
| `sdk/samplers/` | `TraceSampler` base class, sampling experiments |
| `sdk/datasets/` | Dataset/datapack path helpers, data loaders |
| `sdk/evaluation/` | Performance metrics (Avg@k, Top-k, MRR, MAP) |
| `sdk/experiments/` | Experiment runners (single, batch, report) |
| `sdk/graphs/` | SDG data structures, builders, statistics |
| `sdk/pedestals/` | Dataset-specific processors |
| `sdk/utils/` | Shared utilities (serde, fmap, fs, etc.) |
| `sdk/config.py` | Environment configuration |
| `sdk/logging.py` | Structured logging with loguru |

#### `v3/internal/` — Platform Internal (Operators)

Install: `pip install "rcabench-platform[internal]"`

| Module | Description |
|--------|-------------|
| `internal/clients/` | RCABench, K8s, Neo4j, ClickHouse clients |
| `internal/cloud/` | MinIO, HuggingFace Hub storage |
| `internal/sources/` | Dataset format converters |
| `internal/metrics/` | Algorithm metrics calculation, anomaly detection |

#### `v3/analysis/` — Research & Visualization

Install: `pip install "rcabench-platform[analysis]"`

#### `v3/cli/` — CLI Entry Points

The CLI conditionally loads internal commands when `[internal]` deps are available.

### What's NOT Changing

- **v2 code remains untouched** — existing code using v2 imports continues to work
- **Package name** — still `rcabench-platform`
- **Dependency groups** — same `[internal]`, `[analysis]`, `[all]` optional groups

## Related Issues

- #76 — Windows support blocked by `resource` module in `sdg.py` (fixed in v3)
- #80 — LLM agent evaluation judge needs clean SDK interface
