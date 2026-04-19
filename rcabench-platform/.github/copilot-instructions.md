# RCABench Platform - AI Coding Agent Instructions

## Project Overview

RCABench Platform is an experiment framework for Root Cause Analysis (RCA) in microservices. It supports fast development of RCA algorithms, trace samplers, and their evaluation on various datasets (rcabench, RCAEval, etc.).

**Key Architecture:** The project follows a plugin-based architecture where algorithms and samplers are registered via a global registry pattern. Data flows from external sources → conversion → standardized parquet files → algorithm execution → evaluation metrics.

## Development Setup

### Essential Commands

```bash
# Install dependencies and run checks (most important first command)
just dev

# Run evaluation locally (main workflow)
./main.py eval single <algorithm> <dataset> <datapack>
DEBUG=true ./main.py eval single <algorithm> <dataset> <datapack>  # verbose output

# Run batch evaluation
./main.py eval batch -a <algo1> -a <algo2> -d <dataset>

# List available resources
./main.py eval show-algorithms
./main.py eval show-datasets
```

**Environment Variables:**

- `DEBUG=true` - Enable verbose logging in algorithms
- `DATA_ROOT` - Override data directory (default: `data/rcabench-platform-v2`)
- `OUTPUT_ROOT` - Override output directory (default: `output/rcabench-platform-v2`)
- `LOGURU_COLORIZE` - Enable/disable log colors

### Dataset Linking

Datasets are stored on JuiceFS and must be symlinked before local development:

```bash
sudo juicefs mount redis://10.10.10.119:6379/1 /mnt/jfs -d --cache-size=1024
mkdir -p data && cd data
ln -s /mnt/jfs/rcabench-platform-v2 ./
```

See `CONTRIBUTING.md` for full setup details.

## Core Concepts & Patterns

### 1. Algorithm Development

All RCA algorithms inherit from `Algorithm` (in `src/rcabench_platform/v2/algorithms/spec.py`):

```python
class MyAlgorithm(Algorithm):
    def needs_cpu_count(self) -> int | None:
        return 1  # or None for all cores

    def __call__(self, args: AlgorithmArgs) -> list[AlgorithmAnswer]:
        # Read data from args.input_folder (parquet files)
        # Write intermediate results to args.output_folder
        # Return ranked list of root causes
        pass
```

**Registration Pattern:** Register in `src/rcabench_platform/v2/cli/main.py`:

```python
def register_builtin_algorithms():
    getters = {
        "my-algo": MyAlgorithm,
        # ...
    }
    registry = global_algorithm_registry()
    for name, getter in getters.items():
        registry[name] = getter
```

**Built-in algorithms:** `random`, `traceback-A7/A8/A9/A10`, `baro`, `nsigma`

### 2. Trace Sampler Development

Samplers inherit from `TraceSampler` (in `src/rcabench_platform/v2/samplers/spec.py`):

- **SamplingMode.ONLINE**: Returns all traces with scores (flexible)
- **SamplingMode.OFFLINE**: Limited by exact sampling rate (strict)

See `docs/specifications.md` for detailed sampler metrics (Controllability, API Coverage, Path Coverage, Event Coverage, Shannon Entropy, etc.).

### 3. Data Formats

**Standard Datapack Structure:**

```
data/<dataset>/<datapack>/
  ├── trace.parquet          # Distributed trace data
  ├── log.parquet            # Application logs
  ├── metrics.parquet        # Time-series metrics
  ├── metrics_sli.parquet    # SLI metrics with anomaly detection
  ├── injection.json         # Ground truth fault injection info
  └── conclusion.json        # Expected root causes (labels)
```

**Polars-First Approach:** All data processing uses `polars.LazyFrame` for lazy evaluation:

```python
import polars as pl
lf = pl.scan_parquet(input_folder / "trace.parquet")
# Always use lazy evaluation, collect only when necessary
```

**Immutable Dataclasses:** Use `@dataclass(frozen=True, slots=True)` for data structures (see examples in `algorithms/traceback/A10.py`).

### 4. Configuration & Environment Modes

Three modes in `config.py`: `debug`, `dev`, `prod` (selected via `ENV_MODE` environment variable).

- **debug**: Local development (base_url: `http://127.0.0.1:8082`)
- **dev**: Development server (base_url: `http://10.10.10.161:8082`)
- **prod**: Production server (base_url: `http://10.10.10.220:32080`)

