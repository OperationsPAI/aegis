# Review for issue #240 — PR #262

## Cascade preconditions
**command**: `git submodule status && echo '--- changed gitlinks vs main ---' && git diff --raw origin/main...origin/workbuddy/issue-240 | awk '$1 ~ /^:/ && $6 == "160000" {print}'`
**exit**: 0
**stdout** (first 20 lines):
```text
--- changed gitlinks vs main ---
```

| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| none (no gitlink changes vs `origin/main`) | n/a | n/a | n/a |

## Per-AC verdicts
### AC 1: `aegisctl inject get <name>` 与 `aegisctl inject get <numeric_id>` 都能返回正确的 injection 详情（任选一条 `inject list` 列出的对象验证）。
**verdict**: PASS
**command**: `python3 scripts/review-reports/issue-240-checks/ac1_get_by_name_and_id.py`
**exit**: 0
**stdout** (first 20 lines):
```text
{
  "by_name": {
    "code": 0,
    "stdout": "{\n  \"id\": 744,\n  \"name\": \"otel-demo23-recommendation-pod-failure-4t2mpb\",\n  \"state\": \"build_success\"\n}",
    "stderr": ""
  },
  "by_id": {
    "code": 0,
    "stdout": "{\n  \"id\": 744,\n  \"name\": \"otel-demo23-recommendation-pod-failure-4t2mpb\",\n  \"state\": \"build_success\"\n}",
    "stderr": ""
  }
}
```

### AC 2: `aegisctl inject files <name>` 与 `aegisctl inject download <name> --output-file <path>` 在合法 name/ID 上都不再 404。
**verdict**: FAIL
**command**: `python3 scripts/review-reports/issue-240-checks/ac2_files_contract.py`
**exit**: 1
**stdout** (first 20 lines):
```text
{
  "files": {
    "code": 1,
    "stdout": "",
    "stderr": "Error: decode response: json: cannot unmarshal object into Go struct field APIResponse[[]aegis/cmd/aegisctl/cmd.fileItem·3].data of type []cmd.fileItem\nexit status 1"
  },
  "download_by_id": {
    "code": 0,
    "stdout": "",
    "stderr": "Downloaded 9 bytes to /tmp/tmpjvw08lso",
    "exists": true
  }
}
```
**stderr** (first 20 lines, if nonzero):
```text
```

Observed failure matches the current code shape mismatch: `inject files` decodes `APIResponse[[]fileItem]` in `AegisLab/src/cmd/aegisctl/cmd/inject.go:367`, but the backend contract returns `data.files` / `file_count` / `dir_count` in `AegisLab/src/module/injection/api_types.go:915`. The new test also mocks the wrong wire shape (`data` as a bare array) in `AegisLab/src/cmd/aegisctl/cmd/inject_integration_test.go:58`.

### AC 3: resolver 失败时 stderr 输出结构化 not_found 错误，并附 3 个最近邻 name 作 suggestion；EXIT=7（依赖 #237-退出码中枢子 issue）。
**verdict**: PASS
**command**: `python3 scripts/review-reports/issue-240-checks/ac3_not_found.py`
**exit**: 0
**stdout** (first 20 lines):
```text
{
  "code": 7,
  "stdout": "",
  "stderr": "Error: {\"type\":\"not_found\",\"resource\":\"injection\",\"query\":\"otel-demo23-recommendation-pod-failure-4t2zzz\",\"project_id\":7,\"suggestions\":[\"otel-demo23-recommendation-pod-failure-4t2mpa\",\"otel-demo23-recommendation-pod-failure-4t2mpb\",\"otel-demo23-recommendation-pod-failure-4t2mqa\"]}"
}
```

