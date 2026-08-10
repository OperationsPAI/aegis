"""Datapack quality survey — runs on Ray cluster, reads all datapacks from rustfs.

For each injection UUID in aegis-datapack/:
  - reads injection.json (system, chaos_type, target, ground_truth)
  - reads abnormal_traces.parquet (span count, unique services, duration stats)
  - reads abnormal_logs.parquet row count

Outputs:
  1. Per-injection detail table (printed as TSV)
  2. Per-system × chaos_type summary (printed as formatted table)
  3. Writes results parquet to aegis-datapack/results/survey/

Usage:
  cd rcabench-platform/scripts/ray_examples

  TOKEN=$(grep -A20 "^    default:" ~/.aegisctl/config.yaml \
         | grep "token:" | head -1 | sed 's/.*token: //')

  ray job submit \
    --address "${AEGIS_SERVER}/api/v2/ray" \
    --headers "{\"Authorization\": \"Bearer $TOKEN\"}" \
    --verify false \
    --working-dir . \
    -- python3 datapack_survey.py
"""

import json
import os
import traceback

import pyarrow.fs as pafs
import pyarrow.parquet as pq
import ray


def make_fs():
    return pafs.S3FileSystem(
        endpoint_override=os.environ["AWS_ENDPOINT_URL"],
        access_key=os.environ["AWS_ACCESS_KEY_ID"],
        secret_key=os.environ["AWS_SECRET_ACCESS_KEY"],
        region="us-east-1",
    )


def list_datapacks(fs):
    """List all UUID dirs under aegis-datapack/."""
    selector = pafs.FileSelector("aegis-datapack/", recursive=False)
    entries = fs.get_file_info(selector)
    return [e.path.split("/")[-1] for e in entries if e.type.name == "Directory"]