### 5. Logging & Debugging

```python
from ...logging import logger, timeit

@timeit(log_level="INFO")  # Auto-logs function execution time
def my_function():
    logger.info("Processing...")
    if debug():  # Check DEBUG env var
        logger.debug("Verbose details")
```

**Progress Tracking:** Use `tqdm.auto.tqdm` for loops processing large datasets.

## Critical Workflows

### Converting External Datasets

Dataset converters follow a common pattern (see `src/rcabench_platform/v2/sources/`):

1. Implement `DatapackLoader` and `DatasetLoader` abstract classes
2. Use `convert_dataset()` function with parallel processing
3. Output: standardized parquet files + metadata (index.parquet, labels.parquet)

**Example generators:** `cli/dataset_transform/make_rcabench.py`, `cli/dataset_transform/make_rcaeval.py`

### Evaluation Metrics

Performance metrics are calculated in `src/rcabench_platform/v2/evaluation/ranking.py`:

- **Avg@k**: Average rank of first hit within top-k
- **Top-k Accuracy**: Percentage of cases with at least one hit in top-k

Results saved to `output/<dataset>/<datapack>/<algorithm>/perf.parquet`

### Multi-Processing Pattern

The codebase uses `fmap_processpool` (from `utils/fmap.py`) for parallel processing:

```python
from ..utils.fmap import fmap_processpool

tasks = [functools.partial(func, arg) for arg in args]
results = fmap_processpool(tasks, parallel=8, ignore_exceptions=False)
```

**CPU Allocation:** Algorithms specify `needs_cpu_count()` to enable intelligent scheduling.

## File Organization

```
src/rcabench_platform/v2/
├── algorithms/          # RCA algorithm implementations
│   ├── spec.py         # Algorithm base class & registry
│   ├── traceback/      # TraceBACK family (A7-A10)
│   └── rcaeval/        # RCAEval baselines (baro, nsigma)
├── samplers/           # Trace sampling algorithms
├── datasets/           # Dataset specs & utilities
├── evaluation/         # Metrics calculation
├── experiments/        # Experiment runners (single, batch)
├── sources/            # Data converters (rcabench, rcaeval)
├── cli/                # Command-line interface
└── config.py           # Environment configuration
```

## Important Conventions

1. **No in-place file modification:** Dataset generators are idempotent and skip finished datapacks (`.finished` marker files)
2. **Use `running_mark` context manager** when writing outputs to prevent incomplete states
3. **Linear commit history required:** Use `git rebase` and `--ff-only` merges (see `CONTRIBUTING.md`)
4. **Conventional Commits:** Follow format like `feat:`, `fix:`, `docs:` for commit messages
5. **Fish shell:** Terminal commands should be fish-compatible (no heredocs - use `printf` or `echo`)
6. **Type hints:** All new code should use type hints (checked with pyright)

## Testing & Validation

```bash
# Self-test (validates environment setup)
./main.py self test

# Format and lint
just fmt
just lint

# Full CI checks
just ci
```

**No dedicated test framework:** Testing primarily done through running algorithms on real datasets and validating outputs.

## Common Pitfalls

1. **Don't use eager `collect()` on large LazyFrames** - always use lazy operations until final output
2. **Dataset paths are absolute** - use `get_datapack_folder()`, `get_dataset_folder()` from `datasets/spec.py`
3. **Register new algorithms** - adding a new algorithm class isn't enough, must register in `cli/main.py`
4. **Check `.finished` markers** - use `skip_finished=True` to avoid re-running completed work
5. **Sampler vs Algorithm input** - sampled data goes to different input_folder (see `experiments/single.py:47-56`)

## External Dependencies

- **RCABench API Client:** OpenAPI-generated client in `rcabench` package for submitting results
- **JuiceFS:** Shared filesystem for dataset storage (requires manual mount)
- **Docker/Compose:** For local services (Neo4j graph visualization) and building deployment images
- **ClickHouse:** Used by some data collection workflows (see `docker/clickhouse_dataset/`)

## Release Process

```bash
# Automated patch release (bumps version, commits, tags, pushes)
./scripts/release-patch.sh

# Docker images (build order matters: rcabench-platform → clickhouse_dataset → detector)
./scripts/docker.py update-all
```

See `docs/maintenance.md` for full release procedures.
