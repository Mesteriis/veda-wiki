#!/usr/bin/env python3
"""Opt-in read-only browser import of one explicitly selected public program repo."""
import json
import os
from pathlib import Path
import re
import signal
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
    if os.environ.get('VEDA_TEST_GITHUB_BROWSER') != '1':
        print(json.dumps({'status': 'skipped', 'reason': 'opt-in required'}))
        return
    chrome = os.environ.get('VEDA_TEST_CHROME', '')
    repo = os.environ.get('VEDA_TEST_PUBLIC_PROGRAM_REPO', '')
    if (not Path(chrome).is_absolute() or not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repo)
            or any(part in ('.', '..') for part in repo.split('/'))):
        raise SystemExit('Set an absolute VEDA_TEST_CHROME and an owner/repository VEDA_TEST_PUBLIC_PROGRAM_REPO')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    headers = {'User-Agent': 'VEDA-browser-acceptance'}
    with opener.open(urllib.request.Request('https://api.github.com/rate_limit', headers=headers), timeout=10) as response:
        remaining = json.load(response)['resources']['core']['remaining']
    if remaining < 20:
        print(json.dumps({'status': 'skipped', 'reason': 'low anonymous quota'}))
        return
    with opener.open(urllib.request.Request('https://api.github.com/repos/' + repo, headers=headers), timeout=10) as response:
        metadata = json.load(response)
    if metadata.get('private') is not False or metadata.get('visibility') != 'public':
        raise SystemExit('repository is not verified public')
    with tempfile.TemporaryDirectory(prefix='veda-github-browser-') as tmp:
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
            for _ in range(100):
                if server.poll() is not None:
                    raise RuntimeError('fixture exited')
                try:
                    with opener.open(f'http://127.0.0.1:{private}/health', timeout=1):
                        break
                except OSError:
                    time.sleep(.05)
            else:
                raise RuntimeError('fixture unavailable')
            debug = port()
            browser = subprocess.Popen(
                [chrome, '--headless=new', '--disable-gpu', '--disable-background-networking',
                 '--disable-extensions', '--no-first-run', '--no-proxy-server',
                 f'--user-data-dir={root / "chrome"}', f'--remote-debugging-port={debug}', 'about:blank'],
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True,
            )
            try:
                subprocess.run(['node', str(ROOT / 'scripts/smoke_public_github_browser.mjs'), str(debug),
                                f'http://127.0.0.1:{private}/', repo], check=True, timeout=150)
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
        finally:
            server.terminate()
            server.wait(timeout=10)


if __name__ == '__main__':
    main()
