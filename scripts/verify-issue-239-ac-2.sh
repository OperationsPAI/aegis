#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
printf 'AC2 evidence: HTTP/decode mapping lines\n'
rg -n 'StatusCode == 401|StatusCode == 403|StatusCode == 404|StatusCode == 409|StatusCode >= 400|StatusCode >= 500|DecodeError|return CodeDecodeFailure|return CodeUsage|return CodeServerError' \
  AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go \
  AegisLab/src/cmd/aegisctl/client/client.go
