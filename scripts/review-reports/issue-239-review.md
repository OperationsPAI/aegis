# Review for issue #239 — PR #264

## Cascade preconditions
| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| none (no submodule pointer changes in `origin/main...origin/workbuddy/issue-239`) | n/a | n/a | n/a |

## Per-AC verdicts
### AC 1: 实现单一 `internal/cli/exitcode` 包，把任何 `error` 映射到上表退出码；所有 cobra command 的 `RunE` 错误返回都流过这个映射，禁止直接 `os.Exit(0)`。
**verdict**: PASS
**command**: `bash scripts/verify-issue-239-ac-1.sh`
**exit**: 0
**stdout** (first 20 lines):
```
AC1 evidence: exitcode hub + root handler + no os.Exit(0) in cmd package
AegisLab/src/cmd/aegisctl/cmd/contract.go:115:	return executeError(err)
AegisLab/src/cmd/aegisctl/cmd/contract.go:118:func executeError(err error) int {
AegisLab/src/cmd/aegisctl/cmd/contract.go:132:	return exitcode.ErrorMessage(err)
AegisLab/src/cmd/aegisctl/cmd/contract.go:136:	return exitcode.ForError(err)
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:1:package exitcode
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:84:func ErrorMessage(err error) string {
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:92:func ForError(err error) int {
AegisLab/src/cmd/aegisctl/cmd/contract.go:115:	return executeError(err)
AegisLab/src/cmd/aegisctl/cmd/contract.go:118:func executeError(err error) int {
AegisLab/src/cmd/aegisctl/cmd/contract.go:132:	return exitcode.ErrorMessage(err)
AegisLab/src/cmd/aegisctl/cmd/contract.go:136:	return exitcode.ForError(err)
```

### AC 2: HTTP 状态码 → 退出码：4xx-validation→2、401/403→3、404→7、409→8、5xx→10；JSON decode 失败→11。
**verdict**: PASS
**command**: `bash scripts/verify-issue-239-ac-2.sh`
**exit**: 0
**stdout** (first 20 lines):
```
AC2 evidence: HTTP/decode mapping lines
AegisLab/src/cmd/aegisctl/client/client.go:46:// DecodeError wraps JSON decoding failures when the API returns a non-conformant
AegisLab/src/cmd/aegisctl/client/client.go:48:type DecodeError struct {
AegisLab/src/cmd/aegisctl/client/client.go:53:func (e *DecodeError) Error() string {
AegisLab/src/cmd/aegisctl/client/client.go:57:func (e *DecodeError) Unwrap() error {
AegisLab/src/cmd/aegisctl/client/client.go:138:			return &DecodeError{Body: respBody, Err: err}
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:105:		case apiErr.StatusCode == 401 || apiErr.StatusCode == 403:
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:107:		case apiErr.StatusCode == 404:
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:109:		case apiErr.StatusCode == 409:
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:111:		case apiErr.StatusCode >= 400 && apiErr.StatusCode <= 499:
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:112:			return CodeUsage
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:113:		case apiErr.StatusCode >= 500 && apiErr.StatusCode <= 599:
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:114:			return CodeServerError
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:118:	var decodeErr *client.DecodeError
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:120:		return CodeDecodeFailure
AegisLab/src/cmd/aegisctl/internal/cli/exitcode/exitcode.go:137:		return CodeUsage
```

