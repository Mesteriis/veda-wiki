#!/usr/bin/env python3
"""Exercise browser reading and search with a disposable synthetic VEDA store."""
import json
import os
from pathlib import Path
import socket
import signal
import subprocess
import tempfile
import time
import urllib.request
from datetime import datetime, timedelta, timezone

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

            def post(route, obj, method='POST'):
                request = urllib.request.Request(
                    f'http://127.0.0.1:{private}' + route, data=json.dumps(obj).encode(),
                    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + 'd' * 64},
                    method=method,
                )
                with opener.open(request, timeout=5) as response:
                    return json.load(response)

            post('/api/v1/sources', {'id': 'fixture', 'title': 'Тестовый источник',
                                      'kind': 'wiki', 'visibility': 'public'})
            post('/api/v1/sources', {'id': 'mirror', 'title': 'Второй источник',
                                      'kind': 'wiki', 'visibility': 'public'})
            post('/api/v1/articles', {'source_id': 'mirror', 'path': 'projects/architecture.md',
                 'text': '# Архитектура второго источника\n\nДемонстрационное содержимое для проверки интерфейса.',
                 'expected_revision': 0})
            for path, title in [('projects/architecture.md', 'Тестовая архитектура'),
                                ('infra/setup.md', 'Настройка примера'),
                                ('guides/intro.md', 'Начало работы'),
                                ('notes/ideas.md', 'Тестовые заметки')]:
                post('/api/v1/articles', {'source_id': 'fixture', 'path': path,
                     'text': '# ' + title + '\n\nДемонстрационное содержимое для проверки интерфейса.' + ('\n\n[Документация проекта](./)' if path == 'projects/architecture.md' else ''),
                     'expected_revision': 0})
            post('/api/v1/articles', {'source_id': 'fixture', 'path': 'projects/README.md',
                 'text': '# Руководство проекта\n\nСинтетическая страница раздела.', 'expected_revision': 0})
            post('/api/v1/sources', {'id': 'private-fixture', 'title': 'Приватный пример',
                                      'kind': 'wiki', 'visibility': 'private'})
            post('/api/v1/articles', {'source_id': 'private-fixture', 'path': 'private.md',
                 'text': '# Приватная тестовая страница\n\nЗакрытый синтетический текст.',
                 'expected_revision': 0})
            batch = {'source_id': 'conflict-fixture', 'kind': 'wiki', 'metadata': {'schema_version': 1, 'kind': 'wiki', 'title': 'Конфликтный пример'},
                     'files': {'guide.md': '# Исходный пример'}, 'commit': 'a' * 40, 'complete': True,
                     'create': True, 'visibility': 'private', 'expected_import': ''}
            imported = post('/api/v1/import', batch)
            post('/api/v1/articles', {'source_id': 'conflict-fixture', 'path': 'guide.md', 'text': '# Локальный пример', 'expected_revision': 1}, method='PUT')
            batch.update({'create': False, 'expected_import': imported['generation'], 'commit': 'b' * 40, 'files': {'guide.md': '# Изменение источника'}})
            if post('/api/v1/import', batch)['conflicts'] != 1:
                raise RuntimeError('synthetic conflict missing')
            post('/api/v1/sources', {'id': 'other-private', 'title': 'Недоступный пример',
                                      'kind': 'wiki', 'visibility': 'private'})
            post('/api/v1/articles', {'source_id': 'other-private', 'path': 'secret.md',
                 'text': '# Недоступная тестовая страница\n\nСинтетический текст.',
                 'expected_revision': 0})
            post('/api/v1/teams', {'id': 'browser-team', 'sources': ['fixture', 'private-fixture']})
            post('/api/v1/users', {'id': 'browser-user', 'teams': ['browser-team'],
                                   'password': 'synthetic fixture password'})
            key = post('/api/v1/access-keys', {'id': 'browser-fixture',
                       'sources': ['fixture', 'private-fixture', 'conflict-fixture'],
                       'expires_at': (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat()})['key']
            key_file = root / 'browser.key'
            key_file.write_text(key)
            key_file.chmod(0o600)
            debug = port()
            browser_log = (root / 'browser.log').open('w+b')
            browser = subprocess.Popen(
                [chrome, '--headless=new', '--disable-gpu', '--disable-background-networking',
                 '--disable-extensions', '--no-first-run', '--no-proxy-server',
                 f'--user-data-dir={root / "chrome"}', f'--remote-debugging-port={debug}', 'about:blank'],
                stdout=subprocess.DEVNULL, stderr=browser_log, start_new_session=True,
            )
            try:
                subprocess.run(['node', str(ROOT / 'scripts/smoke_ui.mjs'), str(debug),
                                f'http://127.0.0.1:{public}/', f'http://127.0.0.1:{private}/', str(key_file)],
                               check=True, timeout=45)
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
                browser_log.flush()
                browser_log.seek(0)
                log = browser_log.read().lower()
                print(json.dumps({'browser_exit': browser.poll(), 'sandbox_error': b'sandbox' in log,
                                  'missing_library': b'shared libraries' in log,
                                  'devtools_listening': b'devtools listening' in log}), flush=True)
                raise
            finally:
                try:
                    browser.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    try:
                        os.killpg(browser.pid, signal.SIGTERM)
                    except ProcessLookupError:
                        pass
                    try:
                        browser.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        os.killpg(browser.pid, signal.SIGKILL)
                        browser.wait(timeout=5)
                # Some Chrome children may still be closing after the parent exits.
                for _ in range(50):
                    try:
                        os.killpg(browser.pid, 0)
                    except ProcessLookupError:
                        break
                    time.sleep(.1)
                else:
                    try:
                        os.killpg(browser.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                browser_log.close()
        finally:
            server.terminate()
            server.wait(timeout=10)


if __name__ == '__main__':
    main()
