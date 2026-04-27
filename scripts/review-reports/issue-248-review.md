# Review for issue #248 — PR #260

## Cascade preconditions
| submodule | remote branch | SHA match | FF-able |
|-----------|---------------|-----------|---------|
| none (no submodule pointer changes vs `origin/main...origin/workbuddy/issue-248`) | N/A | N/A | N/A |

**command**: `python - <<'PY'
import subprocess, sys
repo='/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-248'
res=subprocess.run(['git','diff','--raw','origin/main...origin/workbuddy/issue-248'],cwd=repo,capture_output=True,text=True,check=True)
submods=[]
for line in res.stdout.splitlines():
    parts=line.split('\t')
    meta=parts[0].split()
    if len(meta)>=5 and meta[0].startswith(':160000'):
        submods.append(parts[1])
print('submodule_pointer_changes', submods)
sys.exit(0)
PY`
**exit**: 0
**stdout** (first 20 lines):
```text
submodule_pointer_changes []
```

## Per-AC verdicts
### AC 1: `实现 internal/cli/clierr.CLIError 结构（字段见父 issue §3.5：type/message/cause/request_id/suggestion/retryable/exit_code）。`
**verdict**: PASS
**command**: `python - <<'PY'
from pathlib import Path
import re, sys
p = Path('/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-248/AegisLab/src/cmd/aegisctl/internal/cli/clierr/clierr.go')
text = p.read_text()
required = {
    'Type': 'json:"type"',
    'Message': 'json:"message"',
    'Cause': 'json:"cause"',
    'RequestID': 'json:"request_id"',
    'Suggestion': 'json:"suggestion"',
    'Retryable': 'json:"retryable"',
    'ExitCode': 'json:"exit_code"',
}
missing = [f'{name}:{tag}' for name, tag in required.items() if name not in text or tag not in text]
if 'type CLIError struct' not in text:
    missing.append('CLIError struct')
if 'func (e *CLIError) Error() string' not in text:
    missing.append('Error() method')
if missing:
    print('MISSING')
    print('\n'.join(missing))
    sys.exit(1)
print('CLIError fields verified:')
for name, tag in required.items():
    print(f'- {name} ({tag})')
print('- Error() method present')
PY`
**exit**: 0
**stdout** (first 20 lines):
```text
CLIError fields verified:
- Type (json:"type")
- Message (json:"message")
- Cause (json:"cause")
- RequestID (json:"request_id")
- Suggestion (json:"suggestion")
- Retryable (json:"retryable")
- ExitCode (json:"exit_code")
- Error() method present
```

### AC 2: `-o json` 与 `-o ndjson` 时 error 走 stderr 单行 JSON（agent 可解析）；默认 table/text 时 stderr 多行人类可读：第一行 `Error [<type>]: <message>`，附 `cause:` / `hint:` 缩进行。
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/output -run 'TestPrintCLIError_JSON|TestPrintCLIError_HumanReadable' -count=1`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/output	0.002s
```

### AC 3: `server 5xx 包装为 Error [server]: server returned HTTP <code>; cause: <body 摘要>; request_id=<id>，禁止 bare An unexpected error occurred 透出。`
**verdict**: FAIL
**command**: `python - <<'PY'
import http.server, socketserver, threading, subprocess, sys
repo = '/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-248/AegisLab/src'
subprocess.run(['go','build','-o','/tmp/aegisctl-248','./cmd/aegisctl'], cwd=repo, check=True)
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(500)
        self.send_header('Content-Type','application/json')
        self.send_header('X-Request-Id','req-123')
        self.end_headers()
        self.wfile.write(b'{"code":500,"message":"An unexpected error occurred"}')
    def log_message(self, *args):
        pass
with socketserver.TCPServer(('127.0.0.1', 0), H) as srv:
    port = srv.server_address[1]
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    p = subprocess.run(['/tmp/aegisctl-248','project','list','--server',f'http://127.0.0.1:{port}','--token','token'], capture_output=True, text=True)
    print('exit', p.returncode)
    print('stdout')
    print(p.stdout)
    print('stderr')
    print(p.stderr)
    leak = 'An unexpected error occurred' in p.stderr
    print('contains_unexpected_error_phrase', leak)
    srv.shutdown()
    sys.exit(1 if leak else 0)
PY`
**exit**: 1
**stdout** (first 20 lines):
```text
exit 10
stdout

stderr
Error [server]: server returned HTTP 500; cause: {"code":500,"message":"An unexpected error occurred"}; request_id=req-123
  cause: {"code":500,"message":"An unexpected error occurred"}
  hint: The request failed on the server side. Retry if this is a transient incident.
  retryable: true

contains_unexpected_error_phrase True
```
**stderr** (first 20 lines, if nonzero):
```text
```

