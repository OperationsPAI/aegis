# Review for issue #243 — PR #263

## Cascade preconditions
| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| (none) | no submodule pointers changed between `origin/main` and `origin/workbuddy/issue-243` | n/a | n/a |

Checked with `git submodule status --recursive` (stdout empty, exit 0) and `git diff --raw origin/main origin/workbuddy/issue-243` (no `160000` gitlink entries).

## Per-AC verdicts
### AC1: "`aegisctl version` 命令存在，输出至少包含：版本号（git describe / tag）、git commit SHA、build time（UTC ISO8601）、最低支持的 server API minor 版本。"
**verdict**: PASS
**command**: `cd AegisLab && just build-aegisctl /tmp/aegisctl-243-ac1 >/dev/null && /tmp/aegisctl-243-ac1 version`
**exit**: 0
**stdout** (first 20 lines):
```
version: release-platform/v0.4.22-77-g2973059
commit: 2973059
build_time: 2026-04-27T09:19:06Z
min_server_api: 2
```

### AC2: "`aegisctl --version` 作为同效 alias，输出与 `version` 一致。"
**verdict**: PASS
**command**: `cd AegisLab && just build-aegisctl /tmp/aegisctl-243-ac2 >/dev/null && v1=$(/tmp/aegisctl-243-ac2 version -o json) && v2=$(/tmp/aegisctl-243-ac2 --version -o json) && printf "version=%s\nflag=%s\n" "$v1" "$v2" && test "$v1" = "$v2"`
**exit**: 0
**stdout** (first 20 lines):
```
version={
  "version": "release-platform/v0.4.22-77-g2973059",
  "commit": "2973059",
  "build_time": "2026-04-27T09:19:08Z",
  "min_server_api": "2"
}
flag={
  "version": "release-platform/v0.4.22-77-g2973059",
  "commit": "2973059",
  "build_time": "2026-04-27T09:19:08Z",
  "min_server_api": "2"
}
```

### AC3: "这些字段通过 `-ldflags "-X ..."` 在 `make` / `go build` 注入；构建脚本对应更新。"
**verdict**: PASS
**command**: `cd AegisLab && rg -n "build-aegisctl|ldflags|cmd\.version|cmd\.commit|cmd\.buildTime|minServerAPIVersion" justfile`
**exit**: 0
**stdout** (first 20 lines):
```
193:build-aegisctl output="/tmp/aegisctl":
200:    ldflags=(
201:      "-X aegis/cmd/aegisctl/cmd.version=${version}"
202:      "-X aegis/cmd/aegisctl/cmd.commit=${commit}"
203:      "-X aegis/cmd/aegisctl/cmd.buildTime=${build_time}"
204:      "-X aegis/cmd/aegisctl/cmd.minServerAPIVersion=2"
206:    cd {{src_dir}} && go build -ldflags "${ldflags[*]}" -o {{output}} ./cmd/aegisctl
```

### AC4: "`-o json` 时输出 JSON：`{"version":"...","commit":"...","build_time":"...","min_server_api":"..."}`。"
**verdict**: PASS
**command**: `cd AegisLab && just build-aegisctl /tmp/aegisctl-243-ac4 >/dev/null && json=$(/tmp/aegisctl-243-ac4 version -o json) && printf '%s\n' "$json" && python - <<'PYCHECK' "$json"
import json, sys
payload=json.loads(sys.argv[1])
assert set(payload)=={'version','commit','build_time','min_server_api'}
assert all(isinstance(payload[k], str) and payload[k].strip() for k in payload)
print('json-shape-ok')
PYCHECK`
**exit**: 0
**stdout** (first 20 lines):
```
{
  "version": "release-platform/v0.4.22-77-g2973059",
  "commit": "2973059",
  "build_time": "2026-04-27T09:19:10Z",
  "min_server_api": "2"
}
json-shape-ok
```

