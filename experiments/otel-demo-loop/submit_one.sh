#!/usr/bin/env bash
# Submit one otel-demo candidate via aegisctl inject guided --apply --auto --allow-bootstrap.
# Lets the server pick a free deployed slot and bootstrap a fresh one if pool empty.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CANDS="$ROOT/candidates_round1.json"
AEGISCTL="${AEGISCTL:-/tmp/aegisctl}"

cid="${1:?candidate id required}"
cand=$(jq -c --arg id "$cid" '.candidates[] | select(.id==$id)' "$CANDS")
[[ -n "$cand" ]] || { echo "$cid not found" >&2; exit 2; }

system=$(jq -r '.system' "$CANDS")
ped_name=$(jq -r '.pedestal.name' "$CANDS")
ped_tag=$(jq -r '.pedestal.tag' "$CANDS")
bench_name=$(jq -r '.benchmark.name' "$CANDS")
bench_tag=$(jq -r '.benchmark.tag' "$CANDS")
interval=$(jq -r '.defaults.interval' "$CANDS")
pre_dur=$(jq -r '.defaults.pre_duration' "$CANDS")

app=$(jq -r '.app' <<<"$cand")
ctype=$(jq -r '.chaos_type' <<<"$cand")
duration_override=$(jq -r '.duration_override // empty' <<<"$cand")

args=(
  inject guided --apply --auto --allow-bootstrap
  --reset-config --no-save-config --non-interactive
  --output json
  --system-type "$system"
  --pedestal-name "$ped_name" --pedestal-tag "$ped_tag"
  --benchmark-name "$bench_name" --benchmark-tag "$bench_tag"
  --interval "$interval" --pre-duration "$pre_dur"
  --chaos-type "$ctype" --app "$app"
  --skip-stale-check
)
[[ -n "$duration_override" ]] && args+=(--duration "$duration_override")

case "$ctype" in
  HTTPRequestDelay|HTTPResponseDelay)
    args+=(--route "$(jq -r '.params.route' <<<"$cand")")
    args+=(--http-method "$(jq -r '.params.http_method' <<<"$cand")")
    args+=(--target-service "$(jq -r '.params.target_service' <<<"$cand")")
    args+=(--delay-duration "$(jq -r '.params.delay_duration' <<<"$cand")")
    ;;
  HTTPRequestAbort|HTTPResponseAbort)
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
  NetworkLoss)
    args+=(--target-service "$(jq -r '.params.target_service' <<<"$cand")")
    args+=(--loss "$(jq -r '.params.loss' <<<"$cand")")
    args+=(--correlation "$(jq -r '.params.correlation' <<<"$cand")")
    args+=(--direction "$(jq -r '.params.direction' <<<"$cand")")
    ;;
  JVMException)
    args+=(--class "$(jq -r '.params.class' <<<"$cand")")
    args+=(--method "$(jq -r '.params.method' <<<"$cand")")
    args+=(--exception-opt "$(jq -r '.params.exception_opt' <<<"$cand")")
    ;;
  DNSError)
    args+=(--domain "$(jq -r '.params.domain' <<<"$cand")")
    ;;
  PodFailure|PodKill) : ;;
  *) echo "unsupported chaos_type $ctype" >&2; exit 2 ;;
esac

"$AEGISCTL" "${args[@]}"
