# Review for issue #248 — PR #260

## Cascade preconditions

`git diff --raw origin/main origin/workbuddy/issue-248` reported no gitlink (`160000`) changes, so this PR does not bump any submodule pointers and no cascade branch/SHA checks were required.

| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| none | n/a | n/a | n/a |

## Per-AC verdicts

### AC 1: `实现 internal/cli/clierr.CLIError 结构（字段见父 issue §3.5：type/message/cause/request_id/suggestion/retryable/exit_code）。`
**verdict**: PASS
**command**: `./scripts/verify-issue-248-ac-1.sh`
**exit**: 0
**stdout** (first 20 lines):
```text
CLIError fields OK: type, message, cause, request_id, suggestion, retryable, exit_code
```

### AC 2: `-o json 与 -o ndjson 时 error 走 stderr 单行 JSON（agent 可解析）；默认 table/text 时 stderr 多行人类可读：第一行 Error [<type>]: <message>，附 cause: / hint: 缩进行。`
**verdict**: PASS
**command**: `./scripts/verify-issue-248-ac-2.sh`
**exit**: 0
**stdout** (first 20 lines):
```text
structured stderr rendering OK for json/ndjson and human formats
```

### AC 3: `server 5xx 包装为 Error [server]: server returned HTTP <code>; cause: <body 摘要>; request_id=<id>，禁止 bare An unexpected error occurred 透出。`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/client -run TestServerErrorsAreSanitized -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/client	0.003s
```

### AC 4: `decode error 包装为 type=decode，附字段路径与期望/实际类型摘要。`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestIntegrationServerAndDecodeErrorsEmitJSONStructuredOutput -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.040s
```

### AC 5: `一个 integration test（仅一个）：mock server 返回 500 + 一个 schema mismatch；断言 stderr JSON 形态正确，type 与 exit_code 字段对应表（10 / 11）。`
**verdict**: PASS
**command**: `./scripts/verify-issue-248-ac-5.sh`
**exit**: 0
**stdout** (first 20 lines):
```text
single integration test present with server+decode assertions and exit codes 10/11
```

### Mini-AC 1 (dev plan subtask 1 verify): `cd AegisLab/src && go test ./cmd/aegisctl/client -run TestServerErrorsAreSanitized -count=1`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/client -run TestServerErrorsAreSanitized -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/client	0.003s
```

### Mini-AC 2 (dev plan subtask 2 verify): `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestIntegrationServerAndDecodeErrorsEmitJSONStructuredOutput -count=1`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestIntegrationServerAndDecodeErrorsEmitJSONStructuredOutput -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.022s
```

### Mini-AC 3 (dev plan subtask 3 verify): `cd AegisLab/src && go test ./cmd/aegisctl/internal/cli/clierr ./cmd/aegisctl/output && go build ./cmd/aegisctl`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/internal/cli/clierr ./cmd/aegisctl/output && go build ./cmd/aegisctl`
**exit**: 0
**stdout** (first 20 lines):
```text
?   	aegis/cmd/aegisctl/internal/cli/clierr	[no test files]
ok  	aegis/cmd/aegisctl/output	(cached)
```

## Overall
- PASS: 8 / 8
- FAIL: none
- UNVERIFIABLE: none
