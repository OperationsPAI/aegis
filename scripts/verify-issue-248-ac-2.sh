#!/usr/bin/env bash
set -euo pipefail
cd AegisLab/src
python - <<'PY'
from pathlib import Path
text = Path('cmd/aegisctl/output/output.go').read_text()
checks = {
    'json-ndjson-switch': 'return format == FormatJSON || format == FormatNDJSON',
    'json-single-line': 'fmt.Fprintln(os.Stderr, string(payload))',
    'human-header': 'fmt.Fprintf(os.Stderr, "Error [%s]: %s"',
    'human-cause': 'fmt.Fprintf(os.Stderr, "\\n  cause: %s"',
    'human-hint': 'fmt.Fprintf(os.Stderr, "\\n  hint: %s"',
}
missing = [name for name, snippet in checks.items() if snippet not in text]
if missing:
    raise SystemExit('missing=' + ','.join(missing))
print('structured stderr rendering OK for json/ndjson and human formats')
PY
