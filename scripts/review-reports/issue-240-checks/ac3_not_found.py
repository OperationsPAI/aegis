import json
import os
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

PROJECT_NAME = 'pair_diagnosis'
PROJECT_ID = 7
NAMES = [
    'otel-demo23-recommendation-pod-failure-4t2mpb',
    'otel-demo23-recommendation-pod-failure-4t2mpa',
    'otel-demo23-recommendation-pod-failure-4t2mqa',
    'otel-demo23-checkout-pod-failure-4t2mpb',
]
QUERY = 'otel-demo23-recommendation-pod-failure-4t2zzz'

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
            return self._json({'code': 200, 'message': 'ok', 'data': {'items': [{'id': 700 + i, 'name': name} for i, name in enumerate(NAMES)], 'pagination': {'page': 1, 'size': 100, 'total': len(NAMES), 'total_pages': 1}}})
        return self._json({'code': 404, 'message': 'not found'}, 404)


server = HTTPServer(('127.0.0.1', 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
base = f'http://127.0.0.1:{server.server_port}'
bin_path = tempfile.mktemp(prefix='aegisctl-')
subprocess.run(['go', 'build', '-o', bin_path, './cmd/aegisctl'], cwd='AegisLab/src', check=True, capture_output=True, text=True)
res = subprocess.run([bin_path, '--server', base, '--token', 'token', '--project', PROJECT_NAME, 'inject', 'get', QUERY, '--output', 'json'], cwd='AegisLab/src', capture_output=True, text=True)
print(json.dumps({'code': res.returncode, 'stdout': res.stdout.strip(), 'stderr': res.stderr.strip()}, ensure_ascii=False, indent=2))
if res.returncode != 7:
    raise SystemExit(1)
line = res.stderr.strip()
if line.startswith('Error: '):
    line = line[len('Error: '):]
payload = json.loads(line)
if payload.get('type') != 'not_found' or payload.get('query') != QUERY:
    raise SystemExit(1)
if len(payload.get('suggestions', [])) != 3:
    raise SystemExit(1)
os.remove(bin_path)
server.shutdown()
