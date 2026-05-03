#!/usr/bin/env bash
# loop.sh — autonomous fault-injection puzzle-author loop for one aegis system.
#
# Each round shells out to a headless agent (claude -p, or codex) with a prompt
# that triggers the `injection` skill. The agent reads per-system state under
# ~/.aegisctl/injection-author/<system>/, picks a hard fault, submits via
# aegisctl, records the round, and updates memory.md. State persists across
# runs — Ctrl-C is safe; the next invocation resumes.
#
# Usage:
#   loop.sh <system> [--rounds N] [--sleep SECS] [--engine claude|codex]
#                    [--state-dir DIR] [--source-dir PATH]
#                    [--extra-instruction TEXT] [--aegisctl-bin PATH]
#                    [--escalate-after N] [--max-consecutive-failures N]
#                    [--no-codex-daemon-restart] [--heartbeat-dir PATH]
#
# Defaults: rounds=999, sleep=900s, engine=claude,
#           state-dir=~/.aegisctl/injection-author/<system>
#           aegisctl-bin=/tmp/aegisctl
#           escalate-after=3, max-consecutive-failures=6
#           heartbeat-dir=<state-dir>
#
# 900s ≈ one inject→detect cycle, so the agent's next round can grade the
# previous one retrospectively from aegisctl. Tune with --sleep if your
# pipeline is faster/slower or the system shows long detector latency.
#
# --source-dir lets the agent grep the target system's source code during
# step 4 (read-the-system) — code-grounded judgment about fan-in callers,
# retry/timeout policies, and cache fallbacks beats prior-only judgment.
# When omitted, the agent has to fall back on memory.md + topology only.
#
# Failure detection (issue #377):
# After N consecutive rc!=0 rounds (--escalate-after, default 3) the wrapper
# emits a banner ERROR line and touches <heartbeat-dir>/unhealthy. If the
# stderr matches `thread .* not found` or `failed to record rollout`, the
# codex daemon is also kicked (kill + relaunch on next round) unless
# --no-codex-daemon-restart was passed. After --max-consecutive-failures
# (default 6) the loop exits non-zero so `ps` no longer finds it. Each
# successful round touches <heartbeat-dir>/heartbeat for external
# `find -mmin +30` style monitoring.

set -euo pipefail

SYSTEM=""
ROUNDS=999
SLEEP=900
ENGINE="claude"
STATE_BASE="${HOME}/.aegisctl/injection-author"
EXTRA_INSTRUCTION=""
AEGISCTL_BIN="/tmp/aegisctl"
SOURCE_DIR=""
ESCALATE_AFTER=3
MAX_CONSECUTIVE_FAILURES=6
CODEX_DAEMON_RESTART=1
HEARTBEAT_DIR=""

usage() {
  sed -n '2,20p' "$0" >&2
  exit "${1:-2}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --rounds)            ROUNDS="$2";   shift 2 ;;
    --sleep)             SLEEP="$2";    shift 2 ;;
    --engine)            ENGINE="$2";   shift 2 ;;
    --state-dir)         STATE_BASE="$2"; shift 2 ;;
    --extra-instruction) EXTRA_INSTRUCTION="$2"; shift 2 ;;
    --aegisctl-bin)      AEGISCTL_BIN="$2"; shift 2 ;;
    --source-dir)        SOURCE_DIR="$2"; shift 2 ;;
    --escalate-after)    ESCALATE_AFTER="$2"; shift 2 ;;
    --max-consecutive-failures) MAX_CONSECUTIVE_FAILURES="$2"; shift 2 ;;
    --no-codex-daemon-restart)  CODEX_DAEMON_RESTART=0; shift ;;
    --heartbeat-dir)     HEARTBEAT_DIR="$2"; shift 2 ;;
    -h|--help)           usage 0 ;;
    -*)                  echo "unknown flag: $1" >&2; usage 2 ;;
    *)
      if [[ -z "$SYSTEM" ]]; then
        SYSTEM="$1"; shift
      else
        echo "unexpected positional: $1" >&2; usage 2
      fi
      ;;
  esac
done

if [[ -z "$SYSTEM" ]]; then
  echo "error: <system> is required (e.g. ts, hs, sn, mm, tea, sockshop, otel-demo)" >&2
  usage 2
fi

