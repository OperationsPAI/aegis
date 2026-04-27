import json
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

PROJECT_NAME = 'pair_diagnosis'
PROJECT_ID = 7
INJECTION_ID = 744
INJECTION_NAME = 'otel-demo23-recommendation-pod-failure-4t2mpb'

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
            return self._json({'code': 200, 'message': 'ok', 'data': {'items': [{'id': INJECTION_ID, 'name': INJECTION_NAME}], 'pagination': {'page': 1, 'size': 100, 'total': 1, 'total_pages': 1}}})
        if path == f'/api/v2/injections/{INJECTION_ID}':
            return self._json({'code': 200, 'message': 'ok', 'data': {'id': INJECTION_ID, 'name': INJECTION_NAME, 'state': 'build_success'}})
        return self._json({'code': 404, 'message': 'not found'}, 404)


server = HTTPServer(('127.0.0.1', 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
base = f'http://127.0.0.1:{server.server_port}'
cmd_base = ['go', 'run', './cmd/aegisctl', '--server', base, '--token', 'token', '--project', PROJECT_NAME, '--output', 'json']
by_name = subprocess.run(cmd_base + ['inject', 'get', INJECTION_NAME], cwd='AegisLab/src', capture_output=True, text=True)
by_id = subprocess.run(cmd_base + ['inject', 'get', str(INJECTION_ID)], cwd='AegisLab/src', capture_output=True, text=True)
print(json.dumps({'by_name': {'code': by_name.returncode, 'stdout': by_name.stdout.strip(), 'stderr': by_name.stderr.strip()}, 'by_id': {'code': by_id.returncode, 'stdout': by_id.stdout.strip(), 'stderr': by_id.stderr.strip()}}, ensure_ascii=False, indent=2))
for res in (by_name, by_id):
    if res.returncode != 0:
        raise SystemExit(1)
    payload = json.loads(res.stdout)
    if int(payload['id']) != INJECTION_ID or payload['name'] != INJECTION_NAME:
        raise SystemExit(1)
server.shutdown()
