"""Exercise v16 directory transfers and a stopped SQLite restore with fixtures."""
import argparse
import json
from pathlib import Path
import shutil
import socket
import sqlite3
import subprocess
import tempfile
import time
from urllib.parse import urlencode
from urllib.request import ProxyHandler, Request, build_opener

ROOT = Path(__file__).resolve().parents[1]
TOKEN = 'd' * 64  # Synthetic fixture only.


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--index', action='store_true')
    use_index = parser.parse_args().index
    opener = build_opener(ProxyHandler({}))
    with tempfile.TemporaryDirectory(prefix='veda-directory-restore-') as tmp:
        root = Path(tmp)
        root.chmod(0o700)
        data = root / 'data'
        data.mkdir(mode=0o700)
        for name, value in [('owner.key', TOKEN), ('reader.key', 'e' * 64)]:
            f = data / name
            f.write_text(value)
            f.chmod(0o600)
        private, public = port(), port()
        while public == private:
            public = port()

        def start(directory):
            proc = subprocess.Popen([str(ROOT / 'veda'), '--data', str(directory),
                '--listen', f'127.0.0.1:{private}', '--public-listen', f'127.0.0.1:{public}'],
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            for _ in range(100):
                if proc.poll() is not None:
                    raise RuntimeError('fixture server exited')
                try:
                    with opener.open(f'http://127.0.0.1:{private}/health', timeout=1):
                        return proc
                except OSError:
                    time.sleep(.05)
            proc.terminate()
            proc.wait(timeout=10)
            raise RuntimeError('fixture server unavailable')

        def call(route, body=None):
            req = Request(f'http://127.0.0.1:{private}' + route,
                data=None if body is None else json.dumps(body).encode(),
                headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + TOKEN})
            with opener.open(req, timeout=5) as resp:
                return json.load(resp)

        proc = start(data)
        try:
            for source in ('from', 'to'):
                call('/api/v1/sources', {'id': source, 'title': source, 'kind': 'wiki', 'visibility': 'private'})
            source_path = 'docs/index.md' if use_index else 'docs/README.md'
            for path, value in [('guide.md', '# Guide\n\n[Docs](docs/)'), (source_path, '# Documentation')]:
                call('/api/v1/articles', {'source_id': 'from', 'path': path, 'text': value, 'expected_revision': 0})
            call('/api/v1/articles/copy', {'source_id': 'from', 'source_path': 'guide.md', 'expected_revision': 1,
                 'destination_id': 'to', 'destination_path': 'guide.md', 'include_linked': True})
        finally:
            proc.terminate()
            if proc.wait(timeout=10) != 0:
                raise RuntimeError('fixture server did not stop cleanly')

        subprocess.run([str(ROOT / 'veda'), 'migrate-sqlite', '--data', str(data)],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        restored = root / 'restored'
        shutil.copytree(data, restored)
        db = sqlite3.connect(f'file:{restored / "state.sqlite"}?mode=ro', uri=True)
        try:
            row = db.execute("SELECT data FROM records WHERE category='meta' AND id='state'").fetchone()
            if row is None or json.loads(row[0])['version'] != (17 if use_index else 16) or db.execute('PRAGMA integrity_check').fetchone()[0] != 'ok':
                raise RuntimeError('restored database version or integrity mismatch')
        finally:
            db.close()
        proc = start(restored)
        try:
            for source in ('from', 'to'):
                documentation_path = source_path if source == 'from' else 'docs/README.md'
                for name, expected in [('guide.md', '# Guide\n\n[Docs](docs/)'), (documentation_path, '# Documentation')]:
                    article = call('/api/v1/articles?' + urlencode({'source_id': source, 'path': name}))
                    if article['versions'][-1]['text'] != expected:
                        raise RuntimeError('restored article differs')
            page = call('/api/state?' + urlencode({'page': 'to/docs'}))
            if page['view']['Page']['path'] != 'to/docs/README.md':
                raise RuntimeError('restored directory link not resolved')
        finally:
            proc.terminate()
            if proc.wait(timeout=10) != 0:
                raise RuntimeError('restored fixture did not stop cleanly')
        print('PASS: actual binary, synthetic directory copy, SQLite migration and stopped-store restore')


if __name__ == '__main__':
    main()