@ray.remote
def analyze_one(uuid):
    """Analyze a single datapack. Returns a dict or None on error."""
    fs = make_fs()
    base = f"aegis-datapack/{uuid}"
    result = {"uuid": uuid}

    # --- injection.json (parse from engine_config, not top-level) ---
    try:
        with fs.open_input_stream(f"{base}/injection.json") as f:
            inj = json.loads(f.read().decode())
        ec = inj.get("engine_config") or []
        result["leaf_count"] = len(ec)
        chaos_types = sorted(set(e.get("chaos_type", "?") for e in ec))
        result["chaos_types"] = chaos_types
        result["chaos_type"] = chaos_types[0] if len(chaos_types) == 1 else ",".join(chaos_types)
        systems = sorted(set(e.get("system_type", e.get("system", "?")) for e in ec))
        result["system"] = systems[0] if systems else inj.get("category", "?")
        targets = sorted(set(e.get("target_service", "?") for e in ec))
        result["target_service"] = targets[0] if len(targets) == 1 else ",".join(targets)
        namespaces = sorted(set(e.get("namespace", "?") for e in ec))
        result["namespace"] = namespaces[0] if len(namespaces) == 1 else ",".join(namespaces)
        durations = [e.get("duration", 0) for e in ec]
        result["chaos_duration_s"] = max(durations) if durations else 0
        gt = inj.get("ground_truth") or []
        result["gt_services"] = gt[0].get("service", []) if gt else []
    except Exception:
        result["system"] = "?"
        result["chaos_type"] = "?"
        result["chaos_types"] = []
        result["target_service"] = "?"
        result["chaos_duration_s"] = 0
        result["namespace"] = "?"
        result["gt_services"] = []
        result["leaf_count"] = 0

    # --- abnormal_traces.parquet ---
    try:
        table = pq.read_table(f"{base}/abnormal_traces.parquet", filesystem=fs)
        result["abnormal_span_count"] = len(table)
        if "ServiceName" in table.column_names:
            result["unique_services"] = len(table.column("ServiceName").unique())
            result["services"] = sorted(set(table.column("ServiceName").to_pylist()))
        else:
            result["unique_services"] = 0
            result["services"] = []
        if "Duration" in table.column_names:
            durations = table.column("Duration").to_pylist()
            durations = [d for d in durations if d is not None and d > 0]
            if durations:
                result["p50_duration_ms"] = sorted(durations)[len(durations) // 2] / 1e6
                result["p99_duration_ms"] = sorted(durations)[int(len(durations) * 0.99)] / 1e6
                result["max_duration_ms"] = max(durations) / 1e6
            else:
                result["p50_duration_ms"] = 0
                result["p99_duration_ms"] = 0
                result["max_duration_ms"] = 0
        del table
    except FileNotFoundError:
        result["abnormal_span_count"] = 0
        result["unique_services"] = 0
        result["services"] = []
        result["p50_duration_ms"] = 0
        result["p99_duration_ms"] = 0
        result["max_duration_ms"] = 0
    except Exception as e:
        result["abnormal_span_count"] = -1
        result["error_traces"] = str(e)

    # --- abnormal_logs.parquet ---
    try:
        table = pq.read_table(f"{base}/abnormal_logs.parquet", filesystem=fs)
        result["abnormal_log_count"] = len(table)
        del table
    except FileNotFoundError:
        result["abnormal_log_count"] = 0
    except Exception:
        result["abnormal_log_count"] = -1

    return result


def print_summary(results):
    """Print per-system × chaos_type aggregation."""
    from collections import defaultdict

    buckets = defaultdict(lambda: {
        "count": 0, "total_spans": 0, "total_logs": 0,
        "avg_services": [],
    })

    for r in results:
        key = (r.get("system", "?"), r.get("chaos_type", "?"))
        b = buckets[key]
        b["count"] += 1
        spans = r.get("abnormal_span_count", 0)
        if spans > 0:
            b["total_spans"] += spans
        logs = r.get("abnormal_log_count", 0)
        if logs > 0:
            b["total_logs"] += logs
        b["avg_services"].append(r.get("unique_services", 0))

    print("\n" + "=" * 110)
    print(f"{'system':<18} {'chaos_type':<28} {'n':>5} {'spans':>12} {'logs':>12} "
          f"{'avg_span':>10} {'avg_svc':>8}")
    print("-" * 110)
    for (sys, ct) in sorted(buckets.keys()):
        b = buckets[(sys, ct)]
        n = b["count"]
        avg_span = b["total_spans"] // n if n else 0
        avg_svc = sum(b["avg_services"]) / n if n else 0
        print(f"{sys:<18} {ct:<28} {n:>5} {b['total_spans']:>12} {b['total_logs']:>12} "
              f"{avg_span:>10} {avg_svc:>8.1f}")

    print("-" * 110)
    total = len(results)
    total_spans = sum(r.get("abnormal_span_count", 0) for r in results if r.get("abnormal_span_count", 0) > 0)
    total_logs = sum(r.get("abnormal_log_count", 0) for r in results if r.get("abnormal_log_count", 0) > 0)
    print(f"{'TOTAL':<18} {'':<28} {total:>5} {total_spans:>12} {total_logs:>12}")
    print("=" * 110)


def main():
    ray.init()
    fs = make_fs()

    print("Listing datapacks from rustfs ...")
    uuids = list_datapacks(fs)
    print(f"Found {len(uuids)} datapacks")

    if not uuids:
        print("No datapacks found. Exiting.")
        return

    print(f"Analyzing {len(uuids)} datapacks across Ray workers ...")
    futures = [analyze_one.remote(u) for u in uuids]
    results = ray.get(futures)
    results = [r for r in results if r is not None]

    # Detail TSV
    print("\n--- Per-injection detail ---")
    header = ["uuid", "system", "chaos_type", "target_service", "namespace",
              "abnormal_span_count", "unique_services", "abnormal_log_count",
              "p50_duration_ms", "p99_duration_ms"]
    print("\t".join(header))
    for r in sorted(results, key=lambda x: (x.get("system", ""), x.get("chaos_type", ""))):
        row = [str(r.get(h, "")) for h in header]
        print("\t".join(row))

    # Summary
    print_summary(results)

    # Write parquet back to rustfs
    try:
        import pyarrow as pa
        flat = []
        for r in results:
            flat.append({k: (str(v) if isinstance(v, list) else v) for k, v in r.items()})
        table = pa.Table.from_pylist(flat)
        pq.write_table(table, "aegis-datapack/results/survey/survey.parquet", filesystem=fs)
        print("\nResults written to aegis-datapack/results/survey/survey.parquet")
    except Exception:
        traceback.print_exc()
        print("(Failed to write results parquet — stdout output above is still valid)")


if __name__ == "__main__":
    main()
