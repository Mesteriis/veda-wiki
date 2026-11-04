#!/usr/bin/env python3
"""Measure `veda migrate-revisions` on a synthetic format 20 store.

Usage (from the repository root): python3 scripts/bench_migrate_revisions.py

The current binary cannot create a store below format 21, so the env-gated Go
test TestBenchBuildLegacyStore writes one in the legacy layout: 550 articles
of 10 revisions each and 150 PNG images (120 listed by the source, 100 used by
some revision text). Everything lives in a temporary directory that is removed
afterwards. The output is numbers only.
"""
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def run(cmd, **kw):
    start = time.monotonic()
    out = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True, **kw)
    return time.monotonic() - start, out


def blobs(data):
    return len(list(Path(data, 'blobs').iterdir()))


def size(data):
    return Path(data, 'state.sqlite').stat().st_size


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def start_time(binary, data):
    port, public = free_port(), free_port()
    start = time.monotonic()
    proc = subprocess.Popen([binary, '--data', data, '--listen', '127.0.0.1:%d' % port, '--public-listen', '127.0.0.1:%d' % public],
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        while time.monotonic() - start < 30:
            try:
                with urllib.request.urlopen('http://127.0.0.1:%d/health' % port, timeout=1) as r:
                    if r.status == 200:
                        return time.monotonic() - start
            except OSError:
                time.sleep(0.01)
        raise SystemExit('service did not become healthy')
    finally:
        proc.terminate()
        proc.wait(timeout=10)


def main():
    with tempfile.TemporaryDirectory(prefix='veda-bench-') as tmp:
        binary = os.path.join(tmp, 'veda')
        subprocess.run(['go', 'build', '-trimpath', '-o', binary, './cmd/veda'], cwd=ROOT, check=True)
        data = os.path.join(tmp, 'data')
        subprocess.run(['go', 'test', './internal/store', '-run', '^TestBenchBuildLegacyStore$', '-count=1'],
                       cwd=ROOT, env=dict(os.environ, VEDA_BENCH_BUILD=data), check=True, stdout=subprocess.DEVNULL)
        before_size, before_blobs = size(data), blobs(data)
        took, out = run([binary, 'migrate-revisions', '--data', data])
        if out.returncode:
            raise SystemExit('migrate-revisions failed, exit %d' % out.returncode)
        counts = ', '.join(out.stdout.splitlines()[:3])
        verify_took, out = run([binary, 'migrate-revisions', '--verify', '--data', data])
        if out.returncode or not out.stdout.endswith('verify: OK\n'):
            raise SystemExit('verify failed')
        after_size, after_blobs = size(data), blobs(data)

        # Collection runs on a copy without the backup (it pauses while one
        # exists) with the blobs aged past the 24 h grace period.
        copy = os.path.join(tmp, 'collect')
        shutil.copytree(data, copy)
        for p in Path(copy).glob('state.before-revisions.*'):
            p.unlink()
        old = time.time() - 48 * 3600
        for p in Path(copy, 'blobs').iterdir():
            os.utime(p, (old, old))
        start_ms = start_time(binary, data) * 1000
        _, out = run(['go', 'test', './internal/store', '-run', '^TestBenchCollectBlobs$', '-count=1', '-v'],
                     env=dict(os.environ, VEDA_BENCH_COLLECT=copy))
        collect = [l for l in out.stdout.splitlines() if l.startswith('collect_')]
        if out.returncode or not collect:
            raise SystemExit('collection measurement failed')
        print('articles=550 revisions_per_article=10 images=150')
        print('migrate_s=%.2f verify_s=%.2f' % (took, verify_took))
        print('migrate_counts: ' + counts)
        print('state_sqlite_bytes before=%d after=%d' % (before_size, after_size))
        print('blobs before=%d after=%d' % (before_blobs, after_blobs))
        print('service_start_to_health_ms=%.0f' % start_ms)
        print('\n'.join(collect))


if __name__ == '__main__':
    sys.exit(main())
