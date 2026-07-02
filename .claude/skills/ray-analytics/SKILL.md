---
name: ray-analytics
description: Submit distributed data analysis and ML tasks to the aegis Ray cluster. Use when the user wants to analyze datapacks at scale, filter/score/aggregate parquet data across many injections, run ML training or inference on cluster, or asks about "分析数据", "跑分析", "ray submit", "datapack analysis", "batch analysis", "parquet分析", "筛选数据", "数据分析任务". Also use when the user asks how to connect to Ray, what the Ray cluster looks like, or how to read datapacks programmatically.
---

# Ray Analytics — aegis datapack analysis cluster

## Architecture

```
Local Python / ray CLI          byte-cluster (exp namespace)
┌──────────────────┐  HTTPS   ┌──────────────────────────┐
│ ray job submit   │ ───────▶ │ gateway (/api/v2/ray/)   │
│ or Python SDK    │  + JWT   │   ↓ strip_prefix         │
└──────────────────┘          │ Ray Head Pod (8265)       │
                              │   ├─ Job API             │
                              │   ├─ Dashboard           │
                              │   └─ GCS (6379)          │
                              │ Ray Worker Pod ×2         │
                              │   └─ PyArrow + S3 → rustfs│
                              └──────────────────────────┘
```

- **Image**: `opspai/ray-aegis:2.56.0-py313` (pre-installed: pyarrow, boto3, s3fs, fsspec, duckdb, aliyun pip mirror)
- **Helm subchart**: `aegislab/helm/charts/ray/` under the rcabench umbrella
- **Gateway route**: `/api/v2/ray/` → `rcabench-ray-head:8265`, auth=jwt, strip_prefix, timeout=600s
- **S3 storage**: in-cluster rustfs (S3-compatible), bucket `aegis-datapack`
- **Credentials**: from `rustfs-admin` Secret, injected as `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`AWS_ENDPOINT_URL` env vars

## How to submit a job

### Method 1: ray CLI (recommended for interactive use)

```bash
# Get JWT token
TOKEN=$(grep -A20 "^    default:" ~/.aegisctl/config.yaml | grep "token:" | head -1 | sed 's/.*token: //')

# Submit (stdout streams to terminal in real time)
ray job submit \
  --address "https://<AEGIS_PUBLIC_BASE_URL>/api/v2/ray" \
  --headers '{"Authorization": "Bearer '$TOKEN'"}' \
  --verify false \
  --working-dir . \
  -- python3 my_analysis.py
```

Without `--no-wait` (default), logs stream to your terminal like a local run. `print()` output appears in real time.

### Method 2: Python SDK (recommended for automation / scripting)

```python
import os
from ray.job_submission import JobSubmissionClient

token = os.environ["AEGIS_TOKEN"]  # or read from ~/.aegisctl/config.yaml

client = JobSubmissionClient(
    "https://<AEGIS_PUBLIC_BASE_URL>/api/v2/ray",
    headers={"Authorization": f"Bearer {token}"},
    verify=False,
)

job_id = client.submit_job(entrypoint="python3 my_analysis.py")

# Poll status
import time
while True:
    status = client.get_job_status(job_id)
    if str(status) in ("SUCCEEDED", "FAILED", "STOPPED"):
        break
    time.sleep(2)

# Get logs
print(client.get_job_logs(job_id))
```

### Method 3: curl (quick checks)

```bash
# Check cluster
curl -sk -H "Authorization: Bearer $TOKEN" \
  "https://<AEGIS_PUBLIC_BASE_URL>/api/v2/ray/api/version"

# Get job logs
curl -sk -H "Authorization: Bearer $TOKEN" \
  "https://<AEGIS_PUBLIC_BASE_URL>/api/v2/ray/api/jobs/<job_id>/logs" | jq -r .logs
```

## Writing analysis scripts

Scripts run on the Ray head pod. They have access to:
- `AWS_ENDPOINT_URL`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` env vars (pre-set)
- All packages in the image: ray, pyarrow, boto3, s3fs, duckdb, pandas, numpy

### Reading datapacks from rustfs

