# Notes of Building Dataset

```bash
# patch detection result, convert the dataset to standard RCABench format in */converted directory
sudo -E ./cli/detector.py patch-detection

# copy the converted dataset to the rcabench-platform-v2
sudo -E ./cli/dataset_transform/make_rcabench.py run

# do some filtering strategies
sudo -E ./cli/dataset_transform/make_rcabench_filtered.py run

# build log templates using Drain3 for all datapacks (rebuild drain_ts.bin from scratch)
sudo -E ./cli/dataset_transform/make_rcabench.py build-template

# run normal conversion (templates are processed per-datapack during conversion)
sudo -E ./cli/dataset_transform/make_rcabench.py run
```

## Log Template Processing with Drain3

The system supports automatic log template extraction using Drain3 for log anomaly detection and analysis. Templates are processed per-datapack during conversion without persistent caching.

### Template Processing Features

1. **Per-Datapack Processing**: Each datapack processes its own log messages independently
2. **Drain3 State Persistence**: Global template state is maintained in `drain_ts.bin` file
3. **Service Filtering**: Automatically excludes `ts-ui-dashboard` logs from template processing
4. **Memory Efficient**: No cross-datapack template caching, reducing memory usage
5. **Deduplication**: Unique messages are extracted per-datapack before processing

### Configuration Files

- **Config**: `data/rcabench_dataset/drain_template/drain_ts.ini` - Drain3 configuration parameters
- **Persistence**: `data/rcabench_dataset/drain_template/drain_ts.bin` - Drain3 state persistence across all datapacks

### Template Processing Workflow

#### Per-Datapack Processing (during conversion)
1. **Extract Unique Messages**: Extract unique log messages from current datapack (excluding ts-ui-dashboard)
2. **Load Drain3 State**: Load existing template miner state from `drain_ts.bin`
3. **Process Messages**: Process datapack messages with Drain3 to get templates
4. **Template Mapping**: Create in-memory mapping of messages to templates
5. **Log Enhancement**: Join templates with log data to add `attr.template_id` and `attr.log_template` columns

#### Global Template Building (optional)
1. **Extract All Messages**: Scan all datapacks and extract unique messages
2. **Rebuild Drain3 State**: Process all messages to rebuild `drain_ts.bin` from scratch
3. **Template State Persistence**: Save updated template state for future datapack processing

### Template Output

Each log entry will be enhanced with:
- `attr.template_id` (UInt16): Unique identifier for the template
- `attr.log_template` (String): The extracted log template with variables replaced by placeholders

### Commands

- `build-template`: Rebuild the global Drain3 state by processing all unique messages from all datapacks
- `run`: Normal conversion process, templates are processed per-datapack using existing Drain3 state


## Anomaly detection logic documentation

This detector is mainly used for performance anomaly detection in microservice systems, identifying delay and success rate anomalies by analyzing trace data during normal and abnormal periods.

### Overview of the testing process


Data preprocessing → endpoint analysis → latency anomaly detection → success rate anomaly detection → result classification and output


