#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
port_file=$(mktemp)
server_log=$(mktemp)
python3 - <<'PY' >"$port_file" 2>"$server_log" &
import json
import socket
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

class Handler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass

    def _json(self, status, payload):
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(payload).encode())

    def do_GET(self):
        p = urlparse(self.path)
        qs = parse_qs(p.query)
        if p.path == '/api/v2/projects':
            return self._json(200, {'code': 200, 'message': 'ok', 'data': {'items': [{'id': 7, 'name': 'demo'}], 'pagination': {'page': 1, 'size': 100, 'total': 1, 'total_pages': 1}}})
        if p.path == '/api/v2/projects/7/injections':
            if qs.get('size') == ['500']:
                return self._json(400, {'code': 400, 'message': 'invalid size', 'data': None})
            return self._json(200, {'code': 200, 'message': 'ok', 'data': {'items': [{'id': 42, 'name': 'bogus'}], 'pagination': {'page': 1, 'size': 100, 'total': 1, 'total_pages': 1}}})
        if p.path == '/api/v2/injections/42':
            return self._json(404, {'code': 404, 'message': 'not found', 'data': None})
        if p.path == '/api/v2/projects/7/executions':
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(b'not-json')
            return
        if p.path == '/api/v2/evaluations':
            return self._json(500, {'code': 500, 'message': 'eval failed', 'data': None})
        return self._json(404, {'code': 404, 'message': 'not found', 'data': None})

    def do_POST(self):
        if self.path == '/api/v2/projects/7/injections/search':
            return self._json(500, {'code': 500, 'message': 'temporary fail', 'data': None})
        return self._json(404, {'code': 404, 'message': 'not found', 'data': None})

with socket.socket() as s:
    s.bind(('127.0.0.1', 0))
    port = s.getsockname()[1]
server = HTTPServer(('127.0.0.1', port), Handler)
print(port, flush=True)
server.serve_forever()
PY
server_pid=$!
cleanup() {
  kill "$server_pid" >/dev/null 2>&1 || true
  rm -f "$port_file" "$server_log" /tmp/issue239-ac4.out /tmp/issue239-ac4.err /tmp/aegisctl-issue239
}
trap cleanup EXIT
for _ in $(seq 1 50); do
  if [[ -s "$port_file" ]]; then
    break
  fi
  sleep 0.1
done
port=$(cat "$port_file")
cd AegisLab/src
go build -o /tmp/aegisctl-issue239 ./cmd/aegisctl
cases=(
  '2|inject list --project demo --size 500'
  '10|inject search --project demo'
  '10|eval list'
  '11|execute list --project demo'
  '7|inject get bogus --project demo'
)
for case in "${cases[@]}"; do
  expected=${case%%|*}
  args=${case#*|}
  set +e
  /tmp/aegisctl-issue239 --server "http://127.0.0.1:${port}" --token token $args >/tmp/issue239-ac4.out 2>/tmp/issue239-ac4.err
  actual=$?
  set -e
  echo "expected=$expected actual=$actual cmd=$args"
  sed -n '1,3p' /tmp/issue239-ac4.err
  if [[ "$actual" != "$expected" ]]; then
    echo 'stdout:'
    sed -n '1,20p' /tmp/issue239-ac4.out
    echo 'stderr:'
    sed -n '1,20p' /tmp/issue239-ac4.err
    exit 1
  fi
done
