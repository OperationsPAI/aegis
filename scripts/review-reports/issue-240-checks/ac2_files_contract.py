import json
import os
import subprocess
import tempfile
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
        if path == f'/api/v2/injections/{INJECTION_ID}/files':
            return self._json({'code': 200, 'message': 'ok', 'data': {'files': [{'name': 'demo.log', 'path': 'raw/demo.log', 'size': '10 B'}], 'file_count': 1, 'dir_count': 0}})
        if path == f'/api/v2/injections/{INJECTION_ID}/download':
            body = b'zip-bytes'
            self.send_response(200)
            self.send_header('Content-Type', 'application/zip')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        return self._json({'code': 404, 'message': 'not found'}, 404)


server = HTTPServer(('127.0.0.1', 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
base = f'http://127.0.0.1:{server.server_port}'
common = ['go', 'run', './cmd/aegisctl', '--server', base, '--token', 'token', '--project', PROJECT_NAME]
files = subprocess.run(common + ['inject', 'files', INJECTION_NAME, '--output', 'json'], cwd='AegisLab/src', capture_output=True, text=True)
out = tempfile.NamedTemporaryFile(delete=False)
out.close()
os.unlink(out.name)
download = subprocess.run(common + ['inject', 'download', str(INJECTION_ID), '--output-file', out.name], cwd='AegisLab/src', capture_output=True, text=True)
print(json.dumps({'files': {'code': files.returncode, 'stdout': files.stdout.strip(), 'stderr': files.stderr.strip()}, 'download_by_id': {'code': download.returncode, 'stdout': download.stdout.strip(), 'stderr': download.stderr.strip(), 'exists': os.path.exists(out.name)}}, ensure_ascii=False, indent=2))
server.shutdown()
raise SystemExit(0 if files.returncode == 0 and download.returncode == 0 else 1)
