#!/usr/bin/env bash
set -euo pipefail
cd AegisLab/src
python - <<'PY'
from pathlib import Path
import re
text = Path('cmd/aegisctl/cmd/contract_test.go').read_text()
name = 'TestIntegrationServerAndDecodeErrorsEmitJSONStructuredOutput'
count = len(re.findall(r'^func\s+' + re.escape(name) + r'\s*\(', text, re.M))
if count != 1:
    raise SystemExit(f'{name} count={count}')
required = [
    'http.StatusInternalServerError',
    'An unexpected error occurred',
    'ExitCodeServer',
    'ExitCodeDecode',
    'serverPayload["type"]',
    'decodePayload["type"]',
]
missing = [snippet for snippet in required if snippet not in text]
if missing:
    raise SystemExit('missing=' + ','.join(missing))
print('single integration test present with server+decode assertions and exit codes 10/11')
PY
