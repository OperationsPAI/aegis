# Review for issue #241 — PR #261

## Cascade preconditions
Verification command:
`git diff --raw origin/main..origin/workbuddy/issue-241 | awk '$5 ~ /^160000$/ || $6 ~ /^160000$/ {print}'`

No gitlink entries were returned, so this PR does not bump any submodule pointers. The cascade branch / SHA / fast-forward checks were therefore not applicable.

| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| none | n/a | n/a | n/a |

## Per-AC verdicts
### AC 1: `aegisctl execute list` 在生产 server 上正确返回结果（不再 decode error）。
**verdict**: PASS
**command**: `cd AegisLab && just build-aegisctl >/tmp/issue241-ac1b-build.stdout 2>/tmp/issue241-ac1b-build.stderr && out=$(/tmp/aegisctl execute list --project pair_diagnosis --output json); printf '%s\n' "$out" | sed -n '1,20p'`
**exit**: 0
**stdout** (first 20 lines):
```text
{
  "items": [
    {
      "id": 508,
      "algorithm": "",
      "datapack": "",
      "state": "success",
      "duration": 34.176802,
      "created_at": "2026-04-27T17:26:46.299+08:00"
    },
    {
      "id": 507,
      "algorithm": "",
      "datapack": "",
      "state": "success",
      "duration": 13.676902,
      "created_at": "2026-04-27T17:24:55.285+08:00"
    },
    {
      "id": 506,
```

### AC 2: 客户端 struct 的 duration 字段类型与 server openapi 声明对齐；若不一致，记录在 PR description 里说明选择原因。
**verdict**: PASS
**command**: `printf '%s\n' '--- client type ---'; nl -ba AegisLab/src/cmd/aegisctl/cmd/execute.go | sed -n '124,136p'; printf '%s\n' '--- server openapi type ---'; nl -ba AegisLab/src/module/execution/api_types.go | sed -n '232,238p'`
**exit**: 0
**stdout** (first 20 lines):
```text
--- client type ---
   124	type executeListItem struct {
   125		ID        int     `json:"id"`
   126		Algorithm string  `json:"algorithm"`
   127		Datapack  string  `json:"datapack"`
   128		State     string  `json:"state"`
   129		Duration  float64 `json:"duration"`
   130		CreatedAt string  `json:"created_at"`
   131	}
   132	
   133	var (
   134		executeListPage int
   135		executeListSize int
   136	)
--- server openapi type ---
   232	// ExecutionResp represents execution summary information.
   233	type ExecutionResp struct {
   234		ID                 int                   `json:"id"`
   235		Duration           float64               `json:"duration"`
   236		State              consts.ExecutionState `json:"state" swaggertype:"string" enums:"Initial,Failed,Success"`
```

### AC 3: 一个 unit test（仅一个）：以 server 实际返回的一段真实 JSON sample 喂给 decoder，断言不报错、duration 字段值正确。
**verdict**: PASS
**command**: `cd AegisLab/src && sed -n '1,40p' cmd/aegisctl/cmd/execute_test.go && printf '%s\n' '--- test list ---' && rg -n '^func Test' cmd/aegisctl/cmd/execute_test.go && go test ./cmd/aegisctl/cmd -run TestDecodeExecuteListResponse -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
package cmd

import (
	"encoding/json"
	"testing"

	"aegis/cmd/aegisctl/client"
)

func TestDecodeExecuteListResponse(t *testing.T) {
	payload := `{"code":0,"message":"success","data":{"items":[{"id":508,"algorithm":"","datapack":"","state":"success","duration":34.176802,"created_at":"2026-04-27T17:26:46.299+08:00"}],"pagination":{"page":1,"size":20,"total":508,"total_pages":26}}}`

	var resp client.APIResponse[client.PaginatedData[executeListItem]]
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		t.Fatalf("decode execute list response: %v", err)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("unexpected item count: %d", len(resp.Data.Items))
	}
	if got := resp.Data.Items[0].Duration; got != 34.176802 {
```

### AC 4: decode 失败时（非本字段，未来其它字段）EXIT=11（依赖 #237-退出码中枢子 issue）。
**verdict**: PASS
**command**: `tmpdir=$(mktemp -d); cat >"$tmpdir/server.py" <<'PY' ... PY; python3 "$tmpdir/server.py" & just build-aegisctl >/tmp/issue241-ac4-build.stdout 2>/tmp/issue241-ac4-build.stderr; AEGIS_SERVER=http://127.0.0.1:18081 AEGIS_TOKEN=dummy /tmp/aegisctl execute list --project pair_diagnosis >/tmp/issue241-ac4-cli.stdout 2>/tmp/issue241-ac4-cli.stderr; printf 'exit=%s\n' "$status"; sed -n '1,20p' /tmp/issue241-ac4-cli.stderr`
**exit**: 0
**stdout** (first 20 lines):
```text
exit=11
Error: decode response: json: cannot unmarshal number into Go struct field executeListItem.data.items.created_at of type string
```

### Mini-AC 1 (`## Plan`): Tighten the decode regression test to use the real `aegisctl execute list` response type from `cmd/execute.go`, not a test-local stand-in.
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestDecodeExecuteListResponse -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.024s
```

### Mini-AC 2 (`## Plan`): Re-run the affected `aegisctl` packages to confirm the duration decode fix and exit-code path still compile and pass after the test move.
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/client ./cmd/aegisctl/cmd -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/client	0.005s
ok  	aegis/cmd/aegisctl/cmd	0.770s
```

### Mini-AC 3 (`## Plan`): Refresh the parent PR/issue handoff so review can continue with the corrected evidence and current branch state.
**verdict**: PASS
**command**: `gh pr view 261 -R OperationsPAI/aegis --json url,headRefName,state`
**exit**: 0
**stdout** (first 20 lines):
```text
{"headRefName":"workbuddy/issue-241","state":"OPEN","url":"https://github.com/OperationsPAI/aegis/pull/261"}
```

### Mini-AC 4 (`## Plan`): Build and run the real `aegisctl execute list` path against the configured server context.
**verdict**: PASS
**command**: `cd AegisLab && just build-aegisctl >/tmp/issue241-ac1b-build.stdout 2>/tmp/issue241-ac1b-build.stderr && out=$(/tmp/aegisctl execute list --project pair_diagnosis --output json); printf '%s\n' "$out" | sed -n '1,20p'`
**exit**: 0
**stdout** (first 20 lines):
```text
{
  "items": [
    {
      "id": 508,
      "algorithm": "",
      "datapack": "",
      "state": "success",
      "duration": 34.176802,
      "created_at": "2026-04-27T17:26:46.299+08:00"
    },
    {
      "id": 507,
      "algorithm": "",
      "datapack": "",
      "state": "success",
      "duration": 13.676902,
      "created_at": "2026-04-27T17:24:55.285+08:00"
    },
    {
      "id": 506,
```

## Overall
- PASS: 8 / 8
- FAIL: none
- UNVERIFIABLE: none