### AC5: "一个 integration test（仅一个）：跑 `aegisctl version -o json`，断言四个字段都非空。"
**verdict**: FAIL
**command**: `cd AegisLab && rg -n "func TestVersionJSONIncludesRequiredFields|runCLI\(|executeArgs\(|exec\.Command" src/cmd/aegisctl/cmd/version_test.go src/cmd/aegisctl/cmd/contract_test.go`
**exit**: 0
**stdout** (first 20 lines):
```
src/cmd/aegisctl/cmd/contract_test.go:103:func runCLI(t *testing.T, args ...string) cliRunResult {
src/cmd/aegisctl/cmd/contract_test.go:166:	code := executeArgs(args)
src/cmd/aegisctl/cmd/contract_test.go:221:	res := runCLI(t, "auth", "login", "--server", "http://example.test", "--key-id", "pk_test")
src/cmd/aegisctl/cmd/contract_test.go:234:	res := runCLI(t, "cluster", "preflight", "--check", "does-not-exist")
src/cmd/aegisctl/cmd/contract_test.go:249:	res := runCLI(t, "cluster", "preflight", "--config", cfgPath, "--check", "db.mysql")
src/cmd/aegisctl/cmd/contract_test.go:275:	res := runCLI(t, "wait", "trace-1", "--server", "http://example.test", "--token", "token", "--output", "json", "--interval", "0", "--timeout", "1")
src/cmd/aegisctl/cmd/contract_test.go:292:	res := runCLI(t, "trace", "get", "trace-1", "--server", "http://example.test")
src/cmd/aegisctl/cmd/version_test.go:9:func TestVersionJSONIncludesRequiredFields(t *testing.T) {
src/cmd/aegisctl/cmd/version_test.go:28:		res := runCLI(t, "version", "--output", "json")
src/cmd/aegisctl/cmd/version_test.go:52:		resVersion := runCLI(t, "version", "-o", "json")
src/cmd/aegisctl/cmd/version_test.go:53:		resFlag := runCLI(t, "--version", "--output", "json")
src/cmd/aegisctl/cmd/version_test.go:67:		res := runCLI(t, "version", "--output")
```
**notes**: The only added test calls `runCLI()` and `executeArgs()` in-process instead of invoking a built `aegisctl` binary. See `AegisLab/src/cmd/aegisctl/cmd/version_test.go:28`, `AegisLab/src/cmd/aegisctl/cmd/version_test.go:52`, `AegisLab/src/cmd/aegisctl/cmd/version_test.go:67`, `AegisLab/src/cmd/aegisctl/cmd/contract_test.go:103`, and `AegisLab/src/cmd/aegisctl/cmd/contract_test.go:166`.

### PLAN1: "Plan step 1 verify: `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestVersionCommandPayload -count=1`"
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestVersionCommandPayload -count=1`
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

### PLAN2: "Plan step 2 verify: `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestVersionAlias -count=1`"
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestVersionAlias -count=1`
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

### PLAN3: "Plan step 3 verify: `cd AegisLab && just build-aegisctl output=/tmp/aegisctl && /tmp/aegisctl version -o json`"
**verdict**: FAIL
**command**: `cd AegisLab && just build-aegisctl output=/tmp/aegisctl && /tmp/aegisctl version -o json`
**exit**: 0
**stdout** (first 20 lines):
```
[1;34m🔨 Building aegisctl...[0m
[1;32m✅ aegisctl built: output=/tmp/aegisctl[0m
{
  "version": "release-platform/v0.4.22-77-g2973059",
  "commit": "2973059",
  "build_time": "2026-04-27T09:13:07Z",
  "min_server_api": "2"
}
```
**notes**: The command reported `build_time` `2026-04-27T09:13:07Z`, but `AegisLab/justfile:199` rebuilds with the current UTC time on every invocation. The reused timestamp shows this command executed a stale `/tmp/aegisctl` instead of the binary supposedly rebuilt by the `just` call.

### PLAN4: "Plan step 4 verify: `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestVersionJSONIncludesRequiredFields -count=1`"
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/cmd/aegisctl/cmd -run TestVersionJSONIncludesRequiredFields -count=1`
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
- PASS: 4 / 9
- FAIL: AC5, PLAN1, PLAN2, PLAN3, PLAN4
- UNVERIFIABLE: none
