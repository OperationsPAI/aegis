# Review for issue #240 — PR #262

## Cascade preconditions
| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| none (no `.gitmodules`, no `160000` diff entries) | n/a | n/a | n/a |

**command**: `python3 - <<'PY'
import subprocess
raw = subprocess.run(['git','diff','--raw','origin/main...origin/workbuddy/issue-240'], capture_output=True, text=True, check=True).stdout.splitlines()
submods = [line.split('\t',1)[1] for line in raw if line.startswith(':160000') or ' 160000 ' in line]
print('gitmodules_exists=' + ('yes' if subprocess.run(['bash','-lc','test -f .gitmodules']).returncode == 0 else 'no'))
print('submodule_pointer_changes=' + (','.join(submods) if submods else '<none>'))
status = subprocess.run(['git','submodule','status'], capture_output=True, text=True)
print('git_submodule_status=' + (status.stdout.strip() if status.stdout.strip() else '<empty>'))
PY`
**exit**: 0
**stdout** (first 20 lines):
```text
gitmodules_exists=no
submodule_pointer_changes=<none>
git_submodule_status=<empty>
```

## Per-AC verdicts

### AC 1: `aegisctl inject get <name>` 与 `aegisctl inject get <numeric_id>` 都能返回正确的 injection 详情
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

### AC 2: `inject files <name>` 与 `inject download <name> --output-file <path>` 在合法 name/ID 上都不再 404
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
    "stderr": "Downloaded 9 bytes to /tmp/tmp15yydcq9",
    "exists": true
  }
}
```
**stderr** (first 20 lines, if nonzero):
```text
```

Relevant code inspected in this turn:
- `AegisLab/src/cmd/aegisctl/cmd/inject.go:360-380` still decodes `/files` into `APIResponse[[]fileItem]`.
- `AegisLab/src/module/injection/api_types.go:915-918` defines the backend contract as `data.files`, `file_count`, and `dir_count`.

### AC 3: resolver 失败时 stderr 输出结构化 `not_found` 错误，并附 3 个最近邻 name suggestion；EXIT=7
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

### AC 4: `inject download` 写文件用 `<path>.tmp` → `rename`；失败时清理 tmp、最终路径不存在；成功时 `-o json` 输出 `{path,size,sha256}`
**verdict**: PASS
**command**: `python3 scripts/review-reports/issue-240-checks/ac4_download_atomic.py`
**exit**: 0
**stdout** (first 20 lines):
```text
{
  "success": {
    "code": 0,
    "stdout": "{\n  \"path\": \"/tmp/issue240-ok-idct1_ko\",\n  \"size\": 22,\n  \"sha256\": \"3d9c69475b0f23610df5d9fb02a1f88f74c68b60da8af24aa78b90ebb81528e8\"\n}",
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

### AC 5: 一个 integration test（仅一个）：mock server 返回一个 injection 的 list + get；CLI 用 name 与 id 各调一次 `inject get` 都成功；并断言 `download` 在中途断流时不留 partial 文件
**verdict**: PASS
**command**: `bash -lc 'count=$(rg -n "^func Test" AegisLab/src/cmd/aegisctl/cmd/inject_integration_test.go | wc -l); echo "test_count=$count"; cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1'`
**exit**: 0
**stdout** (first 20 lines):
```text
test_count=1
ok  	aegis/cmd/aegisctl/cmd	0.028s
```

Relevant file inspected in this turn:
- `AegisLab/src/cmd/aegisctl/cmd/inject_integration_test.go:73-90` mocks `/api/v2/injections/744/files` as a bare array, which does not match the backend wire format even though the single test passes.

### Plan mini-AC 1: verify command for “Fix `inject` resolver routing for name/ID lookups …”
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.027s
```

### Plan mini-AC 2: verify command for “Add structured resolver not-found output …”
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.027s
```

### Plan mini-AC 3: verify command for “Make `inject download --output-file` write atomically …”
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.027s
```

### Plan mini-AC 4: verify command for “Keep exactly one integration test …”
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run TestInjectGetByNameAndIdAndDownloadAtomicFailure -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.027s
```

## Overall
- PASS: 8 / 9
- FAIL:
  - AC 2 (`inject files` still fails against the real `data.files` response shape)
- UNVERIFIABLE:
  - none
