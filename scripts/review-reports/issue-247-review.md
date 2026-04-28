# Review for issue #247 — PR #266

## Cascade preconditions
| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| none (no submodule pointer changes in `origin/main...origin/workbuddy/issue-247`) | N/A | N/A | N/A |

**cascade command**: `python - <<'PY'
from pathlib import Path
import subprocess
changed = Path('/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-247')
res = subprocess.run(['git','diff','--raw','origin/main...origin/workbuddy/issue-247'], cwd=changed, text=True, capture_output=True, check=True)
submods = [line for line in res.stdout.splitlines() if line.startswith(':160000') or '\t' in line and '160000' in line]
print('submodule_rows=' + str(len(submods)))
print('raw_diff_lines=' + str(len(res.stdout.splitlines())))
print('result=' + ('PASS:no-submodule-pointer-changes' if not submods else 'FAIL:submodule-pointer-changes-found'))
PY`
**exit**: 0
**stdout** (first 20 lines):
```text
submodule_rows=0
raw_diff_lines=12
result=PASS:no-submodule-pointer-changes
```

## Per-AC verdicts
### AC 1: "上述命令均新增 `--stdin` flag；与位置参数互斥（同时给 → EXIT=2）。"
**verdict**: PASS
**command**: `python - <<'PY'
from pathlib import Path
cmd_dir = Path('/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-247/AegisLab/src/cmd/aegisctl/cmd')
checks = {
    'inject.go': ['addStdinFlags(injectGetCmd', 'addStdinFlags(injectFilesCmd', 'addStdinFlags(injectDownloadCmd', 'runStdinItems("inject get"', 'runStdinItems("inject files"', 'runStdinItems("inject download"'],
    'task.go': ['addStdinFlags(taskGetCmd', 'addStdinFlags(taskLogsCmd', 'runStdinItems("task get"', 'runStdinItems("task logs"'],
    'trace.go': ['addStdinFlags(traceGetCmd', 'addStdinFlags(traceWatchCmd', 'runStdinItems("trace get"', 'runStdinItems("trace watch"'],
    'wait.go': ['addStdinFlags(waitCmd', 'runStdinItems("wait"'],
    'stdin_helpers.go': ['if len(args) > 0 {', 'usageErrorf("--stdin cannot be combined with positional arguments")'],
}
missing = []
for rel, needles in checks.items():
    text = (cmd_dir / rel).read_text()
    for needle in needles:
        if needle not in text:
            missing.append(f'{rel}: {needle}')
print('checked_files=' + str(len(checks)))
print('missing=' + str(len(missing)))
for item in missing:
    print(item)
if missing:
    raise SystemExit(1)
print('result=PASS:all-target-commands-wire---stdin-and-share-mutual-exclusion-helper')
PY`
**exit**: 0
**stdout** (first 20 lines):
```text
checked_files=5
missing=0
result=PASS:all-target-commands-wire---stdin-and-share-mutual-exclusion-helper
```

### AC 2: "输入自动识别：单行非 JSON → 当 1 个 ID；以 `{` 开头 → NDJSON 对象，按 `--stdin-field` 取字段（默认推导：`inject *`→`name`、`trace *`→`id`、`task *`→`id`、`wait`→`trace_id` 或 `id`）。"
**verdict**: PASS
**command**: `python - <<'PY'
from pathlib import Path
text = Path('/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-247/AegisLab/src/internal/cli/stdin/parser.go').read_text()
needles = [
    'case "inject get", "inject files", "inject download":',
    'return []string{"name"}',
    'case "trace get", "trace watch":',
    'return []string{"id"}',
    'case "task get", "task logs":',
    'case "wait":',
    'return []string{"trace_id", "id"}',
    'modeJSON = strings.HasPrefix(raw, "{")',
    'items = append(items, raw)',
    'parse stdin line %d as json',
    'missing field %q',
]
missing = [n for n in needles if n not in text]
print('checked_needles=' + str(len(needles)))
print('missing=' + str(len(missing)))
for item in missing:
    print(item)
if missing:
    raise SystemExit(1)
print('result=PASS:parser-defaults-and-line-vs-ndjson-dispatch-present')
PY`
**exit**: 0
**stdout** (first 20 lines):
```text
checked_needles=11
missing=0
result=PASS:parser-defaults-and-line-vs-ndjson-dispatch-present
```