### AC 4: `inject download` 写文件用 `<path>.tmp` → `rename`：失败时清理 tmp、最终路径不存在；成功时 stdout（`-o json`）输出 `{path,size,sha256}`。
**verdict**: PASS
**command**: `python3 scripts/review-reports/issue-240-checks/ac4_download_atomic.py`
**exit**: 0
**stdout** (first 20 lines):
```text
{
  "success": {
    "code": 0,
    "stdout": "{\n  \"path\": \"/tmp/issue240-ok-9rr__vc_\",\n  \"size\": 22,\n  \"sha256\": \"3d9c69475b0f23610df5d9fb02a1f88f74c68b60da8af24aa78b90ebb81528e8\"\n}",
    "stderr": "",
    "path_exists": true,
    "tmp_exists": false,
    "actual_sha256": "3d9c69475b0f23610df5d9fb02a1f88f74c68b60da8af24aa78b90ebb81528e8"
  },
  "failure": {
    "code": 2,
    "stdout": "",
    "stderr": "Error: write output file: unexpected EOF",
    "path_exists": false,
    "tmp_exists": false
  }
}
```

### AC 5: 一个 integration test（**仅一个**）：mock server 返回一个 injection 的 list + get；CLI 用 name 与 id 各调一次 `inject get` 都成功；并断言 `download` 在中途断流时不留 partial 文件。
**verdict**: PASS
**command**: `set -euo pipefail; count=$(grep -c '^func Test' AegisLab/src/cmd/aegisctl/cmd/inject_integration_test.go); echo "test_functions=$count"; cd AegisLab/src; go test ./cmd/aegisctl/cmd -run '^TestInjectGetByNameAndIdAndDownloadAtomicFailure$' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
test_functions=1
ok  	aegis/cmd/aegisctl/cmd	0.028s
```

### Mini-AC 6: dev plan verify command `cd AegisLab && go test ./src/... -run TestResolverInjectionIDOrName -count=1`
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/... -run TestResolverInjectionIDOrName -count=1`
**exit**: 1
**stdout** (first 20 lines):
```text
FAIL	./src/... [setup failed]
# ./src/...
pattern ./src/...: directory prefix src does not contain main module or its selected dependencies
FAIL
```

### Mini-AC 7: dev plan verify command `cd AegisLab && go test ./src/... -run TestResolverInjectionIDOrNameNotFound -count=1`
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/... -run TestResolverInjectionIDOrNameNotFound -count=1`
**exit**: 1
**stdout** (first 20 lines):
```text
FAIL	./src/... [setup failed]
# ./src/...
pattern ./src/...: directory prefix src does not contain main module or its selected dependencies
FAIL
```

### Mini-AC 8: dev plan verify command `cd AegisLab && go test ./src/... -run TestInjectDownloadOutputFileAtomicAndReportsMetadata -count=1`
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/... -run TestInjectDownloadOutputFileAtomicAndReportsMetadata -count=1`
**exit**: 1
**stdout** (first 20 lines):
```text
FAIL	./src/... [setup failed]
# ./src/...
pattern ./src/...: directory prefix src does not contain main module or its selected dependencies
FAIL
```

### Mini-AC 9: dev plan verify command `cd AegisLab && go test ./src/... -run TestInjectGetAndDownloadIntegration -count=1`
**verdict**: FAIL
**command**: `cd AegisLab && go test ./src/... -run TestInjectGetAndDownloadIntegration -count=1`
**exit**: 1
**stdout** (first 20 lines):
```text
FAIL	./src/... [setup failed]
# ./src/...
pattern ./src/...: directory prefix src does not contain main module or its selected dependencies
FAIL
```

## Overall
- PASS: 4 / 9
- FAIL: AC 2 (`inject files` still broken against the real `/files` response contract); Mini-AC 6; Mini-AC 7; Mini-AC 8; Mini-AC 9
- UNVERIFIABLE: none

Supporting check (not used as aggregate AC evidence): `cd AegisLab/src && go test ./cmd/aegisctl/...` exits 0, but that does not clear the AC 2 contract mismatch because the added integration test stubs the wrong JSON shape for `/api/v2/injections/{id}/files`.
