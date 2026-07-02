#!/usr/bin/env bash
# loop-supervisor.sh — watchdog that restarts dead injection loops.
# Run periodically (cron every 15m). The loops self-kill after
# --max-consecutive-failures consecutive engine rc!=0 (issue #377); this
# restarts any that have died so a transient API blip doesn't halt the
# campaign until a human notices.
#
# All paths are configurable via env vars with sensible defaults derived
# from the script's own location (works for any user/host, not just ddq).
#
# Disable (stop auto-restart): touch ${SUPERVISOR_DISABLE_FLAG}
# Re-enable:                    rm   ${SUPERVISOR_DISABLE_FLAG}
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CAMPAIGN="${SUPERVISOR_CAMPAIGN_DIR:-$(cd "$SCRIPT_DIR/../../.." && pwd)}"
AEG="${SUPERVISOR_AEGISCTL:-${HOME}/.local/bin/aegisctl}"
LOG_DIR="${SUPERVISOR_LOG_DIR:-${HOME}/.aegisctl/injection-author}"
SUPLOG="${LOG_DIR}/loop-supervisor.log"
SUPERVISOR_DISABLE_FLAG="${SUPERVISOR_DISABLE_FLAG:-${LOG_DIR}/loop-supervisor.disabled}"
SYSTEMS="${SUPERVISOR_SYSTEMS:-ts otel-demo hs}"

[ -f "$SUPERVISOR_DISABLE_FLAG" ] && exit 0

mkdir -p "$LOG_DIR"

# cron runs with a minimal PATH; prepend dirs holding claude / aegisctl / node.
export PATH="${HOME}/.local/bin:${HOME}/cav/.local-tools/node-v24.14.0-linux-x64/bin:${HOME}/.nvm/versions/node/v22.22.1/bin:/usr/local/bin:/usr/bin:/bin:${PATH:-}"

if [[ "${AEGIS_INSECURE_SKIP_VERIFY:-}" != "1" ]]; then
  export AEGIS_INSECURE_SKIP_VERIFY="${SUPERVISOR_INSECURE_SKIP_VERIFY:-0}"
fi

cd "$CAMPAIGN" || { echo "$(date '+%F %T') [supervisor] campaign dir missing: $CAMPAIGN" >> "$SUPLOG"; exit 1; }

COMMON='Submit all traces under project `pair_diagnosis`. ALWAYS allocate namespaces with `--auto --allow-bootstrap` (pool capped at max_count=10, queues via 429-retry when full). Reuse existing namespaces and top up the pool via bootstrap as needed. The collector-crash / spanmetrics / datapack-histogram-gate fixes are all deployed, so build-fail rates are ~0 — run the skill default proportional family diversity (no forced single-family focus). Existing ns may be idle (loadgen stopped); RestartPedestal refreshes them on inject.'

for s in $SYSTEMS; do
  if ! pgrep -f "loop\.sh ${s}([[:space:]]|$)" >/dev/null 2>&1; then
    nohup .claude/skills/injection/loop.sh "$s" \
      --engine claude --model sonnet \
      --aegisctl-bin "$AEG" \
      --max-consecutive-failures 20 \
      --extra-instruction "$COMMON" >> "${LOG_DIR}/loop-${s}.log" 2>&1 &
    launch_rc=$?
    if [[ $launch_rc -eq 0 && -n "$!" ]]; then
      echo "$(date '+%F %T') [supervisor] restarted ${s} loop (was dead), pid $!" >> "$SUPLOG"
    else
      echo "$(date '+%F %T') [supervisor] FAILED to restart ${s} loop (rc=$launch_rc)" >> "$SUPLOG"
    fi
    disown 2>/dev/null
  fi
done
