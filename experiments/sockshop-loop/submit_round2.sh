#!/usr/bin/env bash
# Submit one round-2 candidate via aegisctl inject guided.
# Round 2 candidates carry full param specs (class+method, route, etc.) so we
# don't pass --container blindly; container only goes to JVM* / Stress* if the
# chart needs it.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CANDS="$ROOT/candidates_round2.json"
AEGISCTL="${AEGISCTL:-/tmp/aegisctl}"

cid="${1:?candidate id required}"

cand=$(jq -c --arg id "$cid" '.candidates[] | select(.id==$id)' "$CANDS")
[[ -n "$cand" ]] || { echo "candidate $cid not found" >&2; exit 2; }

system=$(jq -r '.system' "$CANDS")
ped_name=$(jq -r '.pedestal.name' "$CANDS")
ped_tag=$(jq -r '.pedestal.tag' "$CANDS")
bench_name=$(jq -r '.benchmark.name' "$CANDS")
bench_tag=$(jq -r '.benchmark.tag' "$CANDS")
interval=$(jq -r '.defaults.interval' "$CANDS")
pre_dur=$(jq -r '.defaults.pre_duration' "$CANDS")
container=$(jq -r '.defaults.container // empty' "$CANDS")

ns=$(jq -r '.ns' <<<"$cand")
app=$(jq -r '.app' <<<"$cand")
ctype=$(jq -r '.chaos_type' <<<"$cand")
duration_override=$(jq -r '.duration_override // empty' <<<"$cand")

args=(
  inject guided --apply
  --reset-config --no-save-config --non-interactive
  --output json
  --system-type "$system"
  --pedestal-name "$ped_name" --pedestal-tag "$ped_tag"
  --benchmark-name "$bench_name" --benchmark-tag "$bench_tag"
  --interval "$interval" --pre-duration "$pre_dur"
  --chaos-type "$ctype" --app "$app"
  --namespace "$ns"
  --skip-restart-pedestal --skip-stale-check
)
[[ -n "$duration_override" ]] && args+=(--duration "$duration_override")

# Per-chaos_type params extracted from the candidate's params map.
case "$ctype" in
  JVMException)
    args+=(--container "$container")
    args+=(--class "$(jq -r '.params.class' <<<"$cand")")
    args+=(--method "$(jq -r '.params.method' <<<"$cand")")
    args+=(--exception-opt "$(jq -r '.params.exception_opt' <<<"$cand")")
    ;;
  JVMLatency)
    args+=(--container "$container")
    args+=(--class "$(jq -r '.params.class' <<<"$cand")")
    args+=(--method "$(jq -r '.params.method' <<<"$cand")")
    args+=(--latency-duration "$(jq -r '.params.latency_duration' <<<"$cand")")
    ;;
  JVMReturn)
    args+=(--container "$container")
    args+=(--class "$(jq -r '.params.class' <<<"$cand")")
    args+=(--method "$(jq -r '.params.method' <<<"$cand")")
    args+=(--return-value-opt "$(jq -r '.params.return_value_opt' <<<"$cand")")
    args+=(--return-type "$(jq -r '.params.return_type' <<<"$cand")")
    ;;
  JVMCPUStress)
    args+=(--container "$container")
    args+=(--class "$(jq -r '.params.class' <<<"$cand")")
    args+=(--method "$(jq -r '.params.method' <<<"$cand")")
    args+=(--cpu-count "$(jq -r '.params.cpu_count' <<<"$cand")")
    ;;
  JVMMemoryStress)
    args+=(--container "$container")
    args+=(--class "$(jq -r '.params.class' <<<"$cand")")
    args+=(--method "$(jq -r '.params.method' <<<"$cand")")
    args+=(--memory-size "$(jq -r '.params.memory_size' <<<"$cand")")
    args+=(--mem-type "$(jq -r '.params.mem_type // "heap"' <<<"$cand")")
    ;;
  HTTPRequestDelay)
    args+=(--route "$(jq -r '.params.route' <<<"$cand")")
    args+=(--http-method "$(jq -r '.params.http_method' <<<"$cand")")
    args+=(--target-service "$(jq -r '.params.target_service' <<<"$cand")")
    args+=(--delay-duration "$(jq -r '.params.delay_duration' <<<"$cand")")
    ;;
  HTTPResponseAbort)
    args+=(--route "$(jq -r '.params.route' <<<"$cand")")
    args+=(--http-method "$(jq -r '.params.http_method' <<<"$cand")")
    args+=(--target-service "$(jq -r '.params.target_service' <<<"$cand")")
    ;;
  NetworkDelay)
    args+=(--target-service "$(jq -r '.params.target_service' <<<"$cand")")
    args+=(--latency "$(jq -r '.params.latency' <<<"$cand")")
    args+=(--jitter "$(jq -r '.params.jitter' <<<"$cand")")
    args+=(--correlation "$(jq -r '.params.correlation' <<<"$cand")")
    args+=(--direction "$(jq -r '.params.direction' <<<"$cand")")
    ;;
  DNSError)
    args+=(--domain "$(jq -r '.params.domain' <<<"$cand")")
    ;;
  PodFailure) : ;;
  *) echo "unsupported chaos_type $ctype" >&2; exit 2 ;;
esac

"$AEGISCTL" "${args[@]}"
