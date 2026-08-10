# Specifications

## RCA Algorithm Specification

An instance of an RCA algorithm class is initialized with its configuration (hyperparameters).

The instance takes arguments and returns a list of answers.

The algorithm arguments contain
+ dataset name
+ datapack name
+ input directory containing the data files of the datapack
+ output directory for storing intermediate results

The algorithm answer is a predicted root cause with level, name and rank.

## Trace Sampler Specification

An instance of a trace sampler class is initialized with its configuration (hyperparameters).

The instance takes sampler arguments and returns a list of sample results.

The sampler arguments contain
+ dataset name
+ datapack name
+ input directory containing the data files of the datapack
+ output directory for storing intermediate results
+ sampling rate (float between 0.0 and 1.0)
+ sampling mode (online or offline)

The sample result contains a trace_id and its sample_score (weight for sampling).

### Sampling Modes

+ **Online Mode**: Returns all traces with their sampling scores, no limit on count
+ **Offline Mode**: Limited by sampling rate, sorts traces by score and keeps top traces

### Performance Metrics

Sampler performance is evaluated using the following metrics:

+ **Controllability (RoD)**: Rate of Deviation = $|((N_s - N_e) / N_e)|$
  - $N_s$: actual sampled count
  - $N_e$: expected sampled count
+ **API Coverage**: API Coverage Rate = $N_t' / N_t$
  - $N_t$': number of sampled trace types (entry spans)
  - $N_t$: total number of trace types
+ **Path Coverage**: Execution Path Coverage Rate = $N_p' / N_p$
  - $N_p$': number of sampled execution path types
  - $N_p$: total number of execution path types
  - Uses BFS traversal with sorted nodes at same depth for consistent encoding
+ **Deduplicated Path Coverage**: Execution Path Coverage Rate with parallel span deduplication = $N_{pd}' / N_{pd}$
  - $N_{pd}$': number of sampled deduplicated execution path types
  - $N_{pd}$: total number of deduplicated execution path types
  - Removes duplicate spans at the same level to handle parallel calls
  - Uses BFS traversal with deduplication at each depth level for cleaner path patterns
+ **Event Coverage**: Event Coverage Rate = $N_e' / N_e$
  - $N_e$': number of sampled event pairs (2-grams)
  - $N_e$: total number of event pairs
  - Events are encoded from traces and logs combined
  - Includes span events, status errors, performance degradation, and log events
+ **Unique Trace Coverage**: Unique Trace Pattern Coverage Rate = $N_u' / N_u$
  - $N_u$': number of sampled unique trace patterns
  - $N_u$: total number of unique trace patterns
  - Each trace is represented as a set of event pairs
  - Measures diversity of execution patterns captured
+ **Span Coverage**: Span Type Coverage Rate = $N_s' / N_s$
  - $N_s$': number of sampled unique span types (service_name + span_name)
  - $N_s$: total number of unique span types
  - Also reports total span count and sampled span count
+ **Proportion (PRO)**: Three proportion metrics
  - PRO_anomaly: proportion of detector-flagged spans in abnormal traces only
  - PRO_rare: proportion of rare entry spans sampled (< 5% frequency)
  - PRO_common: proportion of common spans (including detector spans in normal traces)
+ **Ground Truth Trace Proportion**: Proportion of ground truth related traces in sampled abnormal traces
  - Uses injection.json to identify fault-related services
  - Only considers abnormal traces for calculation
  - Measures sampler's ability to capture fault-relevant traces
+ **Balance (CV)**: Coefficient of Variation of trace type distribution = $CV = \sqrt{\frac{1}{N_t}\sum_{i=0}^{N_t}(n_i-\bar{n})^2}/\bar{n}$
  - $N_t$: number of trace types in sampled data
  - $n_i$: count of type i in sampled data
  - $\bar{n}$: average count across all types
  - Lower CV indicates more balanced distribution across trace types
+ **Shannon Entropy**: Information density of sampled trace distribution = $H(X) = -\sum_{i=1}^{n} p(x_i) \log_2 p(x_i)$
  - $n$: number of different trace patterns in sampled data (from event encoding)
  - $p(x_i)$: proportion of trace pattern i in sampled traces
  - Higher entropy indicates more uniform and diverse trace pattern distribution
  - Calculated as part of event coverage analysis using unique trace patterns
