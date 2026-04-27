#!/usr/bin/env bash
set -euo pipefail

python3 - <<'PY'
import http.server
import json
import os
import socketserver
import subprocess
import sys
import tempfile
import threading
from urllib.parse import parse_qs, urlparse

repo = '/home/ddq/AoyangSpace/aegis/.workbuddy/worktrees/issue-239/AegisLab/src'
bin_path = os.path.join(tempfile.gettempdir(), 'aegisctl-issue-239-review')
subprocess.run(['go', 'build', '-o', bin_path, './cmd/aegisctl'], cwd=repo, check=True)

class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        return
    def _json(self, status, payload):
        data = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    def do_GET(self):
        path = urlparse(self.path)
        qs = parse_qs(path.query)
        if path.path == '/api/v2/projects':
            return self._json(200, {'code': 200, 'message': 'ok', 'data': {'items': [{'id': 7, 'name': 'demo'}], 'pagination': {'page': 1, 'size': 100, 'total': 1, 'total_pages': 1}}})
        if path.path == '/api/v2/projects/7/injections':
            if qs.get('size') == ['500']:
                return self._json(400, {'code': 400, 'message': 'invalid size', 'data': None})
            return self._json(200, {'code': 200, 'message': 'ok', 'data': {'items': [{'id': 42, 'name': 'bogus'}], 'pagination': {'page': 1, 'size': 100, 'total': 1, 'total_pages': 1}}})
        if path.path == '/api/v2/injections/42':
            return self._json(404, {'code': 404, 'message': 'not found', 'data': None})
        if path.path == '/api/v2/evaluations':
            return self._json(500, {'code': 500, 'message': 'temporary fail', 'data': None})
        if path.path == '/api/v2/projects/7/executions':
            body = b'not-json'
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        return self._json(404, {'code': 404, 'message': 'not found', 'data': None})
    def do_POST(self):
        path = urlparse(self.path)
        if path.path == '/api/v2/projects/7/injections/search':
            return self._json(500, {'code': 500, 'message': 'temporary fail', 'data': None})
        return self._json(404, {'code': 404, 'message': 'not found', 'data': None})

with socketserver.TCPServer(('127.0.0.1', 0), Handler) as server:
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    base = f'http://127.0.0.1:{port}'
    base_args = [bin_path, '--server', base, '--token', 'token']
    cases = [
        ('inject list --size 500', base_args + ['inject', 'list', '--project', 'demo', '--size', '500'], 2),
        ('inject search', base_args + ['inject', 'search', '--project', 'demo'], 10),
        ('eval list', base_args + ['eval', 'list'], 10),
        ('execute list', base_args + ['execute', 'list', '--project', 'demo'], 11),
        ('inject get bogus', base_args + ['inject', 'get', 'bogus', '--project', 'demo'], 7),
    ]
    failed = False
    for name, cmd, want in cases:
        proc = subprocess.run(cmd, cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        print(f'{name}: exit={proc.returncode} want={want}')
        if proc.stderr.strip():
            print(f'stderr: {proc.stderr.strip()}')
        if proc.returncode != want:
            failed = True
    server.shutdown()
    if failed:
        sys.exit(1)
PY