```

### 1. Data preprocessing stage

#### 1.1 Trace data processing

- **Entry point identification**: Prefer `loadgenerator` service as the entry point, and fall back to `ts-ui-dashboard` if it fails

- **Statistical calculation**: The following metrics are calculated for each endpoint:

- Average delay (avg_duration)

- P90 delay (p90_duration)

- P95 delay (p95_duration)

- P99 delay (p99_duration)

- HTTP status code distribution

- Success rate (based on status code 200)

#### 1.2 Endpoint deduplication

- Use the `extract_path` function to extract and deduplicate paths for similar API endpoints

- Combine endpoints with the same path pattern into statistics

### 2. Endpoint anomaly analysis

#### 2.1 Endpoint Classification

The system divides endpoints into two categories and handles them differently:

**A. Endpoints present in normal data**

- Perform complete latency and success rate anomaly detection

- Comparative analysis based on historical baseline data

**B. New endpoint (not present in normal data)**

- Detection using absolute threshold

- Relative rate of change analysis cannot be performed

### 3. Latency Anomaly Detection

#### 3.1 Detector Configuration (EnhancedLatencyDetector)

**Hard timeout threshold**

- `hard_timeout_threshold: 15.0` seconds

- If the value exceeds this value, it will be directly judged as a serious abnormality

**Absolute anomaly threshold** (used when baseline latency is small)

- Average latency: 2.0 seconds

- P90 latency: 4.0 seconds

- P95 latency: 4.5 seconds

- P99 latency: `5.0` seconds

**Relative fold threshold** (fold relative to baseline)

- Average latency: 3.0 times

- P90 latency: `6.0` times

- P95 latency: `7.5` times

- P99 latency: `8.0` times

**Baseline Filtering Rules**

- Baseline average latency > `1.0` seconds: Skip detection (to avoid false positives in high latency environments)

- Baseline P99 latency > `5.0` seconds: Skip detection (unstable baseline)

#### 3.2 Detection rule priority

1. **Rule anomaly detection** (highest priority)

- Hard timeout: delay > 15 seconds

- Adaptive rule anomalies (determined by the detector's internal logic)

2. **Baseline Filtering**

- Filter out cases where baseline latency is too high

3. **Adaptive Threshold Detection**

- Small baseline (< 0.5 seconds): use absolute threshold

- Large baseline (≥ 0.5 sec): Use relative magnification

#### 3.3 Delay indicator detection method

**Average latency (avg)**

- Use the average of all normal period latency data as a baseline

- Check whether the current average delay exceeds the threshold

**Percentile latency (p90/p95/p99)**

- P90: Use the 85%-95% percentile interval of normal data as the baseline

- P95: Use the 90%-99% percentile interval of normal data as the baseline

- P99: Use the 95%-100% quantile interval of normal data as the baseline

### 4. Success rate anomaly detection

#### 4.1 Detector Configuration (SuccessRateDetector)

**Basic Parameters**

- `min_normal_count: 10` - minimum number of requests during normal period

- `min_abnormal_count: 5` - minimum number of requests during an abnormal period

- `min_rate_drop: 0.03` - minimum success rate drop (3%)

- `significance_threshold: 0.05` - Statistical significance threshold (p-value)

- `min_relative_drop: 0.1` - minimum relative drop ratio (10%)

#### 4.2 Statistical detection method

Z-TEST


pooled_p = (normal_rate * normal_count + abnormal_rate * abnormal_count) / (normal_count + abnormal_count)


se = sqrt(pooled_p * (1 - pooled_p) * (1/normal_count + 1/abnormal_count))


z_stat = |abnormal_rate - normal_rate| / se


p_value = 2 * (1 - Φ(|z_stat|))


```

**Abnormal determination conditions** (must be met at the same time)

1. Success rate decreased by > 3%

2. p value < 0.05 (statistically significant)

3. Relative decrease > 10% of normal success rate

#### 4.3 Severity Rating

- **CRITICAL**: Success rate decreased by > 20%

- **HIGH**: Success rate decreased by > 10%

- **MEDIUM**: Success rate decreased by > 5%

### 5. New endpoint detection

For endpoints that are not present in normal data, a simplified threshold detection is used:

#### 5.1 Delay Threshold

- Average latency > `3.0` seconds

- P90 latency > `7.0` seconds

- P95 latency > `8.0` seconds

- P99 latency > `10.0` seconds

- Hard timeout > `15.0` seconds

#### 5.2 Success rate threshold

- Success rate < `90%` is considered abnormal

### 6. Problem classification statistics

Detected issues are categorized by type:

- **latency_only**: latency exception only

- **success_rate_only**: Only the success rate is abnormal

- **both_latency_and_success_rate**: Both latency and success rate are abnormal

- **no_issues**: No anomalies detected

### 7. Output results

#### 7.1 Conclusion file (conclusion.csv)

Contains detailed analysis results for each endpoint:

- Endpoint name and problem description

- Comparison of various indicators during normal/abnormal periods

- Specific parameters for anomaly detection

### 8. Detection characteristics

1. **Multi-dimensional detection**: Simultaneous detection of latency (4 indicators) and success rate
2. **Adaptive Threshold**: Dynamically adjust detection strategy based on baseline conditions
3. **Statistical rigor**: Success rate detection uses statistical significance test
4. **False Positive Control**: Reduce false positives through baseline filtering and multiple conditions
5. **New endpoint processing**: There is a dedicated detection logic for newly emerging endpoints
