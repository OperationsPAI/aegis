#!/usr/bin/env bash
# loop-supervisor.sh — watchdog that restarts dead injection loops (ts/otel-demo/hs).
# Run periodically (cron every 15m, or the while-true wrapper). The loops self-kill
# after --max-consecutive-failures consecutive engine rc!=0 (issue #377); for an
# unattended long-horizon campaign that means a transient API blip halts everything
# until a human notices. This restarts any loop that has died.
#
# Disable (stop auto-restart) with: touch /tmp/loop-supervisor.disabled
# Re-enable with: rm /tmp/loop-supervisor.disabled
set -u

[ -f /tmp/loop-supervisor.disabled ] && exit 0

# cron runs with a minimal PATH; prepend the dirs holding claude / aegisctl / node
# so loop.sh can find the engine. Keep $PATH as a fallback tail.
export PATH="/home/ddq/.local/bin:/home/ddq/cav/.local-tools/node-v24.14.0-linux-x64/bin:/home/ddq/.nvm/versions/node/v22.22.1/bin:/usr/local/bin:/usr/bin:/bin:${PATH:-}"

CAMPAIGN=/home/ddq/aegis-campaign
AEG=/home/ddq/.local/bin/aegisctl
SUPLOG=/tmp/loop-supervisor.log
export AEGIS_INSECURE_SKIP_VERIFY=1

cd "$CAMPAIGN" || { echo "$(date '+%F %T') [supervisor] campaign dir missing" >> "$SUPLOG"; exit 1; }

COMMON='Submit all traces under project `pair_diagnosis`. ALWAYS allocate namespaces with `--auto --allow-bootstrap` (pool capped at max_count=10, queues via 429-retry when full). Reuse existing namespaces and top up the pool via bootstrap as needed. The collector-crash / spanmetrics / datapack-histogram-gate fixes are all deployed, so build-fail rates are ~0 — run the skill default proportional family diversity (no forced single-family focus). Existing ns may be idle (loadgen stopped); RestartPedestal refreshes them on inject.'

for s in ts otel-demo hs; do
  if ! pgrep -f "loop\.sh ${s}( |\$)" >/dev/null 2>&1; then
    nohup .claude/skills/injection/loop.sh "$s" \
      --engine claude --model sonnet \
      --aegisctl-bin "$AEG" \
      --max-consecutive-failures 20 \
      --extra-instruction "$COMMON" >> "/tmp/loop-${s}.log" 2>&1 &
    disown
    echo "$(date '+%F %T') [supervisor] restarted ${s} loop (was dead), pid $!" >> "$SUPLOG"
  fi
done