### AC 4: `decode error 包装为 type=decode，附字段路径与期望/实际类型摘要。`
**verdict**: PASS
**command**: `python - <<'PY'
import http.server, socketserver, threading, subprocess, sys, json
repo = '/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-248/AegisLab/src'
subprocess.run(['go','build','-o','/tmp/aegisctl-248','./cmd/aegisctl'], cwd=repo, check=True)
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type','application/json')
        self.send_header('X-Request-Id','req-decode')
        self.end_headers()
        self.wfile.write(b'{"code":0,"message":"ok","data":{"items":[{"id":"bad-id","name":"broken","description":"","status":"active","created_at":"2026-01-01"}],"pagination":{"page":2,"size":20,"total":1,"total_pages":1}}}')
    def log_message(self, *args):
        pass
with socketserver.TCPServer(('127.0.0.1', 0), H) as srv:
    port = srv.server_address[1]
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    p = subprocess.run(['/tmp/aegisctl-248','project','list','--page','2','--server',f'http://127.0.0.1:{port}','--token','token','--output','ndjson'], capture_output=True, text=True)
    print('exit', p.returncode)
    print('stdout')
    print(p.stdout)
    print('stderr')
    print(p.stderr)
    payload = json.loads(p.stderr)
    ok = p.returncode == 11 and payload.get('type') == 'decode' and payload.get('exit_code') == 11 and 'field' in payload.get('cause','') and 'expected' in payload.get('cause','')
    print('decode_payload_ok', ok)
    srv.shutdown()
    sys.exit(0 if ok else 1)
PY`
**exit**: 0
**stdout** (first 20 lines):
```text
exit 11
stdout

stderr
{"type":"decode","message":"decode response: failed to decode server JSON payload","cause":"field \"data.items.id\": expected int, got string","request_id":"req-decode","suggestion":"Check that client and server response contracts are aligned.","retryable":false,"exit_code":11}

decode_payload_ok True
```

### AC 5: `一个 integration test（仅一个）：mock server 返回 500 + 一个 schema mismatch；断言 stderr JSON 形态正确，type 与 exit_code 字段对应表（10 / 11）。`
**verdict**: PASS
**command**: `python - <<'PY'
from pathlib import Path
import re, sys
p = Path('/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-248/AegisLab/src/cmd/aegisctl/cmd/contract_test.go')
text = p.read_text()
funcs = re.findall(r'^func\s+(Test\w+)\s*\(', text, re.M)
matching = [name for name in funcs if 'ServerAndDecodeErrors' in name]
print('matching_tests', matching)
checks = [
    'runCLI(t, "project", "list", "--server", server.URL, "--output", "json")' in text,
    'runCLI(t, "project", "list", "--page", "2", "--server", server.URL, "--output", "ndjson")' in text,
    'ExitCodeServer' in text,
    'ExitCodeDecode' in text,
]
print('has_server_json_and_decode_ndjson_and_exitcodes', all(checks))
if len(matching) != 1 or not all(checks):
    sys.exit(1)
PY`
**exit**: 0
**stdout** (first 20 lines):
```text
matching_tests ['TestServerAndDecodeErrorsEmitJSONStructuredOutput']
has_server_json_and_decode_ndjson_and_exitcodes True
```

### Mini-AC 6 (Plan subtask 1 verify command): `cd AegisLab/src && go test ./cmd/aegisctl/internal/cli/clierr ./cmd/aegisctl/output`
**verdict**: PASS
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/internal/cli/clierr ./cmd/aegisctl/output`
**exit**: 0
**stdout** (first 20 lines):
```text
?   	aegis/cmd/aegisctl/internal/cli/clierr	[no test files]
ok  	aegis/cmd/aegisctl/output	(cached)
```

### Mini-AC 7 (Plan subtask 2 verify command): `cd AegisLab/src && go test ./cmd/aegisctl/client -run 'TestClient'`
**verdict**: FAIL
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/client -run 'TestClient'`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/client	0.003s [no tests to run]
```

### Mini-AC 8 (Plan subtask 3 verify command): `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run 'Test.*IntegrationServerAndDecodeError'`
**verdict**: FAIL
**command**: `cd AegisLab/src && go test ./cmd/aegisctl/cmd -run 'Test.*IntegrationServerAndDecodeError'`
**exit**: 0
**stdout** (first 20 lines):
```text
ok  	aegis/cmd/aegisctl/cmd	0.016s [no tests to run]
```

### Mini-AC 9 (Plan subtask 4 verify command): `cd AegisLab/src && go build ./cmd/aegisctl`
**verdict**: PASS
**command**: `cd AegisLab/src && go build ./cmd/aegisctl`
**exit**: 0
**stdout** (first 20 lines):
```text
```

## Overall
- PASS: 6 / 9
- FAIL: AC 3; Mini-AC 7; Mini-AC 8
- UNVERIFIABLE: none
