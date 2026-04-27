#!/usr/bin/env bash
set -euo pipefail

python3 - <<'PY'
from pathlib import Path
import sys

exitcode = Path('AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go').read_text()
client = Path('AegisLab/src/cmd/aegisctl/client/client.go').read_text()

checks = [
    ('case apiErr.StatusCode == 401 || apiErr.StatusCode == 403:' in exitcode and 'return CodeAuthFailure' in exitcode, '401/403 -> 3'),
    ('case apiErr.StatusCode == 404:' in exitcode and 'return CodeNotFound' in exitcode, '404 -> 7'),
    ('case apiErr.StatusCode == 409:' in exitcode and 'return CodeConflict' in exitcode, '409 -> 8'),
    ('case apiErr.StatusCode >= 400 && apiErr.StatusCode <= 499:' in exitcode and 'return CodeUsage' in exitcode, 'other 4xx -> 2'),
    ('case apiErr.StatusCode >= 500 && apiErr.StatusCode <= 599:' in exitcode and 'return CodeServerError' in exitcode, '5xx -> 10'),
    ('type DecodeError struct {' in client and 'return &DecodeError{Body: respBody, Err: err}' in client, 'decode failures wrapped as client.DecodeError'),
    ('var decodeErr *client.DecodeError' in exitcode and 'return CodeDecodeFailure' in exitcode, 'DecodeError -> 11'),
]
failed = False
for ok, label in checks:
    print(f"{'OK' if ok else 'FAIL'}: {label}")
    if not ok:
        failed = True
if failed:
    sys.exit(1)
PY
