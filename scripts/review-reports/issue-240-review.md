# Review for issue #240 — PR #262

Reviewed commit: `93bf744f9274dc444f9cd6e51e784e8fc7de8df6`

## Cascade preconditions
| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| _none_ | `.gitmodules` absent; `git diff --submodule=short --name-only origin/main..origin/workbuddy/issue-240` shows only regular files | N/A | N/A |

**command**: `bash -lc 'if [ -f .gitmodules ]; then echo .gitmodules-present; else echo .gitmodules-absent; fi; echo ---; git diff --submodule=short --name-only origin/main..origin/workbuddy/issue-240; echo ---; git submodule status || true'`
**exit**: 0
**stdout** (first 20 lines):
```text
.gitmodules-absent
---
AegisLab/src/cmd/aegisctl/client/resolver.go
AegisLab/src/cmd/aegisctl/cmd/contract.go
AegisLab/src/cmd/aegisctl/cmd/inject.go
AegisLab/src/cmd/aegisctl/cmd/inject_integration_test.go
scripts/review-reports/issue-240-checks/ac1_get_by_name_and_id.py
scripts/review-reports/issue-240-checks/ac2_files_contract.py
scripts/review-reports/issue-240-checks/ac3_not_found.py
scripts/review-reports/issue-240-checks/ac4_download_atomic.py
scripts/review-reports/issue-240-review.md
---
```

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
**verdict**: PASS
**command**: `python3 scripts/review-reports/issue-240-checks/ac2_files_contract.py`
**exit**: 0
**stdout** (first 20 lines):
```text
{
  "files": {
    "code": 0,
    "stdout": "{\n  \"files\": [\n    {\n      \"name\": \"demo.log\",\n      \"path\": \"raw/demo.log\",\n      \"size\": \"10 B\"\n    }\n  ],\n  \"file_count\": 1,\n  \"dir_count\": 0\n}",
    "stderr": ""
  },
  "download_by_id": {
    "code": 0,
    "stdout": "",
    "stderr": "Downloaded 9 bytes to /tmp/tmpifz_enrf",
    "exists": true
  }
}
```

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
    "stdout": "{\n  \"path\": \"/tmp/issue240-ok-lk5jcb3n\",\n  \"size\": 22,\n  \"sha256\": \"3d9c69475b0f23610df5d9fb02a1f88f74c68b60da8af24aa78b90ebb81528e8\"\n}",
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
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.030s
```

### Plan AC 1: Verify command for “Fix `inject` resolver routing for name/ID lookups used by `inject get/files/download`, keeping project-scoped name resolution and direct numeric-ID support.”
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.030s
```

### Plan AC 2: Verify command for “Add structured resolver not-found output with top-3 nearest injection-name suggestions and ensure CLI maps it to exit code 7.”
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.030s
```

### Plan AC 3: Verify command for “Make `inject download --output-file` write atomically via `<path>.tmp` + fsync + rename + cleanup, and emit `{path,size,sha256}` on JSON success.”
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.030s
```

### Plan AC 4: Verify command for “Keep exactly one integration test covering `inject get` by name and id plus interrupted `download` cleanup.”
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.030s
```

## Overall
- PASS: 9 / 9
- FAIL: none
- UNVERIFIABLE: none
