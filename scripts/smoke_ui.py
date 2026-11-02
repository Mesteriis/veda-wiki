#!/usr/bin/env python3
"""Exercise browser reading and search with a disposable synthetic VEDA store."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def main():
    chrome = os.environ.get('VEDA_TEST_CHROME', '')
    if not chrome or not Path(chrome).is_absolute():
        raise SystemExit('Set VEDA_TEST_CHROME to an absolute Chrome or Chromium executable path')
    with tempfile.TemporaryDirectory(prefix='veda-ui-fixture-') as tmp:
        root = Path(tmp)
        data = root / 'data'
        data.mkdir(mode=0o700)
        for name, char in [('owner.key', 'd'), ('reader.key', 'e')]:
            key = data / name
            key.write_text(char * 64)
            key.chmod(0o600)
        private, public = port(), port()
        while private == public:
            public = port()
        server = subprocess.Popen(
            [str(ROOT / 'veda'), '--data', str(data), '--listen', f'127.0.0.1:{private}',
             '--public-listen', f'127.0.0.1:{public}'],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        try:
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            for _ in range(100):
                if server.poll() is not None:
                    raise RuntimeError('fixture exited')
                try:
                    with opener.open(f'http://127.0.0.1:{private}/health', timeout=1):
                        break
                except OSError:
                    time.sleep(.05)
            else:
                raise RuntimeError('fixture not ready')

            def post(route, obj):
                request = urllib.request.Request(
                    f'http://127.0.0.1:{private}' + route, data=json.dumps(obj).encode(),
                    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + 'd' * 64},
                )
                with opener.open(request, timeout=5) as response:
                    return json.load(response)

            post('/api/v1/sources', {'id': 'fixture', 'title': 'Тестовый источник',
                                      'kind': 'wiki', 'visibility': 'public'})
            for path, title in [('projects/architecture.md', 'Тестовая архитектура'),
                                ('infra/setup.md', 'Настройка примера'),
                                ('guides/intro.md', 'Начало работы'),
                                ('notes/ideas.md', 'Тестовые заметки')]:
                post('/api/v1/articles', {'source_id': 'fixture', 'path': path,
                     'text': '# ' + title + '\n\nДемонстрационное содержимое для проверки интерфейса.',
                     'expected_revision': 0})
            debug = port()
            browser = subprocess.Popen(
                [chrome, '--headless=new', '--disable-gpu', '--disable-background-networking',
                 '--disable-extensions', '--no-first-run', '--no-proxy-server',
                 f'--user-data-dir={root / "chrome"}', f'--remote-debugging-port={debug}', 'about:blank'],
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )
            try:
                subprocess.run(['node', str(ROOT / 'scripts/smoke_ui.mjs'), str(debug),
                                f'http://127.0.0.1:{public}/'], check=True, timeout=30)
            finally:
                browser.terminate()
                browser.wait(timeout=10)
        finally:
            server.terminate()
            server.wait(timeout=10)


if __name__ == '__main__':
    main()