```python
import ray
import pyarrow.fs as pafs
import os

ray.init()

# Create S3 filesystem pointing to rustfs
fs = pafs.S3FileSystem(
    endpoint_override=os.environ["AWS_ENDPOINT_URL"],
    access_key=os.environ["AWS_ACCESS_KEY_ID"],
    secret_key=os.environ["AWS_SECRET_ACCESS_KEY"],
    region="us-east-1",
)

# Read a single datapack
ds = ray.data.read_parquet(
    "aegis-datapack/<injection-uuid>/abnormal_traces.parquet",
    filesystem=fs,
)

# Read multiple datapacks at once (glob pattern)
ds = ray.data.read_parquet(
    ["aegis-datapack/uuid1/abnormal_traces.parquet",
     "aegis-datapack/uuid2/abnormal_traces.parquet"],
    filesystem=fs,
)
```

### Common analysis patterns

```python
# Count rows
ds.count()

# Schema inspection
ds.schema()

# GroupBy aggregation
ds.groupby("ServiceName").count().take_all()

# Filter
ds.filter(lambda row: row["Duration"] > 1_000_000)

# Map transformation
ds.map(lambda row: {**row, "duration_ms": row["Duration"] / 1_000})

# Write results back to S3
result_ds.write_parquet("aegis-datapack/results/my_analysis/", filesystem=fs)

# Use DuckDB for SQL analysis
import duckdb
df = ds.to_pandas()
duckdb.sql("SELECT ServiceName, COUNT(*), AVG(Duration) FROM df GROUP BY 1 ORDER BY 2 DESC").show()
```

### Distributed processing (across workers)

```python
# Define a remote function
@ray.remote
def analyze_injection(injection_uuid):
    import pyarrow.parquet as pq
    import pyarrow.fs as pafs
    import os
    fs = pafs.S3FileSystem(
        endpoint_override=os.environ["AWS_ENDPOINT_URL"],
        access_key=os.environ["AWS_ACCESS_KEY_ID"],
        secret_key=os.environ["AWS_SECRET_ACCESS_KEY"],
        region="us-east-1",
    )
    table = pq.read_table(
        f"aegis-datapack/{injection_uuid}/abnormal_traces.parquet",
        filesystem=fs,
    )
    return {
        "uuid": injection_uuid,
        "row_count": len(table),
        "services": table.column("ServiceName").unique().tolist(),
    }

# Fan out across workers
uuids = ["uuid1", "uuid2", "uuid3", ...]
futures = [analyze_injection.remote(u) for u in uuids]
results = ray.get(futures)
```

## Datapack parquet files

Each injection UUID directory in `aegis-datapack/` contains:

| File | Content |
|------|---------|
| `abnormal_traces.parquet` | Spans during fault injection window |
| `normal_traces.parquet` | Spans during normal (pre-fault) window |
| `abnormal_metrics.parquet` | Gauge metrics during fault window |
| `normal_metrics.parquet` | Gauge metrics during normal window |
| `abnormal_metrics_histogram.parquet` | Histogram metrics during fault window |
| `abnormal_metrics_sum.parquet` | Sum/counter metrics during fault window |
| `abnormal_logs.parquet` | Logs during fault window |
| `normal_logs.parquet` | Logs during normal window |
| `injection.json` | Fault injection config + ground truth |
| `env.json` | Time windows (normal/abnormal start/end) |
| `k8s.json` | Kubernetes state snapshot |
| `converted/conclusion.parquet` | Detector output |

Parquet files are written by ClickHouse (v25.7). PyArrow reads them natively. Spark's parquet-mr cannot (STRING annotation incompatibility) — use Ray or PyArrow directly.

## Troubleshooting

- **"unauthorized"**: JWT token expired or missing. Re-extract from `~/.aegisctl/config.yaml` or `aegisctl auth login`.
- **SSL error**: Add `--verify false` (edge-proxy uses self-signed TLS).
- **"Cluster resources are not enough"**: Warning only, not fatal. Head pod has 0 CPU reserved for tasks; workers handle execution. Tasks queue and run when workers have capacity.
- **ray job logs --headers not supported**: `ray job logs` CLI doesn't accept `--headers`. Use `curl` or Python SDK for log retrieval through gateway.
- **Port-forward fallback**: For development, `kubectl port-forward svc/rcabench-ray-head 8265:8265 -n exp` skips gateway/auth.

## Helm chart location

- Subchart: `aegislab/helm/charts/ray/`
- Values override: `aegislab/manifests/byte-cluster/rcabench.values.yaml` → `ray:` section
- Dockerfile: `aegislab/helm/charts/ray/Dockerfile`
- Parent chart dependency: `aegislab/helm/Chart.yaml` → `ray` entry