+ **Benefit-Cost Ratio**: Efficiency of unique pattern discovery = $\frac{\text{Unique Trace Patterns Discovered}}{\text{Actual Sample Count}}$
  - Uses unique trace patterns from event encoding as "benefit"
  - Actual sample count as "cost"
  - Higher ratio indicates more efficient discovery of diverse trace patterns
  - Calculated as part of event coverage analysis
+ **Intra-Sample Average Dissimilarity**: Diversity of sampled trace collection = $\frac{\sum_{i=1}^{N} \sum_{j=i+1}^{N} (1 - JaccardSimilarity(T_i, T_j))}{N(N-1)/2}$
  - $N$: number of traces in sampled set
  - $T_i, T_j$: trace pairs represented as sets of event pairs (from event encoding)
  - Measures average pairwise dissimilarity within the sampled trace collection
  - Higher values indicate better diversity (traces are more dissimilar to each other)
  - Directly quantifies the effectiveness of diversity-aware sampling algorithms like DPP
  - Value range: [0, 1], where 0 = all traces identical, 1 = all traces completely different
+ **Average Anomaly Score**: Mean anomaly score per trace in sampled set = $\frac{\sum_{i=1}^{N} S_i}{N}$
  - $N$: number of traces in sampled set
  - $S_i$: anomaly score for trace i = $E_i \times 5 + P_i + L_i$
  - $E_i$: number of error spans in trace i (spans with status_code = "Error")
  - $P_i$: performance degradation score (1/2/3 based on root span P90 threshold ratio: 1.5x/3x/5x)
  - $L_i$: log level score (WARN: +1, ERROR/SEVERE: +2 each per log entry)
  - Higher values indicate traces with more anomalous characteristics
  - Useful for evaluating sampling algorithms' ability to capture problematic traces
+ **Runtime**: Algorithm runtime per span in milliseconds
+ **Actual Sampling Rate**: Achieved sampling rate

#### Coverage Metrics Requirements

All coverage metrics (Path Coverage, Deduplicated Path Coverage, and Event Coverage) require:
- **Root Span Validation**: Only traces with a valid loadgenerator root span are processed
  - Root span must have `service_name == "loadgenerator"`
  - Root span must have `parent_span_id` as null or empty string
  - Traces without valid loadgenerator root spans are excluded from coverage calculations
- **Consistent Trace Processing**: Ensures all coverage metrics use the same trace validation logic

#### Coverage Metrics Comparison

- **API Coverage**: Simple coverage based on entry span names (API endpoints)
  - Fast to calculate and good for basic assessment
- **Path Coverage**: Advanced coverage based on complete execution paths using TracePicker-style encoding
  - Handles parallel calls by sorting at same depth
  - Provides more detailed insight into trace structure diversity
  - Generally more strict than API coverage as multiple paths can share the same entry point
- **Deduplicated Path Coverage**: Enhanced path coverage that removes parallel span duplicates
  - Removes duplicate spans at the same level to focus on core execution patterns
  - Better reflects unique execution flows without parallel call noise
  - Useful for understanding essential system behavior patterns
  - Generally shows higher coverage than regular path coverage due to deduplication
- **Event Coverage**: Most granular coverage based on event sequences from traces and logs
  - Encodes traces and logs into events (spans, errors, performance issues, log entries)
  - Calculates coverage using consecutive event pairs (2-grams)
  - Considers performance degradation using metrics_sli.parquet thresholds
  - Provides the most comprehensive view of system behavior patterns
  - Generally the most strict coverage metric, showing lowest percentages
  - **Also calculates Shannon entropy and benefit-cost ratio** as part of this analysis
- **Unique Trace Coverage**: Coverage based on unique trace patterns
  - Each trace is represented as a set of event pairs (from Event Coverage)
  - Measures how many different trace patterns are captured in samples
  - Provides insight into behavioral diversity beyond individual event coverage
  - Used for Shannon entropy and benefit-cost ratio calculations

#### Additional Metrics

- **Ground Truth Trace Proportion**: Measures fault-targeting capability
  - Parses injection.json to identify ground truth services involved in faults
  - Excludes mysql service from ground truth analysis
  - For single service faults: finds traces containing the fault service
  - For multi-service faults: finds traces with call relationships between fault services
  - Only considers abnormal traces to focus on fault-phase behavior
  - Higher proportion indicates better fault-relevant trace capture

- **Balance (CV)**: Measures distribution uniformity across trace types
  - Based on TracePicker paper's balance metric using Coefficient of Variation
  - Uses entry span types (root trace types) as trace type classification
  - CV = 0.0 indicates perfect balance (all types have equal sample counts)
  - Higher CV values indicate more unbalanced distribution
  - Important for ensuring diverse trace type representation in samples