### AC 3: `auth login` 在缺 `--username` 与 `--key-id` 任一时 EXIT=2（保持 stderr 已有 message）。
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestAuthLoginMissingIdentityUsesUsageExitCode -count=1`
**exit**: 0
**stdout** (first 20 lines):
```
ok  	aegis/cmd/aegisctl/cmd	0.030s
```

### AC 4: `inject list --size 500`（越界）EXIT=2；`inject search` server 500 EXIT=10；`eval list` server 500 EXIT=10；`execute list` decode 失败 EXIT=11；`inject get <bogus>` 404 EXIT=7。
**verdict**: PASS
**command**: `bash scripts/verify-issue-239-ac-4.sh`
**exit**: 0
**stdout** (first 20 lines):
```
expected=2 actual=2 cmd=inject list --project demo --size 500
Error: API error 400: invalid size
expected=10 actual=10 cmd=inject search --project demo
Error: API error 500: temporary fail
expected=10 actual=10 cmd=eval list
Error: API error 500: eval failed
expected=11 actual=11 cmd=execute list --project demo
Error: decode response: invalid character 'o' in literal null (expecting 'u')
expected=7 actual=7 cmd=inject get bogus --project demo
Error: API error 404: not found
```

### AC 5: 一个 integration test（仅一个）覆盖整张表：起 mock server 返回 400/401/404/409/500 + 一个 decode-fail body，断言 CLI 退出码与表一致。
**verdict**: PASS
**command**: `bash scripts/verify-issue-239-ac-5.sh`
**exit**: 0
**stdout** (first 20 lines):
```
AC5 evidence: single matrix test + status/decode cases
matrix_test_count=1
30:				w.WriteHeader(http.StatusConflict)
34:			w.WriteHeader(http.StatusNotFound)
37:				w.WriteHeader(http.StatusUnauthorized)
41:			w.WriteHeader(http.StatusNotFound)
45:					w.WriteHeader(http.StatusBadRequest)
63:				w.WriteHeader(http.StatusInternalServerError)
69:				w.WriteHeader(http.StatusNotFound)
77:				w.Write([]byte("not-json"))
82:		w.WriteHeader(http.StatusNotFound)
ok  	aegis/cmd/aegisctl/cmd	0.024s
```

### AC 6: Dev plan subtask 1 verify command: `git diff --stat origin/main...HEAD`。
**verdict**: PASS
**command**: `git diff --stat origin/main...HEAD`
**exit**: 0
**stdout** (first 20 lines):
```
 AegisLab/src/cmd/aegisctl/client/client.go         |  17 ++-
 AegisLab/src/cmd/aegisctl/cmd/contract.go          | 114 +++++------------
 AegisLab/src/cmd/aegisctl/cmd/contract_test.go     |  13 ++
 .../src/cmd/aegisctl/cmd/exitcode_matrix_test.go   | 133 +++++++++++++++++++
 AegisLab/src/cmd/aegisctl/cmd/schema.go            |  22 ++--
 .../cmd/aegisctl/internal/cli/exitcode/exitcode.go | 141 +++++++++++++++++++++
 6 files changed, 347 insertions(+), 93 deletions(-)
```

### AC 7: Dev plan subtask 2 verify command: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run 'TestAuthLoginMissingIdentityUsesUsageExitCode|TestAuthLoginMissingSecretUsesUsageExitCode|TestExitCodeContractMatrix' -count=1`。
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run 'TestAuthLoginMissingIdentityUsesUsageExitCode|TestAuthLoginMissingSecretUsesUsageExitCode|TestExitCodeContractMatrix' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```
ok  	aegis/cmd/aegisctl/cmd	0.025s
```

### AC 8: Dev plan subtask 3 verify commands: `gh pr view 264 ... && gh issue view 239 ...`。
**verdict**: PASS
**command**: `gh pr view 264 -R OperationsPAI/aegis --json body,url,state && gh issue view 239 -R OperationsPAI/aegis --json labels`
**exit**: 0
**stdout** (first 20 lines):
```
{"body":"## Summary\n- Centralized `aegisctl` exit-code handling in `internal/cli/exitcode` and routed root command failures through one mapping path.\n- Mapped API 4xx/401/403/404/409/5xx and JSON decode failures to the locked exit-code contract, including the `auth login` missing-identity usage exit.\n- Kept the PR scoped to `AegisLab` by dropping review-agent evidence artifacts that had leaked into the issue branch.\n\n## Subtask results\n- subtask-1 (audit branch drift and keep only issue #239 scope) — DONE\n  verify: `git diff --stat origin/main...HEAD` → exit 0, PR diff reduced to the 6 intended `AegisLab/src/cmd/aegisctl/*` files only.\n- subtask-2 (verify auth usage exit + matrix integration test from the real Go module root) — DONE\n  verify: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run 'TestAuthLoginMissingIdentityUsesUsageExitCode|TestAuthLoginMissingSecretUsesUsageExitCode|TestExitCodeContractMatrix' -count=1` → exit 0, `ok   aegis/cmd/aegisctl/cmd  0.038s`.\n- subtask-3 (repair PR/issue handoff artifacts with runnable commands only) — DONE\n  verify: `gh pr view 264 -R OperationsPAI/aegis --json body,url,state && gh issue view 239 -R OperationsPAI/aegis --json labels` → exit 0, PR body/URL/state resolved and issue labels reflected the handoff state.\n\n## Submodule changes\n- AegisLab: added `src/cmd/aegisctl/internal/cli/exitcode`, updated client decode errors, rewired `cmd/contract.go`, and added the focused auth + matrix tests.\n- AegisLab-frontend: — not modified\n- chaos-experiment: — not modified\n- rcabench-platform: — not modified\n\n## Workspace-level changes\n- none\n\n## Known gaps / blockers\n- none\n\nFixes #239\n","state":"OPEN","url":"https://github.com/OperationsPAI/aegis/pull/264"}
{"labels":[{"id":"LA_kwDOSFIuQM8AAAACfs3a5g","name":"workbuddy","description":"Opt issue into the workbuddy state machine","color":"5319E7"},{"id":"LA_kwDOSFIuQM8AAAACfs3fPg","name":"status:reviewing","description":"Review agent is verifying acceptance criteria","color":"D93F0B"}]}
```

## Overall
- PASS: 8 / 8
- FAIL: none
- UNVERIFIABLE: none