case "$ENGINE" in
  # --dangerously-skip-permissions: the loop is fully autonomous, there's
  # nobody at the keyboard to approve tool calls. Without this the spawned
  # session blocks on every Write / Bash that touches a path outside cwd.
  claude) ENGINE_CMD=("claude" "-p" "--dangerously-skip-permissions") ;;
  codex)  ENGINE_CMD=("codex" "exec" "--dangerously-bypass-approvals-and-sandbox") ;;
  *) echo "error: --engine must be claude or codex" >&2; exit 2 ;;
esac

if ! command -v "${ENGINE_CMD[0]}" >/dev/null 2>&1; then
  echo "error: '${ENGINE_CMD[0]}' not found in PATH" >&2
  exit 127
fi

if [[ ! -x "$AEGISCTL_BIN" ]]; then
  echo "error: aegisctl binary not executable at $AEGISCTL_BIN — pass --aegisctl-bin /abs/path/to/aegisctl" >&2
  exit 127
fi

if [[ -n "$SOURCE_DIR" && ! -d "$SOURCE_DIR" ]]; then
  echo "error: --source-dir $SOURCE_DIR is not a directory" >&2
  exit 2
fi

STATE_DIR="${STATE_BASE}/${SYSTEM}"
mkdir -p "${STATE_DIR}/rounds"
LOG="${STATE_DIR}/loop.log"
META="${STATE_DIR}/metadata.json"

if [[ -z "$HEARTBEAT_DIR" ]]; then
  HEARTBEAT_DIR="$STATE_DIR"
fi
mkdir -p "$HEARTBEAT_DIR"
HEARTBEAT_FILE="${HEARTBEAT_DIR}/heartbeat"
UNHEALTHY_FILE="${HEARTBEAT_DIR}/unhealthy"

ts() { date -Iseconds; }

# stderr-pattern matcher for codex-daemon thread loss (issue #377). Returns 0
# when the captured stderr looks like the codex app-server lost the thread.
codex_thread_lost() {
  local stderr_path="$1"
  [[ -s "$stderr_path" ]] || return 1
  grep -qE 'thread .* not found|failed to record rollout' "$stderr_path"
}

restart_codex_daemon() {
  # Best-effort: kill running codex/codex-app-server processes; the next round
  # re-execs `codex exec` which will spawn a fresh daemon. Errors are non-fatal
  # — if pkill finds nothing or codex isn't installed, we just continue.
  pkill -f 'codex(\s|$)' 2>/dev/null || true
  pkill -f 'codex-app-server' 2>/dev/null || true
  pkill -f 'codex exec' 2>/dev/null || true
  sleep 2
}

if [[ ! -f "$META" ]]; then
  cat > "$META" <<EOF
{
  "system": "${SYSTEM}",
  "first_started_at": "$(ts)",
  "last_started_at": "$(ts)",
  "total_rounds_run": 0
}
EOF
else
  # touch last_started_at; agent maintains the rest
  python3 - "$META" "$(ts)" <<'PY' 2>/dev/null || true
import json, sys, pathlib
p = pathlib.Path(sys.argv[1])
data = json.loads(p.read_text())
data["last_started_at"] = sys.argv[2]
p.write_text(json.dumps(data, indent=2) + "\n")
PY
fi

echo "[$(ts)] starting loop: system=${SYSTEM} engine=${ENGINE} rounds=${ROUNDS} sleep=${SLEEP}s state=${STATE_DIR} escalate_after=${ESCALATE_AFTER} max_consecutive_failures=${MAX_CONSECUTIVE_FAILURES}" | tee -a "$LOG"

CONSECUTIVE_FAILURES=0

