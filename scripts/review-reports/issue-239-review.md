# Review for issue #239 — PR #264

## Cascade preconditions
| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| _none_ | n/a | n/a | n/a |

Observed with:
`python3 - <<'PY2'
import subprocess
raw = subprocess.run(['git','diff','--submodule=short','--raw','origin/main...origin/workbuddy/issue-239'], check=True, capture_output=True, text=True).stdout.strip().splitlines()
submodules = [line for line in raw if line.startswith(':160000')]
print('bumped_submodules=' + str(len(submodules)))
for line in submodules:
    print(line)
status = subprocess.run(['git','submodule','status'], check=True, capture_output=True, text=True).stdout.strip()
print('git_submodule_status=' + (status if status else '<empty>'))
PY2`
- exit: 0
```
bumped_submodules=0
git_submodule_status=<empty>
```

## Per-AC verdicts
### AC 1: "实现单一 `internal/cli/exitcode` 包，把任何 `error` 映射到上表退出码；所有 cobra command 的 `RunE` 错误返回都流过这个映射，禁止直接 `os.Exit(0)`。"
**verdict**: PASS
**command**: `./scripts/verify-issue-239-ac-1.sh`
**exit**: 0
**stdout** (first 20 lines):
```
OK: found AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go
OK: exitcode.ForError exists
OK: contract.go calls rootCmd.Execute()
OK: contract.go routes Execute() errors through executeError
OK: contract.go routes executeError through exitcode.ForError
OK: root Execute() returns executeArgs(os.Args[1:])
OK: main.go delegates process exit to cmd.Execute()
OK: no direct os.Exit(0) calls remain under src/cmd/aegisctl
```

### AC 2: "HTTP 状态码 → 退出码：4xx-validation→2、401/403→3、404→7、409→8、5xx→10；JSON decode 失败→11。"
**verdict**: PASS
**command**: `./scripts/verify-issue-239-ac-2.sh`
**exit**: 0
**stdout** (first 20 lines):
```
OK: 401/403 -> 3
OK: 404 -> 7
OK: 409 -> 8
OK: other 4xx -> 2
OK: 5xx -> 10
OK: decode failures wrapped as client.DecodeError
OK: DecodeError -> 11
```

### AC 3: "`auth login` 在缺 `--username` 与 `--key-id` 任一时 EXIT=2（保持 stderr 已有 message）。"
**verdict**: PASS
**command**: `go test ./cmd/aegisctl/cmd -run 'TestAuthLoginMissing(Identity|Secret)UsesUsageExitCode$' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```
ok  	aegis/cmd/aegisctl/cmd	0.029s
```

### AC 4: "`inject list --size 500`（越界）EXIT=2；`inject search` server 500 EXIT=10；`eval list` server 500 EXIT=10；`execute list` decode 失败 EXIT=11；`inject get <bogus>` 404 EXIT=7。"
**verdict**: PASS
**command**: `./scripts/verify-issue-239-ac-4.sh`
**exit**: 0
**stdout** (first 20 lines):
```
inject list --size 500: exit=2 want=2
stderr: Error: API error 400: invalid size
inject search: exit=10 want=10
stderr: Error: API error 500: temporary fail
eval list: exit=10 want=10
stderr: Error: API error 500: temporary fail
execute list: exit=11 want=11
stderr: Error: decode response: invalid character 'o' in literal null (expecting 'u')
inject get bogus: exit=7 want=7
stderr: Error: API error 404: not found
```

### AC 5: "一个 integration test（仅一个）覆盖整张表：起 mock server 返回 400/401/404/409/500 + 一个 decode-fail body，断言 CLI 退出码与表一致。"
**verdict**: PASS
**command**: `go test ./cmd/aegisctl/cmd -run '^TestExitCodeContractMatrix$' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```
ok  	aegis/cmd/aegisctl/cmd	0.041s
```

### Mini-AC 1 (dev plan): "验证 `go test ./src/cmd/aegisctl/cmd -run TestAuthLoginMissingSecretUsesUsageExitCode -count=1`。"
**verdict**: FAIL
**command**: `go test ./src/cmd/aegisctl/cmd -run TestAuthLoginMissingSecretUsesUsageExitCode -count=1`
**exit**: 1
**stdout** (first 20 lines):
```

```
**stderr** (first 20 lines, if nonzero):
```
go: cannot find main module, but found .git/config in /home/ddq/AoyangSpace/aegis
	to create a module there, run:
	cd ../../.. && go mod init
```

### Mini-AC 2 (dev plan): "验证 `cd AegisLab && go test ./src/cmd/aegisctl/client -run Test`。"
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/cmd/aegisctl/client -run Test`
**exit**: 1
**stdout** (first 20 lines):
```

```
**stderr** (first 20 lines, if nonzero):
```
go: cannot find main module, but found .git/config in /home/ddq/AoyangSpace/aegis
	to create a module there, run:
	cd ../../../.. && go mod init
```

### Mini-AC 3 (dev plan): "验证 `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestAuthLoginMissingSecretUsesUsageExitCode -count=1`。"
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestAuthLoginMissingSecretUsesUsageExitCode -count=1`
**exit**: 1
**stdout** (first 20 lines):
```

```
**stderr** (first 20 lines, if nonzero):
```
go: cannot find main module, but found .git/config in /home/ddq/AoyangSpace/aegis
	to create a module there, run:
	cd ../../../.. && go mod init
```

### Mini-AC 4 (dev plan): "验证 `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestExitCodeContractMatrix -count=1`。"
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestExitCodeContractMatrix -count=1`
**exit**: 1
**stdout** (first 20 lines):
```

```
**stderr** (first 20 lines, if nonzero):
```
go: cannot find main module, but found .git/config in /home/ddq/AoyangSpace/aegis
	to create a module there, run:
	cd ../../../.. && go mod init
```

## Overall
- PASS: 5 / 9
- FAIL:
  - Mini-AC 1 (dev plan): "验证 `go test ./src/cmd/aegisctl/cmd -run TestAuthLoginMissingSecretUsesUsageExitCode -count=1`。"
  - Mini-AC 2 (dev plan): "验证 `cd AegisLab && go test ./src/cmd/aegisctl/client -run Test`。"
  - Mini-AC 3 (dev plan): "验证 `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestAuthLoginMissingSecretUsesUsageExitCode -count=1`。"
  - Mini-AC 4 (dev plan): "验证 `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestExitCodeContractMatrix -count=1`。"
- UNVERIFIABLE: none
