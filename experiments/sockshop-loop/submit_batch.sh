#!/usr/bin/env bash
# Submit a batch of candidates in parallel. Each submission writes one line to
# runs.jsonl: {ts, candidate_id, group_id, trace_id, task_id, allocated_namespace}.
# Usage: submit_batch.sh c01 c02 c03 ...
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNS="$ROOT/runs.jsonl"
LOGDIR="$ROOT/logs"
mkdir -p "$LOGDIR"

pids=()
for cid in "$@"; do
  (
    out=$(bash "$ROOT/submit_one.sh" "$cid" 2>"$LOGDIR/$cid.err") || {
      echo "{\"ts\":\"$(date -Iseconds)\",\"candidate_id\":\"$cid\",\"error\":\"submit_failed\"}" >>"$RUNS"
      exit 1
    }
    line=$(jq -c --arg cid "$cid" --arg ts "$(date -Iseconds)" \
      '{ts:$ts, candidate_id:$cid, group_id:.group_id, trace_id:.items[0].trace_id, task_id:.items[0].task_id, allocated_namespace:.items[0].allocated_namespace}' <<<"$out")
    echo "$line" >>"$RUNS"
    echo "$cid -> $(jq -r '.allocated_namespace' <<<"$line") trace=$(jq -r '.trace_id' <<<"$line")"
  ) &
  pids+=($!)
  # Stagger by 1s to avoid all 10 hitting the alloc mutex simultaneously
  sleep 1
done

for p in "${pids[@]}"; do wait "$p" || true; done