for i in $(seq 1 "$ROUNDS"); do
  ROUND_TS="$(ts)"
  echo "[${ROUND_TS}] ===== round ${i}/${ROUNDS} =====" | tee -a "$LOG"

  # Pre-render optional sections OUTSIDE the heredoc — bash ${VAR:+...}
  # expansion chokes on nested ${} or unbalanced parens inside the
  # alternate-text, so we keep the heredoc free of conditional substitutions.
  SOURCE_LINE=""
  if [[ -n "$SOURCE_DIR" ]]; then
    SOURCE_LINE="system source code: ${SOURCE_DIR}  (target system's source repo — you MUST run at least 2 Read or Grep calls under it during step 4 BEFORE proposing a fault, and cite each result as a {file,lines,what} entry in the round file's code_evidence block. Look for: fan-in callers of your candidate, retry/timeout/circuit-breaker logic, cache fallbacks, shared resources. Empty code_evidence here is an anti-pattern that the next round will mark prior-only in memory.md. Priors lie on unfamiliar systems and miss recent changes on familiar ones — read the actual code.)"
  fi
  EXTRA_LINE=""
  if [[ -n "$EXTRA_INSTRUCTION" ]]; then
    EXTRA_LINE="Additional instruction for this campaign: ${EXTRA_INSTRUCTION}"
  fi

  # The prompt is intentionally short; the heavy lifting lives in the
  # `injection` skill, which is loaded by the agent once it triggers on
  # "故障注入" / "injection". Keep this prompt stable so caching stays warm.
  PROMPT=$(cat <<EOF
Autonomous injection round ${i} of ${ROUNDS}, target system: ${SYSTEM}.
State directory: ${STATE_DIR}
aegisctl binary: ${AEGISCTL_BIN}  (use this absolute path for every aegisctl invocation — it's the build matched to the current backend; do NOT rely on \`aegisctl\` from PATH).
${SOURCE_LINE}

Follow the \`injection\` skill end-to-end this round. The skill body has the full schema for round files and the multi-source reward framework — read it before doing anything else.

1. Read ${STATE_DIR}/metadata.json and ${STATE_DIR}/memory.md (may be empty on first run).
2. Look at the most recent round file in ${STATE_DIR}/rounds/. If its trace's terminal events have landed in aegisctl by now, retroactively grade it using the multi-source signals (detector verdict + injection-effect inspection + SLO impact at the entrance) and patch the round file's "retro_grade" block. Don't trust the detector alone — it has FPs and FNs; cross-check by pulling the abnormal-window trace data via aegisctl. Leave "pending_offline_rca" untouched (that field is reserved for the future offline RCA-grading query).
3. Use aegisctl to survey the recent injection distribution for ${SYSTEM} (last ~50–100 leaf injections, batch parents excluded). Tally by chaos_type family + target service.
4. **Read the system — code AND data, not priors.** This is the round's biggest quality lever; do NOT skip to "I already know queryForTravel/checkout/etc." (a) **Source code:** if a source-dir was given above, run **at least 2 \`Read\` or \`Grep\` calls** under it — find fan-in callers of your candidate, retry/timeout/circuit-breaker/cache-fallback logic, and shared resources. Cite them in the round file's \`code_evidence\` block as \`{file, lines, what}\` triples. Empty \`code_evidence\` when source-dir was provided is an anti-pattern; the next round will mark this round "prior-only" in memory.md. (b) **Live data:** pull recent traces / metrics for the candidate service via aegisctl — actual outbound spans, latency distribution per endpoint, baseline error rates. Cite in \`data_evidence\`. The wire (b) shows what the cluster actually does; the code (a) shows what it's supposed to do; both can lie alone, neither is replaceable by priors. Only after these two evidence streams are gathered do you pick the fault. Optimize for: fine granularity (JVM method / HTTP route+verb / DB table+op / service-pair network — NOT whole-pod) × user-visible SLO breach × long causal chain (≥2 hops, shared resource, retry/cache cascade, or timeout amplifier) × blast radius on a critical-path endpoint.
5. Diversify against the family/service tally — no chaos_type family above ~30% of the recent window; no single target service dominating.
6. Submit through aegisctl. A round has TWO independent fan-out dimensions (see the skill's *Per-round shape* section): **K_outer** = parallel traces this round (each its own ts namespace via \`--auto\`, each its own RCA puzzle), and **K_inner** = leaves per trace (the \`--apply --batch\` shape, used when leaves should INTERACT within the same trace's data — mutual masking, co-trigger amplification, multi-root-cause grading). Default to K_outer ≥ 2 for throughput when the namespace pool has slack. Use K_inner ≥ 2 only with a concrete interaction hypothesis. Two unrelated faults on different namespaces are K_outer=2 (good — independent puzzles), NOT K_inner=2 (a confused single batch).
7. **Verify the fault actually landed.** Pull the trace / abnormal-window data via aegisctl and confirm the chaos perturbed traces or metrics on the targeted scope. If it didn't, mark the round outcome "injection-noop" and investigate (image issue, IPVLAN HTTPChaos, missing observed-pair for network chaos, DNS catalog miss, etc.) — do NOT grade a no-op round as a puzzle.
8. **Simulate the RCA reasoning.** Walk through how an RCA agent would chase this from entrance symptom to actual fault. Count hops in the inference chain and list plausible decoy hypotheses on the way. Record both in the round file's "simulated_rca" block — be honest, don't pad.
9. Write the round record to ${STATE_DIR}/rounds/round-${i}-\$(date +%s).json using the schema from the skill body (picked_faults, hypothesis, diversity_note, code_evidence, data_evidence, submit, injection_landed, injection_evidence, simulated_rca, retro_grade, pending_offline_rca: true).
10. Append distilled cross-round lessons (NOT round-level dumps) to ${STATE_DIR}/memory.md — hard-puzzle templates that worked, system quirks discovered, anti-patterns to avoid, open questions. Prune memory.md if it exceeds ~200 lines.
11. Update ${STATE_DIR}/metadata.json: bump total_rounds_run, refresh family_tally and service_tally.
12. Whenever you reach for mysql/kubectl/redis-cli/raw helm/curl to compensate for something aegisctl couldn't do — or whenever aegisctl output / errors / missing flags forced a workaround — append one line to ${STATE_DIR}/aegisctl_gaps.md per the skill's *Aegisctl gap log* format. The user reviews this periodically to improve the CLI; don't bury friction silently.

Don't sleep waiting for this round's terminal event inline — step 2 of the next round picks it up. Exit cleanly once the round is recorded.
${EXTRA_LINE}
EOF
)

  STDERR_TMP="$(mktemp -t loop-stderr.XXXXXX)"
  rc=0
  "${ENGINE_CMD[@]}" "${PROMPT}" >>"$LOG" 2>"$STDERR_TMP" || rc=$?
  # Mirror captured stderr into the loop log so existing tail -f workflows
  # still see engine errors. Keep the temp file around for pattern matching.
  cat "$STDERR_TMP" >>"$LOG"

  if (( rc == 0 )); then
    echo "[$(ts)] round ${i} ok" | tee -a "$LOG"
    CONSECUTIVE_FAILURES=0
    touch "$HEARTBEAT_FILE"
    rm -f "$UNHEALTHY_FILE"
  else
    CONSECUTIVE_FAILURES=$((CONSECUTIVE_FAILURES + 1))
    echo "[$(ts)] round ${i} returned rc=${rc} (consecutive_failures=${CONSECUTIVE_FAILURES}/${MAX_CONSECUTIVE_FAILURES})" | tee -a "$LOG"

    STDERR_TAIL="$(tail -n 20 "$STDERR_TMP" 2>/dev/null | tr '\n' ' ' | cut -c1-500)"

    if (( CONSECUTIVE_FAILURES >= ESCALATE_AFTER )); then
      echo "[$(ts)] [ERROR] [${SYSTEM}] ${CONSECUTIVE_FAILURES} consecutive rc!=0 — engine may be unhealthy. Last rc=${rc}, last stderr: ${STDERR_TAIL}" | tee -a "$LOG"
      touch "$UNHEALTHY_FILE"

      if [[ "$ENGINE" == "codex" ]] && (( CODEX_DAEMON_RESTART )) && codex_thread_lost "$STDERR_TMP"; then
        echo "[$(ts)] [ERROR] [${SYSTEM}] codex thread loss detected — restarting codex daemon and resetting failure counter" | tee -a "$LOG"
        restart_codex_daemon
        CONSECUTIVE_FAILURES=0
      fi
    fi

    if (( CONSECUTIVE_FAILURES >= MAX_CONSECUTIVE_FAILURES )); then
      echo "[$(ts)] [FATAL] [${SYSTEM}] ${CONSECUTIVE_FAILURES} consecutive rc!=0 — exiting loop. Last stderr: ${STDERR_TAIL}" | tee -a "$LOG"
      rm -f "$STDERR_TMP"
      exit 1
    fi
  fi

  rm -f "$STDERR_TMP"

  if (( i < ROUNDS )); then
    echo "[$(ts)] sleeping ${SLEEP}s before round $((i+1))" | tee -a "$LOG"
    sleep "$SLEEP"
  fi
done

echo "[$(ts)] loop done" | tee -a "$LOG"
