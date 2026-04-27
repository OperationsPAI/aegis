import hashlib
import json
import os
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

PROJECT_NAME = 'pair_diagnosis'
PROJECT_ID = 7
OK_ID = 744
FAIL_ID = 745
OK_NAME = 'otel-demo23-recommendation-pod-failure-4t2mpb'
FAIL_NAME = 'otel-demo23-recommendation-pod-failure-broken'
OK_BODY = b'complete-download-body'

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def _json(self, payload, code=200):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = self.path.split('?', 1)[0]
        if path == '/api/v2/projects':
            return self._json({'code': 200, 'message': 'ok', 'data': {'items': [{'id': PROJECT_ID, 'name': PROJECT_NAME}], 'pagination': {'page': 1, 'size': 100, 'total': 1, 'total_pages': 1}}})
        if path == f'/api/v2/projects/{PROJECT_ID}/injections':
            return self._json({'code': 200, 'message': 'ok', 'data': {'items': [{'id': OK_ID, 'name': OK_NAME}, {'id': FAIL_ID, 'name': FAIL_NAME}], 'pagination': {'page': 1, 'size': 100, 'total': 2, 'total_pages': 1}}})
        if path == f'/api/v2/injections/{OK_ID}/download':
            self.send_response(200)
            self.send_header('Content-Type', 'application/zip')
            self.send_header('Content-Length', str(len(OK_BODY)))
            self.end_headers()
            self.wfile.write(OK_BODY)
            return
        if path == f'/api/v2/injections/{FAIL_ID}/download':
            body = b'broken transfer'
            self.send_response(200)
            self.send_header('Content-Type', 'application/zip')
            self.send_header('Content-Length', '32')
            self.end_headers()
            self.wfile.write(body)
            return
        return self._json({'code': 404, 'message': 'not found'}, 404)


server = HTTPServer(('127.0.0.1', 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
base = f'http://127.0.0.1:{server.server_port}'
bin_path = tempfile.mktemp(prefix='aegisctl-')
subprocess.run(['go', 'build', '-o', bin_path, './cmd/aegisctl'], cwd='AegisLab/src', check=True, capture_output=True, text=True)
out_ok = tempfile.mktemp(prefix='issue240-ok-')
out_fail = tempfile.mktemp(prefix='issue240-fail-')
common = [bin_path, '--server', base, '--token', 'token', '--project', PROJECT_NAME]
success = subprocess.run(common + ['inject', 'download', OK_NAME, '--output-file', out_ok, '--output', 'json'], cwd='AegisLab/src', capture_output=True, text=True)
failure = subprocess.run(common + ['inject', 'download', str(FAIL_ID), '--output-file', out_fail], cwd='AegisLab/src', capture_output=True, text=True)
stdout_json = json.loads(success.stdout) if success.stdout.strip() else {}
actual_hash = hashlib.sha256(open(out_ok, 'rb').read()).hexdigest() if os.path.exists(out_ok) else None
print(json.dumps({'success': {'code': success.returncode, 'stdout': success.stdout.strip(), 'stderr': success.stderr.strip(), 'path_exists': os.path.exists(out_ok), 'tmp_exists': os.path.exists(out_ok + '.tmp'), 'actual_sha256': actual_hash}, 'failure': {'code': failure.returncode, 'stdout': failure.stdout.strip(), 'stderr': failure.stderr.strip(), 'path_exists': os.path.exists(out_fail), 'tmp_exists': os.path.exists(out_fail + '.tmp')}}, ensure_ascii=False, indent=2))
if success.returncode != 0:
    raise SystemExit(1)
if stdout_json.get('path') != out_ok or stdout_json.get('size') != len(OK_BODY) or stdout_json.get('sha256') != actual_hash:
    raise SystemExit(1)
if not os.path.exists(out_ok) or os.path.exists(out_ok + '.tmp'):
    raise SystemExit(1)
if failure.returncode == 0 or os.path.exists(out_fail) or os.path.exists(out_fail + '.tmp'):
    raise SystemExit(1)
os.remove(bin_path)
server.shutdown()
