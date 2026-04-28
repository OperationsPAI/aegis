# Review for issue #246 — PR #257

## Cascade preconditions
| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| (none) | none | N/A | N/A |

No submodule pointer changed in this PR (`git diff --name-status --submodule=short origin/main..HEAD` shows only regular file edits).

## Per-AC verdicts

### AC 1: 全局 `-o` flag whitelist (`table`, `json`, `ndjson`; command-level extras), invalid values return EXIT=2 before server call.
**verdict**: PASS
**command**: `cd /home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-246/AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectList_NDJSONOutput -count=1`
**exit**: 0
**stdout** (first 20 lines):
```
ok   	aegis/cmd/aegisctl/cmd	0.027s
```
**evidence links**: `AegisLab/src/cmd/aegisctl/cmd/root.go:174-229` (global validation), `AegisLab/src/cmd/aegisctl/cmd/inject_list_ndjson_test.go:47-92` (invalid `--output invalid-format` exits with `ExitCodeUsage` and sends zero requests).

### AC 2: `inject/task/trace/project/dataset/container/execute list` all support `-o ndjson`, print one JSON object per line, and do not emit `{items,pagination}` envelope to stdout.
**verdict**: PASS
**command**: `cd /home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-246/AegisLab/src && for f in cmd/aegisctl/cmd/{inject,task,trace,project,dataset,container,execute}.go; do echo "== $f"; rg -n "FormatNDJSON|case \"ndjson\"|PrintMetaJSON\(|PrintNDJSON\(" "$f"; done`
**exit**: 0
**stdout** (first 20 lines):
```
== cmd/aegisctl/cmd/inject.go
185:	case output.FormatNDJSON:
186:		if err := output.PrintMetaJSON(resp.Data.Pagination); err != nil {
187:			return output.PrintNDJSON(resp.Data.Items)
== cmd/aegisctl/cmd/task.go
93:	case output.FormatNDJSON:
94:		if err := output.PrintMetaJSON(resp.Data.Pagination); err != nil {
97:		return output.PrintNDJSON(items)
== cmd/aegisctl/cmd/trace.go
206:	case "ndjson":
207:		if err := output.PrintMetaJSON(resp.Data.Pagination); err != nil {
210:		return output.PrintNDJSON(resp.Data.Items)
== cmd/aegisctl/cmd/project.go
72:	case output.FormatNDJSON:
73:		if err := output.PrintMetaJSON(resp.Data.Pagination); err != nil {
76:		return output.PrintNDJSON(resp.Data.Items)
== cmd/aegisctl/cmd/dataset.go
62:	case output.FormatNDJSON:
63:		if err := output.PrintMetaJSON(resp.Data.Pagination); err != nil {
66:		return output.PrintNDJSON(resp.Data.Items)
```
**evidence links**: `AegisLab/src/cmd/aegisctl/cmd/{inject,task,trace,project,dataset,container,execute}.go` (see above output).

### AC 3: 分页元信息写到 stderr 一行 `_meta`，stdout 不受污染。
**verdict**: PASS
**command**: `cd /home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-246/AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectList_NDJSONOutput -count=1`
**exit**: 0
**stdout** (first 20 lines):
```
ok   	aegis/cmd/aegisctl/cmd	0.027s
```
**evidence links**: `AegisLab/src/cmd/aegisctl/output/output.go:46-55` (`PrintMetaJSON` writes to stderr), `AegisLab/src/cmd/aegisctl/cmd/inject_list_ndjson_test.go:67-82` (`_meta` parsed from stderr and `_meta` absent from stdout).

### AC 4: 一条 integration test 覆盖 `inject list -o ndjson`：每行合法 JSON，对象数与 total/page size 一致。
**verdict**: PASS
**command**: `cd /home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-246/AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectList_NDJSONOutput -count=1`
**exit**: 0
**stdout** (first 20 lines):
```
ok   	aegis/cmd/aegisctl/cmd	0.027s
```
**evidence links**: `AegisLab/src/cmd/aegisctl/cmd/inject_list_ndjson_test.go:11-93`.

## Overall
- PASS: 4 / 4
- FAIL: none
- UNVERIFIABLE: none
