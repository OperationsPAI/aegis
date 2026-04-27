#!/usr/bin/env bash
set -euo pipefail

python3 - <<'PY'
from pathlib import Path
import sys

root = Path('AegisLab/src/cmd/aegisctl')
exitcode = root / 'internal/cli/exitcode/exitcode.go'
contract = root / 'cmd/contract.go'
root_go = root / 'cmd/root.go'
main_go = root / 'main.go'

checks = []
checks.append((exitcode.exists(), f'found {exitcode}'))
checks.append(('func ForError(err error) int {' in exitcode.read_text(), 'exitcode.ForError exists'))
checks.append(('rootCmd.Execute()' in contract.read_text(), 'contract.go calls rootCmd.Execute()'))
checks.append(('return executeError(err)' in contract.read_text(), 'contract.go routes Execute() errors through executeError'))
checks.append(('return exitcode.ForError(err)' in contract.read_text(), 'contract.go routes executeError through exitcode.ForError'))
checks.append(('func Execute() int {' in root_go.read_text() and 'return executeArgs(os.Args[1:])' in root_go.read_text(), 'root Execute() returns executeArgs(os.Args[1:])'))
checks.append(('os.Exit(cmd.Execute())' in main_go.read_text(), 'main.go delegates process exit to cmd.Execute()'))

bad = []
for path in root.rglob('*.go'):
    text = path.read_text()
    if 'os.Exit(0)' in text:
        bad.append(str(path))
checks.append((not bad, 'no direct os.Exit(0) calls remain under src/cmd/aegisctl'))

failed = False
for ok, message in checks:
    status = 'OK' if ok else 'FAIL'
    print(f'{status}: {message}')
    if not ok:
        failed = True
if bad:
    print('os.Exit(0) occurrences:')
    for path in bad:
        print(path)
if failed:
    sys.exit(1)
PY
