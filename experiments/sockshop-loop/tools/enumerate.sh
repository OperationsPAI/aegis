#!/usr/bin/env bash
# Walk the guided-inject enumeration tree for one (system, namespace) and dump
# every reachable (app, chaos_type, leaf-params) tuple as one candidate per
# line of JSONL on stdout. Each line:
#   {system, namespace, app, chaos_type, params:{...}, container?}
#
# Usage: enumerate.sh <system> <namespace>
# Example: enumerate.sh sockshop sockshop1
set -euo pipefail

SYS="${1:?system required}"
NS="${2:?namespace required}"
AEGISCTL="${AEGISCTL:-/tmp/aegisctl}"
TMP=$(mktemp -d); trap "rm -rf $TMP" EXIT

guided() {
  # Pass extra cli args (--app, --chaos-type, etc) and get the JSON response
  "$AEGISCTL" inject guided --reset-config --no-save-config \
    --config "$TMP/probe.yaml" \
    --system-type "$SYS" --namespace "$NS" \
    --output json --quiet "$@" 2>/dev/null
}

# Stage 1: apps
apps=$(guided | jq -r '.next[]? | select(.name=="app") | .options[].value' | sort -u)

for app in $apps; do
  # Stage 2: chaos types for this app
  ctypes=$(guided --app "$app" | jq -r '.next[]? | select(.name=="chaos_type") | .options[].value' | sort -u)
  for ctype in $ctypes; do
    # Stage 3: examine what next[] says
    resp=$(guided --app "$app" --chaos-type "$ctype")
    stage=$(jq -r '.stage // "unknown"' <<<"$resp")
    next=$(jq -c '.next // []' <<<"$resp")

    # If stage says ready_to_apply, the chaos_type itself is a complete
    # candidate (e.g. PodFailure / PodKill — only optional duration left).
    if [[ "$stage" == "ready_to_apply" ]]; then
      jq -n -c --arg sys "$SYS" --arg ns "$NS" --arg app "$app" --arg ct "$ctype" \
        '{system:$sys, namespace:$ns, app:$app, chaos_type:$ct, params:{}}'
      continue
    fi

    n_count=$(jq 'length' <<<"$next")
    if [[ "$n_count" -eq 0 ]]; then
      jq -n -c --arg sys "$SYS" --arg ns "$NS" --arg app "$app" --arg ct "$ctype" \
        '{system:$sys, namespace:$ns, app:$app, chaos_type:$ct, params:{}}'
      continue
    fi

    # Look at first required next field
    field_name=$(jq -r '.[0].name' <<<"$next")
    field_kind=$(jq -r '.[0].kind' <<<"$next")
    field_required=$(jq -r '.[0].required // false' <<<"$next")
    n_options=$(jq '.[0].options | length // 0' <<<"$next")

    if [[ "$n_options" -eq 0 ]]; then
      if [[ "$field_required" == "false" ]]; then
        # Optional and no enumerable options — treat as terminal
        jq -n -c --arg sys "$SYS" --arg ns "$NS" --arg app "$app" --arg ct "$ctype" \
          '{system:$sys, namespace:$ns, app:$app, chaos_type:$ct, params:{}}'
      fi
      # Required but no options → no traffic / no targets; skip
      continue
    fi

    case "$field_kind" in
      enum)
        # Each option becomes a candidate (with that one field set)
        jq -c --arg sys "$SYS" --arg ns "$NS" --arg app "$app" --arg ct "$ctype" --arg fld "$field_name" \
          '.[0].options[] | {system:$sys, namespace:$ns, app:$app, chaos_type:$ct, params:{($fld): .value}}' <<<"$next"
        ;;
      object_ref)
        # Use metadata as params (e.g. method_ref → {class, method})
        jq -c --arg sys "$SYS" --arg ns "$NS" --arg app "$app" --arg ct "$ctype" \
          '.[0].options[] | {system:$sys, namespace:$ns, app:$app, chaos_type:$ct, params:.metadata}' <<<"$next"
        ;;
      *)
        # Unknown kind: emit as-is with a placeholder note
        jq -n -c --arg sys "$SYS" --arg ns "$NS" --arg app "$app" --arg ct "$ctype" --arg k "$field_kind" \
          '{system:$sys, namespace:$ns, app:$app, chaos_type:$ct, params:{}, _note:("unknown kind: " + $k)}'
        ;;
    esac
  done
done
