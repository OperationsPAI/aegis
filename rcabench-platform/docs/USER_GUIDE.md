# User Guide

This guide shows you how to use rcabench-platform both as a console command and as an SDK for developing and evaluating Root Cause Analysis (RCA) algorithms.

## Table of Contents

1. [Installation](#installation)
2. [Console Command Usage](#console-command-usage)
3. [SDK Usage](#sdk-usage)
4. [Common Use Cases](#common-use-cases)
5. [Advanced Topics](#advanced-topics)
6. [Related Documentation](#related-documentation)

## Installation

### Basic Installation

To install rcabench-platform in your project:

```bash
# Using uv (recommended)
uv add rcabench-platform

# Using pip
pip install rcabench-platform
```

### Installation with Analysis Features

For dataset analysis functionality (includes graphviz and matplotlib):

```bash
# Using uv
uv add "rcabench-platform[analysis]"

# Using pip
pip install "rcabench-platform[analysis]"
```

## Console Command Usage

Once installed, rcabench-platform provides the `rca` command for interacting with the RCABench platform and managing RCA workflows.

### Basic Commands

#### List Available Commands

```bash
rca --help
```

#### Working with Datasets

```bash
# List all available datasets
rca list-datasets

# Get details about a specific dataset
rca get-dataset --id 123

# Search for datasets
rca query-injection --name "your-dataset-name"
```

#### Working with Algorithms

```bash
# List all available algorithms
rca list-algorithms

# Upload a new algorithm from Harbor registry
rca upload-algorithm-harbor /path/to/algorithm/folder

# List injections
rca list-injections
```

#### Algorithm Execution

```bash
# Submit algorithm execution on a dataset
rca submit-execution \
  --algorithm "random" \
  --dataset "my-dataset" \
  --dataset-version "v1.0" \
  --project "my-project" \
  --tag "experiment-1"

# Submit multiple algorithms
rca submit-execution \
  --algorithm "random" \
  --algorithm "baro" \
  --algorithm "nsigma" \
  --dataset "my-dataset" \
  --dataset-version "v1.0" \
  --project "my-project" \
  --tag "comparison-experiment"

# Submit execution with custom Docker image
rca submit-execution \
  --algorithm "registry.example.com/my-algo:latest" \
  --dataset "my-dataset" \
  --dataset-version "v1.0" \
  --project "my-project" \
  --tag "custom-algo-test"

# Submit execution with environment variables
rca submit-execution \
  --algorithm "my-algorithm" \
  --dataset "my-dataset" \
  --dataset-version "v1.0" \
  --project "my-project" \
  --tag "env-test" \
  --env "PARAM1=value1" \
  --env "PARAM2=value2"
```

#### Local Algorithm Evaluation

For local development and testing, you can run algorithms directly on local datasets using the eval commands:

```bash
# Run a single algorithm on a datapack
python main.py eval single nsigma rcabench-train train-1

# Run with sampled data (requires prior sampling)
python main.py eval single nsigma rcabench-train train-1 \
  --sampler random \
  --sampling-rate 0.5 \
  --sampling-mode ONLINE

# Run multiple algorithms in batch
python main.py eval batch \
  --algorithm nsigma \
  --algorithm baro \
  --dataset rcabench-train

# Run batch evaluation with sampled data
python main.py eval batch \
  --algorithm nsigma \
  --algorithm baro \
  --dataset rcabench-train \
  --sampler random \
  --sampling-rate 0.5 \
  --sampling-mode OFFLINE

# Run batch evaluation with auto-detected sampler configurations
python main.py eval batch \
  --algorithm nsigma \
  --algorithm baro \
  --dataset rcabench-train \
  --include-sampled

# Run batch evaluation with multiple sampler configurations
python main.py eval batch \
  --algorithm nsigma \
  --algorithm baro \
  --dataset rcabench-train \
  --include-sampled \
  --sampler random \
  --sampler my-custom-sampler \
  --sampling-rate 0.1 \
  --sampling-rate 0.2 \
  --sampling-mode OFFLINE \
  --sampling-mode ONLINE

# Generate performance reports
python main.py eval perf-report rcabench-train

# Generate reports including sampled data results
python main.py eval perf-report rcabench-train --include-sampled

# Generate reports excluding sampled data (traditional format)
python main.py eval perf-report rcabench-train --no-include-sampled
```

**Performance Report Output Formats:**

When using `--include-sampled`, the report will show a unified format with both sampled and non-sampled results:
- Rows with `sampler.name` values show performance on sampled data
- Rows with `sampler.name` as null show performance on original (non-sampled) data
- This allows direct comparison between sampled and non-sampled algorithm performance
- All metrics use the same calculation method for consistency

```bash

When using sampler parameters with eval commands:
- The algorithm will run on the sampled traces from the specified sampler
- Input data is read from `{datapack}/sampled/{sampler}_{rate}_{mode}/` directory
- Output results include sampler metadata for tracking
- Performance reports can distinguish between sampled and non-sampled results

**Auto-Detection vs. Manual Configuration:**

- **Auto-detection**: Use `--include-sampled` without specifying samplers, rates, or modes to automatically detect all available sampler configurations in the output directory
- **Manual configuration**: Specify exact sampler parameters with `--sampler`, `--sampling-rate`, and `--sampling-mode` flags
- **Hybrid approach**: Use `--include-sampled` with specific filters to auto-detect configurations but limit to certain samplers, rates, or modes
- **Multiple values**: Each sampler parameter flag can be specified multiple times to include multiple configurations

**Examples:**
```bash
# Auto-detect all available sampler configurations
python main.py eval batch --algorithm nsigma --dataset rcabench-train --include-sampled

# Filter to specific samplers while auto-detecting rates and modes
python main.py eval batch --algorithm nsigma --dataset rcabench-train --include-sampled --sampler random

# Specify exact configuration (traditional approach)
python main.py eval batch --algorithm nsigma --dataset rcabench-train --sampler random --sampling-rate 0.1 --sampling-mode OFFLINE
```

**Available Sampling Modes:**
- `ONLINE`: Independent sampling where each trace decision is made individually using various strategies (threshold-based, probability-based, etc.)
- `OFFLINE`: Batch sampling that sorts traces by score and selects exactly sampling_rate proportion of traces

#### Trace Sampling

```bash
# List available trace samplers
python main.py sample show-samplers

# Run a single sampler on a datapack
python main.py sample single \
  --sampler "random" \
  --dataset "my-dataset" \
  --datapack "my-datapack" \
  --sampling-rate 0.1 \
  --mode offline

# Run multiple samplers in batch
python main.py sample batch \
  --sampler "random" \
  --dataset "my-dataset" \
  --rate 0.1 \
  --rate 0.2 \
  --mode offline \
  --mode online

# Generate sampling performance report (auto-detects all configurations)
python main.py sample perf-report \
  --dataset "my-dataset"

# Generate report for specific samplers only
python main.py sample perf-report \
  --dataset "my-dataset" \
  --sampler "random" \
  --sampler "my-custom-sampler"

# Generate report for specific rates and modes
python main.py sample perf-report \
  --dataset "my-dataset" \
  --rate 0.1 \
  --rate 0.2 \
  --mode offline

# Generate report for multiple datasets with specific filters
python main.py sample perf-report \
  --dataset "dataset1" \
  --dataset "dataset2" \
  --sampler "random" \
  --rate 0.1 \
  --mode offline \
  --mode online

# Generate SLI metrics aggregation for RCA algorithms
python main.py sample generate-sli-metrics my-dataset --datapack my-datapack

# Generate SLI metrics for entire dataset
python main.py sample generate-sli-metrics my-dataset
```

**Sampler Report Output Structure:**

Reports are now organized by dataset for better management:

```
{output}/sampler_reports/
├── detailed_perf.parquet          # Combined cross-dataset results
├── aggregated_perf.parquet        # Combined cross-dataset aggregates
├── dataset1/
│   ├── detailed_perf.parquet      # Dataset1-specific detailed results
│   └── aggregated_perf.parquet    # Dataset1-specific aggregates
└── dataset2/
    ├── detailed_perf.parquet      # Dataset2-specific detailed results
    └── aggregated_perf.parquet    # Dataset2-specific aggregates
```

**Auto-Detection vs. Filtering:**

- **Auto-detection (default)**: When samplers, rates, or modes are not specified, the system scans existing results and includes all available configurations
- **Filtering**: Use `-s/--sampler`, `-r/--rate`, and `-m/--mode` flags to filter to specific configurations
- **Multiple values**: Each flag can be specified multiple times to include multiple values
- **Cross-filtering**: All specified filters are applied (AND logic between filter types, OR logic within each filter type)

#### Monitoring and Analysis

```bash
# Trace execution events
rca trace "trace-id-12345" --timeout 600

# Get metrics for a single algorithm
rca metrics \
  --algorithm "random" \
  --dataset "my-dataset" \
  --dataset-version "v1.0" \
  --tag "experiment-1"

# Compare multiple algorithms
rca multi-metrics \
  --algorithm "random" \
  --algorithm "baro" \
  --algorithm "nsigma" \
  --dataset "my-dataset" \
  --dataset-version "v1.0" \
  --tag "comparison-experiment"

# Cross-dataset comparison
rca cross-dataset-metrics \
  --algorithm "random" \
  --algorithm "baro" \
  --dataset "dataset1" \
  --dataset "dataset2" \
  --dataset-version "v1.0" \
  --dataset-version "v1.0" \
  --tag "cross-comparison"
```

#### Infrastructure Operations

```bash
# Download Kubernetes cluster information
rca kube-info --namespace "default" --save-path /tmp/kube-info.json
```

### Alternative CLI Entry Point

You can also use the main.py script directly:

```bash
# Clone the repository and run locally
python main.py --help
python main.py online list-datasets
python main.py online submit-execution --help
```

## SDK Usage

The rcabench-platform can be used as a Python SDK for programmatic access to RCA functionality.

### Basic SDK Usage

```python
import rcabench_platform

# Check package version
print(f"rcabench-platform version: {rcabench_platform.__version__}")
```

### Working with RCABench Client

```python
from rcabench_platform.v2.clients.rcabench_ import RCABenchClient
from rcabench.openapi import DatasetsApi, AlgorithmsApi, DtoDatasetV2SearchReq

# Create a client
with RCABenchClient() as client:
    # Work with datasets
    datasets_api = DatasetsApi(client)
    datasets = datasets_api.api_v2_datasets_search_post(
        search=DtoDatasetV2SearchReq(search="")
    )
    print(f"Found {len(datasets.data.items)} datasets")

    # Work with algorithms
    algorithms_api = AlgorithmsApi(client)
    algorithms = algorithms_api.api_v2_algorithms_get()
    print(f"Found {len(algorithms.data.items)} algorithms")
```

### Developing Custom RCA Algorithms

```python
from pathlib import Path
from rcabench_platform.v2.algorithms.spec import Algorithm, AlgorithmArgs, AlgorithmAnswer

class MyCustomAlgorithm(Algorithm):
    def needs_cpu_count(self) -> int | None:
        # Return number of CPU cores needed, or None for all available cores
        return 1

    def __call__(self, args: AlgorithmArgs) -> list[AlgorithmAnswer]:
        # Implement your RCA logic here
        # args.dataset: dataset name
        # args.datapack: datapack name
        # args.input_folder: Path to input data
        # args.output_folder: Path to save results

        # Example: Simple random ranking
        services = ["service-a", "service-b", "service-c"]
        results = []

        for i, service in enumerate(services):
            results.append(AlgorithmAnswer(
                level="service",
                name=service,
                rank=i + 1
            ))

        return results

# Register your algorithm
from rcabench_platform.v2.algorithms.spec import global_algorithm_registry

registry = global_algorithm_registry()
registry["my-custom-algorithm"] = MyCustomAlgorithm
```

### Developing Custom Trace Samplers

```python
from pathlib import Path
from rcabench_platform.v2.samplers.spec import TraceSampler, SamplerArgs, SampleResult, SamplingMode

class MyCustomSampler(TraceSampler):
    def needs_cpu_count(self) -> int | None:
        # Return number of CPU cores needed, or None for all available cores
        return 1

    def __call__(self, args: SamplerArgs) -> list[SampleResult]:
        # Implement your sampling logic here
        # args.dataset: dataset name
        # args.datapack: datapack name
        # args.input_folder: Path to input data
        # args.output_folder: Path to save results
        # args.sampling_rate: Target sampling rate (0.0 to 1.0)
        # args.mode: SamplingMode.ONLINE or SamplingMode.OFFLINE

        # Load traces from normal_traces.parquet and abnormal_traces.parquet
        import polars as pl

        normal_traces_lf = pl.scan_parquet(args.input_folder / "normal_traces.parquet")
        abnormal_traces_lf = pl.scan_parquet(args.input_folder / "abnormal_traces.parquet")

        # Get unique trace_ids
        combined_traces_lf = pl.concat([
            normal_traces_lf.select("trace_id"),
            abnormal_traces_lf.select("trace_id")
        ])
        unique_traces = combined_traces_lf.unique().collect()
        trace_ids = unique_traces["trace_id"].to_list()

        # Implement your sampling strategy
        results = []
        for trace_id in trace_ids:
            # Calculate sample score based on your algorithm
            sample_score = 0.5  # Example: constant score
            results.append(SampleResult(trace_id=trace_id, sample_score=sample_score))

        # Apply sampling mode
        if args.mode == SamplingMode.ONLINE:
            # Online mode: independent sampling strategy
            # Each trace is independently decided whether to sample
            # Implementation can vary: threshold-based, probability-based, etc.
            # Example implementation using threshold:
            threshold = 1.0 - args.sampling_rate  # Higher sampling_rate = lower threshold
            sampled_results = []
            for result in results:
                if result.sample_score > threshold:
                    sampled_results.append(result)
            # Sort by score (higher scores first) for consistency
            sampled_results.sort(key=lambda x: x.sample_score, reverse=True)
            return sampled_results
        elif args.mode == SamplingMode.OFFLINE:
            # Offline mode: strict sampling rate limit
            # Sort by score and take top N traces
            results.sort(key=lambda x: x.sample_score, reverse=True)
            target_count = int(len(results) * args.sampling_rate)
            return results[:target_count]

        return results

# Register your sampler
from rcabench_platform.v2.samplers.spec import global_sampler_registry

registry = global_sampler_registry()
registry["my-custom-sampler"] = MyCustomSampler
```

### Using Built-in Algorithms

```python
from rcabench_platform.v2.algorithms.random_ import Random
from rcabench_platform.v2.algorithms.rcaeval.baro import Baro
from rcabench_platform.v2.algorithms.spec import AlgorithmArgs
from pathlib import Path

# Use the random algorithm
random_algo = Random()
print(f"Random algorithm needs {random_algo.needs_cpu_count()} CPU cores")

# Create algorithm arguments
args = AlgorithmArgs(
    dataset="my-dataset",
    datapack="my-datapack",
    input_folder=Path("/path/to/input"),
    output_folder=Path("/path/to/output")
)

# Run the algorithm
results = random_algo(args)
for result in results:
    print(f"Level: {result.level}, Name: {result.name}, Rank: {result.rank}")
```

### Using Built-in Samplers

```python
from rcabench_platform.v2.samplers.random_ import RandomSampler
from rcabench_platform.v2.samplers.spec import SamplerArgs, SamplingMode
from pathlib import Path

# Use the random sampler
random_sampler = RandomSampler(seed=42)  # Optional seed for reproducibility
print(f"Random sampler needs {random_sampler.needs_cpu_count()} CPU cores")

# Create sampler arguments
args = SamplerArgs(
    dataset="my-dataset",
    datapack="my-datapack",
    input_folder=Path("/path/to/input"),
    output_folder=Path("/path/to/output"),
    sampling_rate=0.1,  # 10% sampling rate
    mode=SamplingMode.OFFLINE
)

# Run the sampler
sample_results = random_sampler(args)
for result in sample_results:
    print(f"Trace ID: {result.trace_id}, Score: {result.sample_score}")

# Sample results can be used for different purposes:
if args.mode == SamplingMode.OFFLINE:
    print(f"Offline mode: Got exactly {len(sample_results)} sampled traces")
else:
    print(f"Online mode: Got {len(sample_results)} traces with scores > threshold")
```

### Generating SLI Metrics

```python
from rcabench_platform.v2.samplers.metrics_sli import generate_metrics_sli, copy_metrics_sli_to_sampled
from pathlib import Path

# Generate metrics_sli.parquet for a datapack
input_folder = Path("/path/to/datapack")
generate_metrics_sli(input_folder)

# Generate with custom output location
output_folder = Path("/path/to/custom/output")
generate_metrics_sli(input_folder, output_folder=output_folder)

# Copy metrics_sli.parquet to sampled folder (for RCA algorithm fairness)
sampled_folder = Path("/path/to/sampled/data")
copy_metrics_sli_to_sampled(input_folder, sampled_folder)

# Load and use the generated SLI metrics
import polars as pl

sli_metrics = pl.read_parquet(input_folder / "metrics_sli.parquet")
print(f"Generated {len(sli_metrics)} SLI metric records")

# Example: Get duration statistics for a specific service
service_metrics = sli_metrics.filter(
    pl.col("service_name") == "ts-user-service"
).select([
    "time", "span_name", "avg_duration", "duration_p95",
    "total_count", "error_count"
])
```

### Working with Metrics

```python
from rcabench_platform.v2.metrics.algo_metrics import (
    get_metrics_by_dataset,
    get_multi_algorithms_metrics_by_dataset
)

# Get metrics for a single algorithm
metrics = get_metrics_by_dataset(
    algorithm="random",
    dataset="my-dataset",
    dataset_version="v1.0",
    tag="experiment-1"
)
print(f"Metrics: {metrics}")

# Compare multiple algorithms
comparison_metrics = get_multi_algorithms_metrics_by_dataset(
    algorithms=["random", "baro", "nsigma"],
    dataset="my-dataset",
    dataset_version="v1.0",
    tag="comparison-experiment"
)
print(f"Comparison metrics: {comparison_metrics}")
```

### Working with Sampler Performance

```python
from rcabench_platform.v2.samplers.experiments import (
    run_sampler_single,
    run_sampler_batch,
    generate_sampler_perf_report
)
from rcabench_platform.v2.samplers.spec import SamplingMode

# Run a single sampler experiment
run_sampler_single(
    sampler="random",
    dataset="my-dataset",
    datapack="my-datapack",
    sampling_rate=0.1,
    mode=SamplingMode.OFFLINE
)

# Run batch sampler experiments
run_sampler_batch(
    samplers=["random"],
    datasets=["my-dataset"],
    sampling_rates=[0.1, 0.2],
    modes=[SamplingMode.OFFLINE, SamplingMode.ONLINE]
)

# Generate performance report
generate_sampler_perf_report(
    datasets=["my-dataset"],
    samplers=["random"],
    sampling_rates=[0.1],
    modes=[SamplingMode.OFFLINE]
)

# Access detailed performance metrics including path coverage
from rcabench_platform.v2.config import get_config
import polars as pl

config = get_config()
perf_df = pl.read_parquet(config.output / "sampler_reports" / "detailed_perf.parquet")

# Display coverage comparison
coverage_comparison = perf_df.select([
    "sampler", "dataset", "sampling_rate", "mode",
    "comprehensiveness",  # API coverage (renamed from comprehensiveness)
    "path_coverage",      # Execution path coverage
    "event_coverage",     # Event coverage (traces + logs)
    "total_entry_types",  # Total API types
    "total_path_types",   # Total execution path types
    "total_event_pairs"   # Total event pairs
])
print(coverage_comparison)
```

### Working with Configuration

```python
from rcabench_platform.v2.config import get_config

# Get current configuration
config = get_config()
print(f"Temp directory: {config.temp}")
```

### Logging

```python
from rcabench_platform.v2.logging import logger, timeit

# Use structured logging
logger.info("Starting RCA analysis")
logger.error("Analysis failed", error="connection timeout")

# Use timing decorator
@timeit()
def run_analysis():
    # Your analysis code here
    pass

run_analysis()
```

## Common Use Cases

### 1. Running a Quick Algorithm Comparison

```bash
# Compare built-in algorithms on a dataset
rca submit-execution \
  --algorithm "random" \
  --algorithm "baro" \
  --algorithm "nsigma" \
  --dataset "test-dataset" \
  --dataset-version "v1.0" \
  --project "algorithm-comparison" \
  --tag "quick-test"

# Wait for completion and get results
rca multi-metrics \
  --algorithm "random" \
  --algorithm "baro" \
  --algorithm "nsigma" \
  --dataset "test-dataset" \
  --dataset-version "v1.0" \
  --tag "quick-test"
```

### 2. Developing and Testing a New Algorithm

```python
# 1. Develop your algorithm (see SDK usage above)
# 2. Register it in the global registry
# 3. Test it locally

from rcabench_platform.v2.algorithms.spec import AlgorithmArgs
from pathlib import Path

# Create test data structure
args = AlgorithmArgs(
    dataset="test-dataset",
    datapack="test-datapack",
    input_folder=Path("/tmp/test-input"),
    output_folder=Path("/tmp/test-output")
)

# Test your algorithm
my_algo = MyCustomAlgorithm()
results = my_algo(args)
print(f"Algorithm returned {len(results)} results")
```

### 3. Batch Processing Multiple Datasets

```python
# Process multiple datasets programmatically
from rcabench_platform.v2.clients.rcabench_ import RCABenchClient
from rcabench.openapi import DatasetsApi, DtoDatasetV2SearchReq

datasets_to_process = ["dataset-1", "dataset-2", "dataset-3"]
algorithms_to_test = ["random", "baro"]

with RCABenchClient() as client:
    api = DatasetsApi(client)

    for dataset in datasets_to_process:
        for algorithm in algorithms_to_test:
            # Submit execution programmatically
            print(f"Processing {dataset} with {algorithm}")
            # Implementation would go here
```

### 4. Monitoring Long-Running Experiments

```bash
# Submit long-running experiment
rca submit-execution \
  --algorithm "complex-algorithm" \
  --dataset "large-dataset" \
  --dataset-version "v2.0" \
  --project "long-experiment" \
  --tag "production-test"

# Monitor progress (get trace ID from submit response)
rca trace "trace-id-from-submission" --timeout 3600

# Check final results
rca metrics \
  --algorithm "complex-algorithm" \
  --dataset "large-dataset" \
  --dataset-version "v2.0" \
  --tag "production-test"
```

### 5. Trace Sampling Evaluation

```bash
# Run sampling experiments on multiple datasets
python main.py sample batch \
  --sampler "random" \
  --dataset "dataset1" \
  --dataset "dataset2" \
  --rate 0.05 \
  --rate 0.1 \
  --rate 0.2 \
  --mode offline

# Generate comprehensive sampling performance report
python main.py sample perf-report \
  --dataset "dataset1" \
  --dataset "dataset2" \
  --sampler "random"

# Compare sampling strategies
python main.py sample batch \
  --sampler "random" \
  --sampler "my-custom-sampler" \
  --dataset "test-dataset" \
  --rate 0.1 \
  --mode offline \
  --mode online
```

### 6. Integrated Sampler and Algorithm Evaluation

```bash
# Run algorithms on both sampled and original data for comparison
python main.py eval batch \
  --algorithm nsigma \
  --algorithm baro \
  --dataset rcabench-train \
  --include-sampled

# This will:
# 1. Auto-detect all available sampler configurations
# 2. Run algorithms on both sampled and original data
# 3. Generate unified performance report comparing results

# Generate performance report showing both sampled and non-sampled results
python main.py eval perf-report rcabench-train --include-sampled

# The unified report will show:
# - Algorithm performance on original data (sampler.name = null)
# - Algorithm performance on sampled data (with sampler details)
# - Direct comparison of accuracy impact from sampling

# Filter to specific sampler configurations while including baseline
python main.py eval batch \
  --algorithm nsigma \
  --dataset rcabench-train \
  --include-sampled \
  --sampler random \
  --sampling-mode OFFLINE
```

### 7. Coverage Metrics Analysis

```python
# Analyze different coverage metrics to understand sampling quality
from rcabench_platform.v2.config import get_config
import polars as pl

# Read detailed performance results
config = get_config()
perf_df = pl.read_parquet(config.output / "sampler_reports" / "detailed_perf.parquet")

# Compare all three coverage metrics
coverage_analysis = perf_df.select([
    "sampler", "dataset", "sampling_rate", "mode",
    "comprehensiveness",  # API coverage (renamed from comprehensiveness)
    "path_coverage",      # Execution path coverage
    "event_coverage",     # Event coverage (traces + logs)
    "total_entry_types",  # Total API types
    "total_path_types",   # Total execution path types
    "total_event_pairs",  # Total event pairs
    (pl.col("path_coverage") - pl.col("comprehensiveness")).alias("path_vs_api_diff"),
    (pl.col("event_coverage") - pl.col("comprehensiveness")).alias("event_vs_api_diff")
])

# Find cases where granular coverage metrics are significantly lower than API coverage
interesting_cases = coverage_analysis.filter(
    (pl.col("path_vs_api_diff") < -0.1) | (pl.col("event_vs_api_diff") < -0.1)
)

print("Cases with significant coverage differences:")
print(interesting_cases)

# Analyze coverage diversity across all three metrics
diversity_analysis = perf_df.group_by(["sampler", "sampling_rate"]).agg([
    pl.col("comprehensiveness").mean().alias("avg_api_coverage"),
    pl.col("path_coverage").mean().alias("avg_path_coverage"),
    pl.col("event_coverage").mean().alias("avg_event_coverage"),
    (pl.col("total_path_types") / pl.col("total_entry_types")).mean().alias("path_complexity_ratio"),
    (pl.col("total_event_pairs") / pl.col("total_entry_types")).mean().alias("event_complexity_ratio")
])

print("\nSampler performance summary:")
print(diversity_analysis)
```

## Advanced Topics

### Custom Docker Images

You can use custom Docker images for algorithms:

```bash
rca submit-execution \
  --algorithm "myregistry.com/my-custom-algo:v1.0" \
  --dataset "my-dataset" \
  --project "custom-algo-test" \
  --tag "docker-test"
```

### Environment Variables

Pass configuration to algorithms via environment variables:

```bash
rca submit-execution \
  --algorithm "configurable-algo" \
  --dataset "my-dataset" \
  --project "config-test" \
  --tag "env-test" \
  --env "THRESHOLD=0.95" \
  --env "MAX_ITERATIONS=100"
```

### Algorithm Upload Process

For algorithms stored in Harbor registry:

1. Create algorithm folder with required files:
   - `info.toml`: Algorithm metadata
   - `Dockerfile`: Container definition
   - `entrypoint.sh`: Algorithm entry point

2. Upload to platform:
```bash
rca upload-algorithm-harbor /path/to/algorithm/folder
```

### Working with Service Dependency Graphs

The platform includes SDG (Service Dependency Graph) functionality accessible via:

```bash
python main.py sdg --help
```

### Trace Sampling Features

The platform provides comprehensive trace sampling capabilities for evaluating sampling algorithms:

#### Sampling Modes

- **Online Mode**: Each trace decision is made independently. The implementation can use various strategies (threshold-based, probability-based, etc.) and the actual sampling rate may vary from the target rate.
- **Offline Mode**: Batch sampling that considers all traces together. Typically implemented as top-N selection that sorts all traces by their scores and selects exactly the top `sampling_rate * total_traces` traces. This guarantees the exact sampling rate.

#### Performance Metrics

The sampling framework calculates the following performance metrics:

- **Controllability (RoD)**: Rate of Deviation - measures sampling rate accuracy
- **API Coverage**: API Coverage Rate based on API entry spans (renamed from "comprehensiveness")
- **Path Coverage**: Execution Path Coverage Rate - measures diversity based on complete execution paths
  - Uses BFS traversal with sorted nodes at same depth for consistent encoding
  - Generally more strict than API coverage as it considers full trace structure
  - Handles parallel calls by sorting child spans alphabetically by service:operation labels
- **Event Coverage**: Event Coverage Rate based on event pairs from traces + logs
  - Encodes traces and logs into event sequences (span events, status errors, performance degradation, log events)
  - Calculates coverage based on consecutive event pairs (2-grams)
  - Provides the most granular view of system behavior
  - Considers performance thresholds from metrics_sli.parquet for degradation detection
- **Proportion Metrics (PRO)**: Three types of proportion analysis:
  - `proportion_anomaly`: Proportion of detector-flagged spans in abnormal traces only
  - `proportion_rare`: Proportion of rare entry spans sampled (< 5% frequency)
  - `proportion_common`: Proportion of common spans (including detector spans in normal traces)
- **Runtime Performance**: Runtime per span in milliseconds
- **Actual Sampling Rate**: Achieved vs. target sampling rate

#### Coverage Metrics Comparison

The platform provides three types of coverage metrics to evaluate sampling comprehensiveness:

1. **API Coverage**: Based on entry span names (API endpoints)
   - Simple and fast to calculate
   - Good for basic coverage assessment
   - Example: 21/22 API types covered = 95.45%

2. **Path Coverage**: Based on complete execution paths using TracePicker-style encoding
   - More detailed and comprehensive
   - Considers full trace call structure and parallel execution patterns
   - Better reflects actual system behavior diversity
   - Example: 50/60 execution paths covered = 83.33%

3. **Event Coverage**: Based on event pairs from traces and logs combined
   - Most granular view of system behavior
   - Encodes traces and logs into events (span start/end, errors, performance degradation, log entries)
   - Calculates coverage based on consecutive event pairs (behavioral sequences)
   - Considers performance degradation using metrics_sli.parquet thresholds
   - Example: 5682/9361 event pairs covered = 60.70%

**Key insight**: Coverage granularity follows the pattern API > Path > Event, where each metric provides increasingly detailed views of trace diversity. Event coverage typically shows the lowest percentages but captures the most comprehensive behavioral patterns.

#### Output Structure

Sampling results are stored in:
```
{output}/sampled/{dataset}/{datapack}/{sampler}_{sampling_rate}_{mode}/
├── online.parquet or offline.parquet  # Sampling results
├── perf.parquet                        # Performance metrics
└── .finished                          # Completion marker
```
