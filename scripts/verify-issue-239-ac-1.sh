#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
printf 'AC1 evidence: exitcode hub + root handler + no os.Exit(0) in cmd package\n'
rg -n 'package exitcode|func ForError|func ErrorMessage|executeError\(|return exitcode\.ForError|return exitcode\.ErrorMessage|os\.Exit\(0\)' \
  AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go \
  AegisLab/src/cmd/aegisctl/cmd/contract.go \
  AegisLab/src/cmd/aegisctl/main.go \
  AegisLab/src/cmd/aegisctl/cmd || true
if rg -n 'os\.Exit\(0\)' AegisLab/src/cmd/aegisctl/cmd AegisLab/src/cmd/aegisctl/main.go >/tmp/issue239-ac1-os-exit.txt; then
  echo 'unexpected os.Exit(0) found:'
  cat /tmp/issue239-ac1-os-exit.txt
  exit 1
fi
