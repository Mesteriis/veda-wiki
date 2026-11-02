"""Exercise authenticated translation through the actual binary using fixtures only."""
import argparse
import json
import socket
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlencode
from urllib.request import ProxyHandler, Request, build_opener

ROOT = Path(__file__).resolve().parents[1]


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--protocol', choices=('ollama', 'openai'), default='ollama')
    protocol = parser.parse_args().protocol
    expected = ['Bearer synthetic-first']
    received = []

    class Model(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_POST(self):
            path = '/api/generate' if protocol == 'ollama' else '/v1/chat/completions'
            if self.path != path or self.headers.get('Authorization') != expected[0]:
                self.send_error(403)
                return
            size = int(self.headers.get('Content-Length', '0'))
            if size > 4096:
                self.send_error(413)
                return
            data = json.loads(self.rfile.read(size))
            if protocol == 'openai':
                messages = data.get('messages', [])
                if data.get('model') != 'synthetic' or data.get('stream') is not False or len(messages) != 2 or messages[0].get('role') != 'system' or messages[1].get('role') != 'user' or 'chat_template_kwargs' in data:
                    self.send_error(400)
                    return
                prompt = messages[1].get('content')
            else:
                prompt = data.get('prompt')
            translated = {'# First': '# Первый', '# Second': '# Второй'}.get(prompt)
            if translated is None:
                self.send_error(400)
                return
            received.append(prompt)
            result = {'done': True, 'response': translated} if protocol == 'ollama' else {'choices': [{'finish_reason': 'stop', 'message': {'content': translated}}]}
            payload = json.dumps(result).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

    model = ThreadingHTTPServer(('127.0.0.1', 0), Model)
    thread = threading.Thread(target=model.serve_forever, daemon=True)
    thread.start()
    with tempfile.TemporaryDirectory(prefix='veda-translation-auth-') as tmp:
        root = Path(tmp)
        root.chmod(0o700)
        for name, value in [('owner.key', 'd' * 64), ('reader.key', 'e' * 64), ('model-token', 'synthetic-first')]:
            path = root / name
            path.write_text(value)
            path.chmod(0o600)
        private, public = port(), port()
        proc = subprocess.Popen([str(ROOT / 'veda'), '--data', tmp, '--listen', f'127.0.0.1:{private}',
            '--public-listen', f'127.0.0.1:{public}', '--translation-local-url', f'http://127.0.0.1:{model.server_port}',
            '--translation-model', 'synthetic', '--translation-protocol', protocol, '--translation-token-file', str(root / 'model-token'),
            '--translation-interval', '5s'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        opener = build_opener(ProxyHandler({}))

        def api(route, body=None):
            data = None if body is None else json.dumps(body).encode()
            req = Request(f'http://127.0.0.1:{private}' + route, data=data,
                          headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + 'd' * 64})
            with opener.open(req, timeout=5) as response:
                return json.load(response)

        def wait_translation(path, expected_text):
            deadline = time.monotonic() + 20
            route = '/api/v1/articles?' + urlencode({'source_id': 'fixture', 'path': path})
            while time.monotonic() < deadline:
                article = api(route)
                translation = article.get('translation') or {}
                if translation.get('text') == expected_text and translation.get('origin') == 'local_model':
                    return
                if proc.poll() is not None:
                    raise RuntimeError('VEDA stopped before translation')
                time.sleep(.2)
            raise RuntimeError('translation was not saved')

        try:
            for _ in range(100):
                if proc.poll() is not None:
                    raise RuntimeError('VEDA exited')
                try:
                    api('/health')
                    break
                except OSError:
                    time.sleep(.05)
            else:
                raise RuntimeError('VEDA not ready')
            api('/api/v1/sources', {'id': 'fixture', 'title': 'Fixture', 'kind': 'program', 'visibility': 'private'})
            api('/api/v1/articles', {'source_id': 'fixture', 'path': 'docs/one.md', 'text': '# First', 'expected_revision': 0})
            wait_translation('docs/one.md', '# Первый')
            (root / 'model-token').write_text('synthetic-second')
            expected[0] = 'Bearer synthetic-second'
            api('/api/v1/articles', {'source_id': 'fixture', 'path': 'docs/two.md', 'text': '# Second', 'expected_revision': 0})
            wait_translation('docs/two.md', '# Второй')
            if received != ['# First', '# Second']:
                raise RuntimeError('unexpected model calls')
            proc.terminate()
            if proc.wait(timeout=15) != 0:
                raise RuntimeError('VEDA did not stop cleanly')
            proc = subprocess.Popen([str(ROOT / 'veda'), '--data', tmp, '--listen', f'127.0.0.1:{private}',
                                     '--public-listen', f'127.0.0.1:{public}'], stdout=subprocess.DEVNULL,
                                    stderr=subprocess.DEVNULL)
            for _ in range(100):
                if proc.poll() is not None:
                    raise RuntimeError('VEDA exited after restart')
                try:
                    api('/health')
                    break
                except OSError:
                    time.sleep(.05)
            else:
                raise RuntimeError('VEDA not ready after restart')
            wait_translation('docs/one.md', '# Первый')
            wait_translation('docs/two.md', '# Второй')
            print(f'PASS: actual binary, {protocol} local fixture, bearer rotation and restart')
        finally:
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=15)
            model.shutdown()
            model.server_close()
            thread.join(timeout=5)


if __name__ == '__main__':
    main()
