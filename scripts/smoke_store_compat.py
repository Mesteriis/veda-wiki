#!/usr/bin/env python3
"""Check the store format boundary between the deployed binary and this code.

Usage (from the repository root):
    python3 scripts/smoke_store_compat.py [--old-binary PATH]

Format 21 is deliberately unreadable for the deployed binary (fc35bea). The
script checks, on private temporary copies of internal/store/testdata/fc35bea:

1. the current binary refuses an unconverted copy and names migrate-revisions;
   the current `veda migrate-revisions` converts a copy and prints only
   counts, and `veda migrate-revisions --verify` passes;
2. the env-gated Go test TestWriteForPreviousBinary appends a revision through
   store.Update;
3. the previous binary refuses to start on that copy: it exits with an error,
   /health never answers and the store files are unchanged;
4. the current binary starts on that copy and the owner API returns the new
   revision;
5. separately, the previous binary still starts on an unconverted copy.

Each binary creates its own owner key inside its temporary copy.

Without --old-binary it builds commit fc35bea with `go build` in a temporary
git worktree outside the repository, embedding the current web bundle
(internal/webui/dist, built by `make frontend`) instead of running npm; only
the store and API are exercised. The worktree is removed afterwards. Only
synthetic fixture data is used; nothing is written to the repository.
"""
import argparse
import hashlib
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
COUNTS = ('articles: 1\nkept revisions: 2\nremoved revisions: 0\n'
          'blob cleanup paused until state.before-revisions.* is removed\n')


def build_previous(tmp):
    tree = os.path.join(tmp, 'veda-' + DEPLOYED)
    dist = ROOT / 'internal' / 'webui' / 'dist'
    if not (dist / 'index.html').is_file():
        raise SystemExit('run `make frontend` first: internal/webui/dist is missing')
    subprocess.run(['git', '-C', str(ROOT), 'worktree', 'add', '--detach', tree, DEPLOYED], check=True, stdout=subprocess.DEVNULL)
    try:
        shutil.copytree(dist, os.path.join(tree, 'server', 'ui'), dirs_exist_ok=True)
        binary = os.path.join(tmp, 'veda-previous')
        subprocess.run(['go', 'build', '-trimpath', '-o', binary, '.'], cwd=os.path.join(tree, 'server'), check=True, stdout=subprocess.DEVNULL)
        return binary
    finally:
        subprocess.run(['git', '-C', str(ROOT), 'worktree', 'remove', '--force', tree], check=True)


def build_current(tmp):
    binary = os.path.join(tmp, 'veda-current')
    subprocess.run(['go', 'build', '-trimpath', '-o', binary, './cmd/veda'], cwd=ROOT, check=True, stdout=subprocess.DEVNULL)
    return binary


def fixture_copy(tmp, name):
    data = os.path.join(tmp, name)
    os.mkdir(data, 0o700)
    for entry in ('state.json', 'state.sqlite'):
        shutil.copyfile(FIXTURE / entry, os.path.join(data, entry))
        os.chmod(os.path.join(data, entry), 0o600)
    return data


def digests(data):
    return {name: hashlib.sha256(Path(data, name).read_bytes()).hexdigest()
            for name in ('state.json', 'state.sqlite')}


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


def start(binary, data):
    """Start binary on data; return (process, base URL, /health status or None)."""
    port, public = free_port(), free_port()
    base = 'http://127.0.0.1:%d' % port
    proc = subprocess.Popen([binary, '--data', data, '--listen', '127.0.0.1:%d' % port, '--public-listen', '127.0.0.1:%d' % public],
                            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    for _ in range(100):
        if proc.poll() is not None:
            return proc, base, None
        try:
            status, _ = get(base + '/health')
            return proc, base, status
        except OSError:
            time.sleep(0.1)
    return proc, base, None


def stop(proc):
    if proc.poll() is None:
        proc.terminate()
        proc.wait(timeout=10)
    proc.stderr.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--old-binary', help='prebuilt binary of commit ' + DEPLOYED)
    args = parser.parse_args()
    result = {'previous': DEPLOYED}
    with tempfile.TemporaryDirectory(prefix='veda-store-compat-') as tmp:
        old = args.old_binary or build_previous(tmp)
        current = build_current(tmp)

        refused = subprocess.run([current, '--data', fixture_copy(tmp, 'refused'), '--listen', '127.0.0.1:%d' % free_port(), '--public-listen', '127.0.0.1:%d' % free_port()],
                                 capture_output=True, text=True, timeout=30)
        if refused.returncode == 0 or 'veda migrate-revisions' not in refused.stderr:
            raise SystemExit('current binary did not refuse an unconverted copy')
        result['current_on_unconverted'] = 'refused, exit %d' % refused.returncode

        data = fixture_copy(tmp, 'migrated')
        out = subprocess.run([current, 'migrate-revisions', '--data', data], capture_output=True, text=True, check=True).stdout
        if out != COUNTS or data in out:
            raise SystemExit('migrate-revisions printed unexpected output')
        result['migrate'] = 'counts only'
        verified = subprocess.run([current, 'migrate-revisions', '--verify', '--data', data], capture_output=True, text=True, check=True).stdout
        if not verified.endswith('verify: OK\n'):
            raise SystemExit('migrate-revisions --verify did not pass')
        result['verify'] = 'OK'
        env = dict(os.environ, VEDA_STORE_COMPAT_DIR=data)
        subprocess.run(['go', 'test', './internal/store', '-run', '^TestWriteForPreviousBinary$', '-count=1'], cwd=ROOT, env=env, check=True, stdout=subprocess.DEVNULL)

        before = digests(data)
        proc, _, status = start(old, data)
        try:
            exited = proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            exited = None
        finally:
            stop(proc)
        if status is not None or exited in (None, 0):
            raise SystemExit('previous binary did not refuse the converted store: /health %s, exit %s' % (status, exited))
        if digests(data) != before:
            raise SystemExit('previous binary changed the converted store')
        result['previous_on_v21'] = 'refused, exit %d' % exited

        proc, base, status = start(current, data)
        try:
            if status != 200:
                raise SystemExit('current binary /health returned %s' % status)
            owner = Path(data, 'owner.key').read_text().strip()
            status, body = get(base + '/api/v1/articles?source_id=fixture&path=guide.md&history=1', owner)
            article = json.loads(body) if status == 200 else {}
            texts = [v.get('text') for v in article.get('versions', [])]
            if status != 200 or len(texts) != 3 or texts[-1] != EXPECTED:
                raise SystemExit('current binary did not serve the new revision: status %s, %d versions' % (status, len(texts)))
        finally:
            stop(proc)
        result['current_on_v21'] = {'health': 200, 'versions': 3}

        proc, _, status = start(old, fixture_copy(tmp, 'unconverted'))
        try:
            if status != 200:
                raise SystemExit('previous binary did not start on an unconverted copy: /health %s' % status)
        finally:
            stop(proc)
        result['previous_on_unconverted'] = {'health': 200}
    result['status'] = 'passed'
    print(json.dumps(result))


if __name__ == '__main__':
    main()