### AC 3: "退出码：全成功→0；全失败→首个 error 的码；部分成功→9；`--fail-fast` 立即停止并取首个失败码。"
**verdict**: PASS
**command**: `go test ./cmd/aegisctl/cmd -run 'TestRunItemsExitCodeProgressAndFailFast' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok   	aegis/cmd/aegisctl/cmd	0.033s
```

### AC 4: "stderr per-item 进度行：`[i/N] <verb> <id>: ok|failed (<type>)`；`-q/--quiet` 抑制。"
**verdict**: PASS
**command**: `python - <<'PY'
from pathlib import Path
helpers = Path('/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-247/AegisLab/src/cmd/aegisctl/cmd/stdin_helpers.go').read_text()
output = Path('/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-247/AegisLab/src/cmd/aegisctl/output/output.go').read_text()
needles = [
    'output.PrintInfo(fmt.Sprintf("[%d/%d] %s %s: failed (%s)"',
    'output.PrintInfo(fmt.Sprintf("[%d/%d] %s %s: ok"',
    'if !Quiet {',
]
missing = [n for n in needles if (helpers + '\n' + output).find(n) == -1]
print('checked_needles=' + str(len(needles)))
print('missing=' + str(len(missing)))
for item in missing:
    print(item)
if missing:
    raise SystemExit(1)
print('result=PASS:progress-format-and-quiet-suppression-implemented-via-PrintInfo')
PY`
**exit**: 0
**stdout** (first 20 lines):
```text
checked_needles=3
missing=0
result=PASS:progress-format-and-quiet-suppression-implemented-via-PrintInfo
```

### AC 5: "一个 integration test（仅一个）：覆盖父 issue §3.6 的 pattern #1（`inject list -o ndjson | inject download --stdin --output-dir`）端到端跑通；其余 4 个 pattern 用 unit test 覆盖 stdin 解析层即可。"
**verdict**: PASS
**command**: `go test ./cmd/aegisctl/cmd -run TestInjectDownloadFromStdinPipe -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok   	aegis/cmd/aegisctl/cmd	0.033s
```

### Mini-AC 6 (plan verify #1): `cd AegisLab/src && go test ./internal/cli/stdin -run 'Test.*Stdin.*Parser' -count=1`
**verdict**: PASS
**command**: `go test ./internal/cli/stdin -run 'Test.*Stdin.*Parser' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok   	aegis/internal/cli/stdin	0.009s
```

### Mini-AC 7 (plan verify #2): `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run 'Test.*(Inject|Task|Trace|Wait).*Stdin' -count=1`
**verdict**: PASS
**command**: `go test ./cmd/aegisctl/cmd -run 'Test.*(Inject|Task|Trace|Wait).*Stdin' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok   	aegis/cmd/aegisctl/cmd	0.022s
```

### Mini-AC 8 (plan verify #3): `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run 'Test.*(RunItems|ExitCode|FailFast|Progress).*' -count=1`
**verdict**: PASS
**command**: `go test ./cmd/aegisctl/cmd -run 'Test.*(RunItems|ExitCode|FailFast|Progress).*' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok   	aegis/cmd/aegisctl/cmd	0.017s
```

### Mini-AC 9 (plan verify #4): `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectDownloadFromStdinPipe -count=1`
**verdict**: PASS
**command**: `go test ./cmd/aegisctl/cmd -run TestInjectDownloadFromStdinPipe -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok   	aegis/cmd/aegisctl/cmd	0.017s
```

## Overall
- PASS: 9 / 9
- FAIL: none
- UNVERIFIABLE: none
