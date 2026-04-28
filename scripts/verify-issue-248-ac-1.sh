#!/usr/bin/env bash
set -euo pipefail
cd AegisLab/src
python - <<'PY'
from pathlib import Path
import re
text = Path('cmd/aegisctl/internal/cli/clierr/clierr.go').read_text()
required = {
    'Type': 'type',
    'Message': 'message',
    'Cause': 'cause',
    'RequestID': 'request_id',
    'Suggestion': 'suggestion',
    'Retryable': 'retryable',
    'ExitCode': 'exit_code',
}
missing = []
for field, tag in required.items():
    pattern = rf'{field}\s+.+`json:"{re.escape(tag)}"`'
    if not re.search(pattern, text):
        missing.append(f'{field}:{tag}')
if 'type CLIError struct {' not in text:
    missing.append('struct')
if missing:
    raise SystemExit('missing=' + ','.join(missing))
print('CLIError fields OK:', ', '.join(required.values()))
PY
