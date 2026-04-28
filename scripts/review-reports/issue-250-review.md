# Review for issue #250 — PR #254

## Cascade preconditions
| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| _None_    | _none_        | N/A       | N/A     |

Commands ran:
`git ls-files --stage | awk '$1 ~ /^160000/'` (no output)
`git submodule status` (no output)

## Per-AC verdicts

### AC 1: `aegisctl execute create 与 `aegisctl execute submit` 行为相同；后者在 stderr 打印 [deprecated] ... warning，不影响退出码。`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestExecuteSubmitAndCreateOutputAreByteIdentical -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok   	aegis/cmd/aegisctl/cmd	0.035s
```
**stderr** (first 20 lines, if nonzero):
```text
(none)
```

### AC 2: `aegisctl inject list-files 与 aegisctl inject files 行为相同；同上 deprecation warning 规则。`
**verdict**: PASS
**command**: `cd AegisLab/src && go build -o /tmp/aegisctl_issue250 ./cmd/aegisctl && python3 /tmp/issue250_verify_server.py >/tmp/issue250_verify_server.log 2>&1 & server_pid=$!; sleep 0.3; /tmp/aegisctl_issue250 inject list-files my_injection --server http://127.0.0.1:18443 --token t --project pair_diagnosis --output json 1> /tmp/issue250_create_stdout 2> /tmp/issue250_create_stderr; ec1=$?; /tmp/aegisctl_issue250 inject files my_injection --server http://127.0.0.1:18443 --token t --project pair_diagnosis --output json 1> /tmp/issue250_files_stdout 2> /tmp/issue250_files_stderr; ec2=$?; kill $server_pid; if [ "$ec1" -ne 0 ] || [ "$ec2" -ne 0 ]; then echo "exit create=$ec1 files=$ec2"; cat /tmp/issue250_create_stderr; cat /tmp/issue250_files_stderr; exit 1; fi; diff -u /tmp/issue250_create_stdout /tmp/issue250_files_stdout >/tmp/issue250_diff || { echo "stdout mismatch"; cat /tmp/issue250_diff; exit 2; }; grep -q "\[deprecated\] 'files' will be removed in v<NEXT_MINOR>; use 'list-files'" /tmp/issue250_files_stderr || { echo "missing warning"; cat /tmp/issue250_files_stderr; exit 3; }`
**exit**: 0
**stdout** (first 20 lines):
```text
PASS
```
**stderr** (first 20 lines, if nonzero):
```text
(none)
```

### AC 3: `所有接受 --spec 的命令同时接受 -f / --input；三者互斥（任两个同时给 → EXIT=2）。`
**verdict**: PASS
**command**: `cd AegisLab/src && go build -o /tmp/aegisctl_issue250 ./cmd/aegisctl && python3 /tmp/issue250_verify_server2.py >/tmp/issue250_verify_server2.log 2>&1 & server_pid=$!; sleep 0.2; /tmp/aegisctl_issue250 execute create --input /tmp/issue250_spec.yaml --server http://127.0.0.1:18444 --token t --project pair_diagnosis --output json --dry-run 1> /tmp/issue250_exec_input_stdout 2> /tmp/issue250_exec_input_stderr; ec_input=$?; /tmp/aegisctl_issue250 execute create -f /tmp/issue250_spec.yaml --server http://127.0.0.1:18444 --token t --project pair_diagnosis --output json --dry-run 1> /tmp/issue250_exec_short_stdout 2> /tmp/issue250_exec_short_stderr; ec_short=$?; /tmp/aegisctl_issue250 execute create --spec /tmp/issue250_spec.yaml -f /tmp/issue250_spec.yaml --server http://127.0.0.1:18444 --token t --project pair_diagnosis --output json --dry-run 1> /tmp/issue250_exec_both_stdout 2> /tmp/issue250_exec_both_stderr; ec_both=$?; kill $server_pid; if [ "$ec_input" -ne 0 ] || [ "$ec_short" -ne 0 ]; then echo "single flag failed"; exit 1; fi; if [ "$ec_both" -ne 2 ]; then echo "exit expected 2, got $ec_both"; exit 2; fi; diff -u /tmp/issue250_exec_input_stdout /tmp/issue250_exec_short_stdout >/tmp/issue250_exec_diff || { echo "stdout mismatch"; cat /tmp/issue250_exec_diff; exit 3; }; grep -q "mutually exclusive" /tmp/issue250_exec_both_stderr || { echo "missing mutual exclusivity message"; cat /tmp/issue250_exec_both_stderr; exit 4; }`
**exit**: 0
**stdout** (first 20 lines):
```text
PASS
```
**stderr** (first 20 lines, if nonzero):
```text
(none)
```

### AC 4: `schema dump 输出中新名是 primary，老名标 deprecated:true。`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestSchemaDumpMarksDeprecatedAliases -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok   	aegis/cmd/aegisctl/cmd	0.022s
```
**stderr** (first 20 lines, if nonzero):
```text
(none)
```

### AC 5: `一个 integration test（仅一个）：对 execute submit 跑一次，断言 stderr 含 deprecation warning 且 stdout 与 execute create 输出 byte-identical。`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestExecuteSubmitAndCreateOutputAreByteIdentical -count=1 && rg -c "TestExecuteSubmitAndCreateOutputAreByteIdentical" cmd/aegisctl/cmd/*_test.go`
**exit**: 0
**stdout** (first 20 lines):
```text
ok   	aegis/cmd/aegisctl/cmd	0.032s
cmd/aegisctl/cmd/deprecation_test.go:1
```
**stderr** (first 20 lines, if nonzero):
```text
(none)
```

## Plan verify commands (from dev-agent)

### Plan 1: `TestValidationWorkflowCommandHelpMentionsMachineReadableFlags`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestValidationWorkflowCommandHelpMentionsMachineReadableFlags -count=1`
**exit**: 0
**stdout**: `ok   	aegis/cmd/aegisctl/cmd	0.017s`

### Plan 2: `TestExecuteCreateSpecFlagsAreMutuallyExclusive`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestExecuteCreateSpecFlagsAreMutuallyExclusive -count=1`
**exit**: 0
**stdout**: `ok   	aegis/cmd/aegisctl/cmd	0.035s`

### Plan 3: `TestSchemaDumpMarksDeprecatedAliases`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestSchemaDumpMarksDeprecatedAliases -count=1`
**exit**: 0
**stdout**: `ok   	aegis/cmd/aegisctl/cmd	0.022s`

### Plan 4: `TestExecuteSubmitAndCreateOutputAreByteIdentical`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestExecuteSubmitAndCreateOutputAreByteIdentical -count=1`
**exit**: 0
**stdout**: `ok   	aegis/cmd/aegisctl/cmd	0.035s`

## Overall
- PASS: 5 / 5
- FAIL: none
- UNVERIFIABLE: none
