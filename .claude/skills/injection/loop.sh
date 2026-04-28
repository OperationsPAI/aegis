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
#                    [--state-dir DIR] [--extra-instruction TEXT]
#
# Defaults: rounds=999, sleep=900s, engine=claude,
#           state-dir=~/.aegisctl/injection-author/<system>
#
# 900s ≈ one inject→detect cycle, so the agent's next round can grade the
# previous one retrospectively from aegisctl. Tune with --sleep if your
# pipeline is faster/slower or the system shows long detector latency.

set -euo pipefail

SYSTEM=""
ROUNDS=999
SLEEP=900
ENGINE="claude"
STATE_BASE="${HOME}/.aegisctl/injection-author"
EXTRA_INSTRUCTION=""
AEGISCTL_BIN="/tmp/aegisctl"

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

STATE_DIR="${STATE_BASE}/${SYSTEM}"
mkdir -p "${STATE_DIR}/rounds"
LOG="${STATE_DIR}/loop.log"
META="${STATE_DIR}/metadata.json"

ts() { date -Iseconds; }

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

echo "[$(ts)] starting loop: system=${SYSTEM} engine=${ENGINE} rounds=${ROUNDS} sleep=${SLEEP}s state=${STATE_DIR}" | tee -a "$LOG"

for i in $(seq 1 "$ROUNDS"); do
  ROUND_TS="$(ts)"
  echo "[${ROUND_TS}] ===== round ${i}/${ROUNDS} =====" | tee -a "$LOG"

  # The prompt is intentionally short; the heavy lifting lives in the
  # `injection` skill, which is loaded by the agent once it triggers on
  # "故障注入" / "injection". Keep this prompt stable so caching stays warm.
  PROMPT=$(cat <<EOF
Autonomous injection round ${i} of ${ROUNDS}, target system: ${SYSTEM}.
State directory: ${STATE_DIR}
aegisctl binary: ${AEGISCTL_BIN}  (use this absolute path for every aegisctl invocation — it's the build matched to the current backend; do NOT rely on \`aegisctl\` from PATH).

Follow the \`injection\` skill end-to-end this round. The skill body has the full schema for round files and the multi-source reward framework — read it before doing anything else.

1. Read ${STATE_DIR}/metadata.json and ${STATE_DIR}/memory.md (may be empty on first run).
2. Look at the most recent round file in ${STATE_DIR}/rounds/. If its trace's terminal events have landed in aegisctl by now, retroactively grade it using the multi-source signals (detector verdict + injection-effect inspection + SLO impact at the entrance) and patch the round file's "retro_grade" block. Don't trust the detector alone — it has FPs and FNs; cross-check by pulling the abnormal-window trace data via aegisctl. Leave "pending_offline_rca" untouched (that field is reserved for the future offline RCA-grading query).
3. Use aegisctl to survey the recent injection distribution for ${SYSTEM} (last ~50–100 leaf injections, batch parents excluded). Tally by chaos_type family + target service.
4. Read service code / topology / traces enough to pick ONE hard puzzle this round. Optimize for: fine granularity (JVM method / HTTP route+verb / DB table+op / service-pair network — NOT whole-pod) × user-visible SLO breach × long causal chain (≥2 hops, shared resource, retry/cache cascade, or timeout amplifier) × blast radius on a critical-path endpoint.
5. Diversify against the family/service tally — no chaos_type family above ~30% of the recent window; no single target service dominating.
6. Submit through aegisctl. Multi-fault batches (\`inject guided --stage\` then \`--apply --batch\`) are encouraged when faults have a concrete interaction hypothesis (mutual masking, co-trigger amplification, multi-root-cause grading); ~25–35% of rounds.
7. **Verify the fault actually landed.** Pull the trace / abnormal-window data via aegisctl and confirm the chaos perturbed traces or metrics on the targeted scope. If it didn't, mark the round outcome "injection-noop" and investigate (image issue, IPVLAN HTTPChaos, missing observed-pair for network chaos, DNS catalog miss, etc.) — do NOT grade a no-op round as a puzzle.
8. **Simulate the RCA reasoning.** Walk through how an RCA agent would chase this from entrance symptom to actual fault. Count hops in the inference chain and list plausible decoy hypotheses on the way. Record both in the round file's "simulated_rca" block — be honest, don't pad.
9. Write the round record to ${STATE_DIR}/rounds/round-${i}-\$(date +%s).json using the schema from the skill body (picked_faults, hypothesis, diversity_note, submit, injection_landed, injection_evidence, simulated_rca, retro_grade, pending_offline_rca: true).
10. Append distilled cross-round lessons (NOT round-level dumps) to ${STATE_DIR}/memory.md — hard-puzzle templates that worked, system quirks discovered, anti-patterns to avoid, open questions. Prune memory.md if it exceeds ~200 lines.
11. Update ${STATE_DIR}/metadata.json: bump total_rounds_run, refresh family_tally and service_tally.
12. Whenever you reach for mysql/kubectl/redis-cli/raw helm/curl to compensate for something aegisctl couldn't do — or whenever aegisctl output / errors / missing flags forced a workaround — append one line to ${STATE_DIR}/aegisctl_gaps.md per the skill's *Aegisctl gap log* format. The user reviews this periodically to improve the CLI; don't bury friction silently.

Don't sleep waiting for this round's terminal event inline — step 2 of the next round picks it up. Exit cleanly once the round is recorded.
${EXTRA_INSTRUCTION:+
Additional instruction for this campaign: ${EXTRA_INSTRUCTION}}
EOF
)

  if "${ENGINE_CMD[@]}" "${PROMPT}" >>"$LOG" 2>&1; then
    echo "[$(ts)] round ${i} ok" | tee -a "$LOG"
  else
    rc=$?
    echo "[$(ts)] round ${i} returned rc=${rc} (continuing)" | tee -a "$LOG"
  fi

  if (( i < ROUNDS )); then
    echo "[$(ts)] sleeping ${SLEEP}s before round $((i+1))" | tee -a "$LOG"
    sleep "$SLEEP"
  fi
done

echo "[$(ts)] loop done" | tee -a "$LOG"