- **Shannon Entropy**: Measures information density in sampled trace pattern distribution
  - Uses standard Shannon entropy formula from information theory
  - Based on trace pattern proportions in sampled data (using event encoding patterns)
  - Higher entropy indicates more uniform and diverse distribution of execution patterns
  - Calculated during event coverage analysis for efficiency
  - Entropy = 0 when only one trace pattern is sampled
  - Maximum entropy achieved when all trace patterns are equally represented
  - More granular than entry span-based metrics as it considers complete execution patterns

- **Benefit-Cost Ratio**: Measures sampling efficiency for unique pattern discovery
  - Benefit: number of unique trace patterns discovered (from event encoding)
  - Cost: actual number of traces sampled
  - Higher ratio indicates more efficient discovery of diverse execution patterns
  - Calculated during event coverage analysis for efficiency
  - Helps evaluate whether sampling strategy effectively captures behavioral diversity
  - Ratio = 1.0 means every sampled trace has a unique pattern (maximum diversity)
  - Ratio < 1.0 indicates some redundancy in sampled patterns
  - Provides insight into sampling efficiency beyond simple coverage metrics

- **Intra-Sample Average Dissimilarity**: Measures internal diversity of sampled trace collection
  - Quantifies how dissimilar traces are to each other within the sampled set
  - Uses Jaccard dissimilarity between trace event pair sets: D(Ti, Tj) = 1 - Jaccard(Ti, Tj)
  - Calculates average pairwise dissimilarity across all trace pairs in sample
  - Directly evaluates effectiveness of diversity-aware sampling algorithms (e.g., DPP)
  - Higher values indicate better diversity - traces are more different from each other
  - Can be used for A/B testing: compare diversity-aware vs. quality-only sampling
  - Complements coverage metrics by focusing on internal sample diversity rather than coverage breadth
  - Essential metric for validating diversity algorithms in trace sampling

## Data Specification

### Dataset and Datapack

A **dataset** is a collection of datapacks.

A **datapack** is a collection of data files (traces, metrics, logs, json, txt, ...).

A datapack represents the telemetry data of a single fault case in a microservice system.

The datapacks in the same dataset must have compatible file structures.

A dataset must have at least two metadata files:

+ `index.parquet` - list of datapacks in the dataset
+ `labels.parquet` - list of ground truth labels for the datapacks

### File System Structure

The metadata files of a dataset is stored in `{ROOT}/meta/{dataset_name}/`.

The datapacks of a dataset are stored in `{ROOT}/data/{dataset_name}/`.

A datapack is stored in `{ROOT}/data/{dataset_name}/{datapack_name}/`.

We access the files in a datapack through POSIX filesystem API.

The size of a dataset may be very large, so we have to use network-based storages like NFS or JuiceFS to store the data files.

### File Contents

#### Traces

Traces file contains a time series of spans.

|     Column     |   Type   | Description                                              |
| :------------: | :------: | :------------------------------------------------------- |
|      time      | datetime | start time of a span in UTC                              |
|    trace_id    |  string  | unique identifier of a trace (a trace groups many spans) |
|    span_id     |  string  | unique identifier of a span                              |
| parent_span_id |  string  | identifier of the parent span (for trace hierarchy)      |
|  service_name  |  string  | name of the service that generated the span              |
|   span_name    |  string  | name of the operation represented by the span            |
|    duration    |  uint64  | duration of a span in nanoseconds                        |
|     attr.*     |    *     | other attributes of a span                               |

#### Metrics

Metrics file contains a time series of metric values.

|    Column    |   Type   | Description                                         |
| :----------: | :------: | :-------------------------------------------------- |
|     time     | datetime | UTC timestamp of a metric value                     |
|    metric    |  string  | name of the metric value                            |
|    value     | float64  | value of the metric value                           |
| service_name |  string  | name of the service that generated the metric value |
|    attr.*    |    *     | other attributes of a metric value                  |

#### Logs

Logs file contains a time series of log events.

|    Column    |   Type   | Description                                      |
| :----------: | :------: | :----------------------------------------------- |
|     time     | datetime | UTC timestamp of a log event                     |
|   trace_id   |  string  | unique identifier of a trace                     |
|   span_id    |  string  | unique identifier of a span                      |
| service_name |  string  | name of the service that generated the log event |
|    level     |  string  | log level (e.g., INFO, ERROR)                    |
|   message    |  string  | log message                                      |
|    attr.*    |    *     | other attributes of a log event                  |
