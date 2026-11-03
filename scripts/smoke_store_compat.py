#!/usr/bin/env python3
"""Check that the deployed binary reads a store written by the current code.

Usage (from the repository root):
    python3 scripts/smoke_store_compat.py [--old-binary PATH]

The script copies internal/store/testdata/fc35bea into a private temporary
directory, appends a revision through store.Update (the env-gated Go test
TestWriteForPreviousBinary), then starts the previous binary on that copy and
expects /health to return 200 and the owner API to return the new revision.
The previous binary creates its own owner key inside the temporary copy.

Without --old-binary it builds commit fc35bea with `make build` in a temporary
git worktree outside the repository and removes the worktree afterwards.
Only synthetic fixture data is used; nothing is written to the repository.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
FIXTURE = ROOT / 'internal' / 'store' / 'testdata' / 'fc35bea'
DEPLOYED = 'fc35bea'
EXPECTED = '# Synthetic guide\n\nThird revision from the new store.'


def build_previous(tmp):
    tree = os.path.join(tmp, 'veda-' + DEPLOYED)
    subprocess.run(['git', '-C', str(ROOT), 'worktree', 'add', '--detach', tree, DEPLOYED], check=True, stdout=subprocess.DEVNULL)
    try:
        subprocess.run(['make', '-C', tree, 'build'], check=True, stdout=subprocess.DEVNULL)
        binary = os.path.join(tmp, 'veda-previous')
        shutil.copy2(os.path.join(tree, 'veda'), binary)
        return binary
    finally:
        subprocess.run(['git', '-C', str(ROOT), 'worktree', 'remove', '--force', tree], check=True)


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def get(url, token=None):
    request = urllib.request.Request(url, headers={'Authorization': 'Bearer ' + token} if token else {})
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--old-binary', help='prebuilt binary of commit ' + DEPLOYED)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='veda-store-compat-') as tmp:
        old = args.old_binary or build_previous(tmp)
        data = os.path.join(tmp, 'data')
        os.mkdir(data, 0o700)
        for name in ('state.json', 'state.sqlite'):
            shutil.copyfile(FIXTURE / name, os.path.join(data, name))
            os.chmod(os.path.join(data, name), 0o600)
        env = dict(os.environ, VEDA_STORE_COMPAT_DIR=data)
        subprocess.run(['go', 'test', './internal/store', '-run', '^TestWriteForPreviousBinary$', '-count=1'], cwd=ROOT, env=env, check=True, stdout=subprocess.DEVNULL)
        port, public = free_port(), free_port()
        base = 'http://127.0.0.1:%d' % port
        proc = subprocess.Popen([old, '--data', data, '--listen', '127.0.0.1:%d' % port, '--public-listen', '127.0.0.1:%d' % public], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        try:
            status = None
            for _ in range(100):
                if proc.poll() is not None:
                    raise SystemExit('previous binary exited: ' + proc.stderr.read().decode(errors='replace')[-500:])
                try:
                    status, _ = get(base + '/health')
                    break
                except OSError:
                    time.sleep(0.1)
            if status != 200:
                raise SystemExit('previous binary /health returned %s' % status)
            owner = Path(data, 'owner.key').read_text().strip()
            status, body = get(base + '/api/v1/articles?source_id=fixture&path=guide.md&history=1', owner)
            article = json.loads(body) if status == 200 else {}
            texts = [v.get('text') for v in article.get('versions', [])]
            if status != 200 or len(texts) != 3 or texts[-1] != EXPECTED:
                raise SystemExit('previous binary did not read the new revision: status %s, %d versions' % (status, len(texts)))
        finally:
            proc.terminate()
            proc.wait(timeout=10)
    print(json.dumps({'status': 'passed', 'previous': DEPLOYED, 'health': 200, 'versions': 3}))


if __name__ == '__main__':
    main()
