# Review for issue #243 — PR #263

## Cascade preconditions
Verification command for submodule pointer changes:

`git diff --raw origin/main...HEAD | awk '$1 ~ /^:/ && $6 ~ /^160000/ {print $0}'`

Exit: 0

Stdout (first 20 lines):
```text

```

No submodule pointers are bumped by `origin/workbuddy/issue-243` relative to `origin/main`, so there are no submodule remote-branch / SHA-match / fast-forward checks to run for this PR.

| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| (none) | n/a | n/a | n/a |

## Per-AC verdicts
### AC 1: "`aegisctl version` 命令存在，输出至少包含：版本号（git describe / tag）、git commit SHA、build time（UTC ISO8601）、最低支持的 server API minor 版本。"
**verdict**: PASS
**command**: `cd AegisLab && just build-aegisctl /tmp/aegisctl-243-ac1 >/tmp/issue243-ac1-build.log && out=$(/tmp/aegisctl-243-ac1 version) && printf '%s\n' "$out" && python - <<'PY' "$out" ... PY`
**exit**: 0
**stdout** (first 20 lines):
```text
version: release-platform/v0.4.22-79-gcb8bd89
commit: cb8bd89
build_time: 2026-04-27T09:39:06Z
min_server_api: 2
CHECK: plain output contains 4 required fields and UTC ISO8601 build_time
```

### AC 2: "`aegisctl --version` 作为同效 alias，输出与 `version` 一致。"
**verdict**: PASS
**command**: `cd AegisLab && just build-aegisctl /tmp/aegisctl-243-ac2 >/tmp/issue243-ac2-build.log && v=$(/tmp/aegisctl-243-ac2 version -o json) && a=$(/tmp/aegisctl-243-ac2 --version -o json) && printf 'version=%s\n' "$v" && printf -- '--version=%s\n' "$a" && python - <<'PY' "$v" "$a" ... PY`
**exit**: 0
**stdout** (first 20 lines):
```text
version={
  "version": "release-platform/v0.4.22-79-gcb8bd89",
  "commit": "cb8bd89",
  "build_time": "2026-04-27T09:39:15Z",
  "min_server_api": "2"
}
--version={
  "version": "release-platform/v0.4.22-79-gcb8bd89",
  "commit": "cb8bd89",
  "build_time": "2026-04-27T09:39:15Z",
  "min_server_api": "2"
}
CHECK: outputs are identical
```

### AC 3: "这些字段通过 `-ldflags \"-X ...\"` 在 `make` / `go build` 注入；构建脚本对应更新。"
**verdict**: PASS
**command**: `python - <<'PY' ... PY && cd AegisLab && just build-aegisctl /tmp/aegisctl-243-ac3 >/tmp/issue243-ac3-build.log && /tmp/aegisctl-243-ac3 version -o json`
**exit**: 0
**stdout** (first 20 lines):
```text
CHECK: justfile contains all 4 ldflags injections
{
  "version": "release-platform/v0.4.22-79-gcb8bd89",
  "commit": "cb8bd89",
  "build_time": "2026-04-27T09:39:26Z",
  "min_server_api": "2"
}
```

### AC 4: "`-o json` 时输出 JSON：`{"version":"...","commit":"...","build_time":"...","min_server_api":"..."}`。"
**verdict**: PASS
**command**: `cd AegisLab && just build-aegisctl /tmp/aegisctl-243-ac4 >/tmp/issue243-ac4-build.log && out=$(/tmp/aegisctl-243-ac4 version -o json) && printf '%s\n' "$out" && python - <<'PY' "$out" ... PY`
**exit**: 0
**stdout** (first 20 lines):
```text
{
  "version": "release-platform/v0.4.22-79-gcb8bd89",
  "commit": "cb8bd89",
  "build_time": "2026-04-27T09:39:35Z",
  "min_server_api": "2"
}
CHECK: JSON keys exactly match required schema
```

### AC 5: "一个 integration test（仅一个）：跑 `aegisctl version -o json`，断言四个字段都非空。"
**verdict**: PASS
**command**: `printf 'Test definitions in version_test.go:\n' && rg -n '^func Test' AegisLab/src/cmd/aegisctl/cmd/version_test.go && printf '\nBinary execution lines:\n' && rg -n 'exec\\.Command\\(|runBuiltAegisctl\\(' AegisLab/src/cmd/aegisctl/cmd/version_test.go && cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestVersionJSONIncludesRequiredFields -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
Test definitions in version_test.go:
12:func TestVersionJSONIncludesRequiredFields(t *testing.T) {

Binary execution lines:
16:	build := exec.Command(
35:		stdout, stderr, err := runBuiltAegisctl(t, binPath, "version", "-o", "json")
53:		versionStdout, versionStderr, err := runBuiltAegisctl(t, binPath, "version", "-o", "json")
58:		flagStdout, flagStderr, err := runBuiltAegisctl(t, binPath, "--version", "-o", "json")
69:		_, stderr, err := runBuiltAegisctl(t, binPath, "version", "--output")
97:func runBuiltAegisctl(t *testing.T, binPath string, args ...string) (string, string, error) {
100:	cmd := exec.Command(binPath, args...)
ok  	aegis/cmd/aegisctl/cmd	2.023s
```

### Plan 1: "Implement / verify `aegisctl version` payload path and `--version` alias inside `AegisLab/src/cmd/aegisctl/cmd`; verify: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run 'TestVersion(CommandPayload|Alias)$' -count=1`"
**verdict**: FAIL
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run 'TestVersion(CommandPayload|Alias)$' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.017s [no tests to run]
```

The command exits successfully, but it does not execute any tests, so it does not verify the plan item it was attached to.

### Plan 2: "Wire build metadata injection via `-ldflags` in `AegisLab/justfile` and verify a freshly built binary emits the required fields; verify: `cd AegisLab && just build-aegisctl /tmp/aegisctl-243 && /tmp/aegisctl-243 version -o json`"
**verdict**: PASS
**command**: `cd AegisLab && just build-aegisctl /tmp/aegisctl-243 && /tmp/aegisctl-243 version -o json`
**exit**: 0
**stdout** (first 20 lines):
```text
\u001b[1;34m🔨 Building aegisctl...\u001b[0m
\u001b[1;32m✅ aegisctl built: /tmp/aegisctl-243\u001b[0m
{
  "version": "release-platform/v0.4.22-79-gcb8bd89",
  "commit": "cb8bd89",
  "build_time": "2026-04-27T09:40:01Z",
  "min_server_api": "2"
}
```

### Plan 3: "Replace the existing version coverage with one real-binary integration test that runs `aegisctl version -o json` and checks the four required fields plus a meaningful failure path; verify: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestVersionJSONIncludesRequiredFields -count=1`"
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestVersionJSONIncludesRequiredFields -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	2.124s
```

### Plan 4: "Update the parent repo submodule pointer / PR metadata and verify the parent diff is clean for issue #243; verify: `git status --short && git diff --submodule=log --stat`"
**verdict**: PASS
**command**: `git status --short && printf -- '---\n' && git diff --submodule=log --stat`
**exit**: 0
**stdout** (first 20 lines):
```text
---
```

## Overall
- PASS: 8 / 9
- FAIL: Plan 1 verify command (`go test -run 'TestVersion(CommandPayload|Alias)$'`) matches no tests, so the plan claim is not actually verified.
- UNVERIFIABLE: none
