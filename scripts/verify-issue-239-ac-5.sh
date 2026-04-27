#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../AegisLab/src"
printf 'AC5 evidence: single matrix test + status/decode cases\n'
count=$(rg -n '^func TestExitCodeContractMatrix' cmd/aegisctl/cmd -g '*_test.go' | wc -l | tr -d ' ')
echo "matrix_test_count=$count"
if [[ "$count" != "1" ]]; then
  exit 1
fi
rg -n 'StatusBadRequest|StatusUnauthorized|StatusNotFound|StatusConflict|StatusInternalServerError|not-json' cmd/aegisctl/cmd/exitcode_matrix_test.go
go test ./cmd/aegisctl/cmd -run TestExitCodeContractMatrix -count=1
