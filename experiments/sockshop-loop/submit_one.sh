#!/usr/bin/env bash
# Submit one candidate via aegisctl inject guided --apply --auto --allow-bootstrap.
# Usage: submit_one.sh <candidate.json>
# Reads global system/pedestal/benchmark/defaults from candidates.json and a single
# candidate row passed as a JSON object on stdin or in $1.
#
# Prints submit response JSON to stdout (server returns trace_id + allocated_namespace).
# Exit non-zero on submit failure.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CANDS="$ROOT/candidates.json"
AEGISCTL="${AEGISCTL:-/tmp/aegisctl}"

cand_id="${1:?candidate id required}"
ns="${2:-}"  # optional explicit namespace; if empty, falls back to --auto --allow-bootstrap

cand=$(jq -c --arg id "$cand_id" '.candidates[] | select(.id==$id)' "$CANDS")
[[ -n "$cand" ]] || { echo "candidate $cand_id not found" >&2; exit 2; }

system=$(jq -r '.system' "$CANDS")
ped_name=$(jq -r '.pedestal.name' "$CANDS")
ped_tag=$(jq -r '.pedestal.tag' "$CANDS")
bench_name=$(jq -r '.benchmark.name' "$CANDS")
bench_tag=$(jq -r '.benchmark.tag' "$CANDS")
interval=$(jq -r '.defaults.interval' "$CANDS")
pre_dur=$(jq -r '.defaults.pre_duration' "$CANDS")
duration=$(jq -r '.defaults.duration // empty' "$CANDS")  # default 5min in guidedcli; only pass if explicitly set

app=$(jq -r '.app' <<<"$cand")
ctype=$(jq -r '.chaos_type' <<<"$cand")

args=(
  inject guided --apply
  --reset-config --no-save-config --non-interactive
  --output json
  --system-type "$system"
  --pedestal-name "$ped_name" --pedestal-tag "$ped_tag"
  --benchmark-name "$bench_name" --benchmark-tag "$bench_tag"
  --interval "$interval" --pre-duration "$pre_dur"
  --chaos-type "$ctype" --app "$app"
  --skip-restart-pedestal --skip-stale-check
)
if [[ -n "$ns" ]]; then
  args+=(--namespace "$ns")
else
  args+=(--auto --allow-bootstrap)
fi
# Only pass --duration if explicitly overridden in candidates.json defaults
[[ -n "$duration" ]] && args+=(--duration "$duration")

container=$(jq -r '.defaults.container // empty' "$CANDS")

# Fault-type-specific params
case "$ctype" in
  CPUStress)
    [[ -n "$container" ]] && args+=(--container "$container")
    args+=(--cpu-load "$(jq -r '.params.cpu_load' <<<"$cand")")
    args+=(--cpu-worker "$(jq -r '.params.cpu_worker' <<<"$cand")")
    ;;
  MemoryStress)
    [[ -n "$container" ]] && args+=(--container "$container")
    args+=(--memory-size "$(jq -r '.params.memory_size' <<<"$cand")")
    args+=(--mem-worker "$(jq -r '.params.mem_worker' <<<"$cand")")
    args+=(--mem-type "$(jq -r '.params.mem_type' <<<"$cand")")
    ;;
  NetworkDelay)
    args+=(--latency "$(jq -r '.params.latency' <<<"$cand")")
    args+=(--jitter "$(jq -r '.params.jitter' <<<"$cand")")
    args+=(--correlation "$(jq -r '.params.correlation' <<<"$cand")")
    args+=(--direction "$(jq -r '.params.direction' <<<"$cand")")
    args+=(--target-service "$(jq -r '.params.target_service' <<<"$cand")")
    ;;
  PodFailure) : ;;
  *) echo "unsupported chaos_type $ctype" >&2; exit 2 ;;
esac

"$AEGISCTL" "${args[@]}"
