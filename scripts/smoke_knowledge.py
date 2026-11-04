#!/usr/bin/env python3
"""End-to-end knowledge graph smoke: real GLiNER2.5, real Neo4j, VEDA REST
and the official MCP SDK on synthetic articles, then an offline restart.

    <python with mcp> scripts/smoke_knowledge.py [--bulk N] [--keep-going]

Prerequisites: Docker, Go, the extractor image and the model cache volume of
the developer stack (python3 scripts/knowledge_stack.py up --extractor
--fetch-model once, with network). This run uses its own compose project
(veda-knowledge-smoke), its own secrets in a temporary directory and its own
Neo4j volumes, which it deletes at the end. It mounts the developer's model
cache volume read-only in effect (offline, no fetch) and keeps it.

Prints only counts, booleans and timings. Never prints secrets or text.
"""
import argparse
import asyncio
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parent))
import knowledge_stack as ks  # noqa: E402
import smoke_knowledge_fixture as fx  # noqa: E402
from smoke_knowledge_checks import Api, check_rest, check_mcp, latency  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
PROJECT = 'veda-knowledge-smoke'
MODEL_VOLUME = 'veda-knowledge_model-cache'
OWN_VOLUMES = [f'{PROJECT}_{name}' for name in ('neo4j-data', 'neo4j-logs', 'neo4j-secrets')]


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def report(name, value):
    print(f'{name}: {value}', flush=True)


def docker(*args, check=True):
    return subprocess.run(['docker', *args], check=check, text=True, capture_output=True)


def configure_stack(tmp):
    """Point knowledge_stack at a disposable project, secrets and ports."""
    secrets = tmp / 'secrets'
    ks.SECRETS = secrets
    ks.NEO4J_PASSWORD, ks.NEO4J_AUTH, ks.EXTRACTOR_KEY = secrets / 'neo4j_password', secrets / 'neo4j_auth', secrets / 'extractor.key'
    ks.NEO4J_PORT, ks.EXTRACTOR_PORT = free_port(), free_port()
    os.environ['KNOWLEDGE_NEO4J_PORT'], os.environ['KNOWLEDGE_EXTRACTOR_PORT'] = str(ks.NEO4J_PORT), str(ks.EXTRACTOR_PORT)
    override = tmp / 'compose.override.yaml'
    override.write_text(json.dumps({
        'secrets': {'neo4j_auth': {'file': str(ks.NEO4J_AUTH)}, 'extractor_key': {'file': str(ks.EXTRACTOR_KEY)}},
        'volumes': {'model-cache': {'external': True, 'name': MODEL_VOLUME}}}))
    ks.PROJECT, ks.OVERRIDES = PROJECT, (override,)
    os.environ.setdefault('COMPOSE_PROGRESS', 'quiet')
    os.environ.setdefault('BUILDKIT_PROGRESS', 'quiet')


def preflight():
    if docker('volume', 'inspect', MODEL_VOLUME, check=False).returncode != 0:
        raise SystemExit(f'missing {MODEL_VOLUME}: run python3 scripts/knowledge_stack.py up --extractor --fetch-model once')
    if docker('compose', '-p', PROJECT, 'ps', '-q', check=False).stdout.strip():
        raise SystemExit(f'compose project {PROJECT} is already running; stop it first')


def start_veda(binary, data, log, ports, knowledge):
    args = [str(binary), '--data', str(data), '--listen', f"127.0.0.1:{ports['private']}",
            '--public-listen', f"127.0.0.1:{ports['public']}", '--reader-socket', str(data / 'reader.sock')]
    if knowledge:
        args += ['--knowledge', '--knowledge-index=neo4j', f'--knowledge-neo4j-uri=bolt://127.0.0.1:{ks.NEO4J_PORT}',
                 f'--knowledge-neo4j-user={ks.NEO4J_USER}', f'--knowledge-neo4j-password-file={ks.NEO4J_PASSWORD}',
                 f'--knowledge-extractor-url=http://127.0.0.1:{ks.EXTRACTOR_PORT}',
                 f'--knowledge-extractor-key-file={ks.EXTRACTOR_KEY}', '--knowledge-reconcile-interval=30s']
    process = subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT)
    api = Api(ports, data)
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError('VEDA exited during start')
        if api.health():
            return process
        time.sleep(0.1)
    raise RuntimeError('VEDA did not become healthy')


def stop(process):
    if process and process.poll() is None:
        process.send_signal(signal.SIGTERM)
        try:
            process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)


def wait_indexed(api, expected, timeout):
    """Wait until the owner status reports every active snapshot succeeded."""
    started = time.monotonic()
    status = {}
    while time.monotonic() - started < timeout:
        _, status = api.owner('GET', '/api/v1/knowledge/status')
        jobs = status.get('jobs') or {}
        if status.get('state') == 'ready' and jobs.get('succeeded') == expected and not jobs.get('pending') and not jobs.get('running'):
            return time.monotonic() - started, status
        time.sleep(0.5)
    raise RuntimeError(f"indexing did not finish: state={status.get('state')} jobs={status.get('jobs')} reasons={status.get('reasons')}")


def container_memory():
    out = docker('stats', '--no-stream', '--format', '{{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}', check=False).stdout
    rows = {}
    for line in out.splitlines():
        name, mem, cpu = (line.split('\t') + ['', ''])[:3]
        if name.startswith(PROJECT + '-') and ('neo4j-1' in name or 'extractor-1' in name):
            rows[name.removeprefix(PROJECT + '-')] = f'{mem} (cpu {cpu})'
    return rows


def rss_mb(pid):
    out = subprocess.run(['ps', '-o', 'rss=', '-p', str(pid)], text=True, capture_output=True).stdout.strip()
    return round(int(out) / 1024, 1) if out else None


def offline_restart(api, expected):
    """Restart the extractor: internal network, HF offline, cached model."""
    started = time.monotonic()
    ks.compose('--profile', 'extractor', 'restart', 'extractor', capture=True)
    status, detail = ks.extractor_ready()
    while status != 200 and time.monotonic() - started < 300:
        time.sleep(1)
        status, detail = ks.extractor_ready()
    report('offline.extractor_ready_after_restart', status == 200)
    report('offline.ready_seconds', round(time.monotonic() - started, 1))
    probe = ('import os,socket\n'
             'def ok(f):\n  try:\n    f(); return True\n  except OSError:\n    return False\n'
             'print(os.environ.get("HF_HUB_OFFLINE"), ok(lambda: socket.getaddrinfo("huggingface.co", 443)),'
             ' ok(lambda: socket.create_connection(("1.1.1.1", 443), timeout=3).close()))')
    out = ks.compose('--profile', 'extractor', 'exec', '-T', 'extractor', 'python', '-c', probe, capture=True, check=False).stdout.split()
    report('offline.hf_hub_offline', out[:1] == ['1'])
    report('offline.dns_huggingface_resolves', out[1:2] == ['True'])
    report('offline.egress_1.1.1.1_443', out[2:3] == ['True'])
    code, op = api.owner('POST', '/api/v1/knowledge/reindex', {'scope': 'source', 'source_id': fx.PUBLIC, 'force': True})
    report('offline.force_reindex_status', code)
    report('offline.force_reindex_requeued', op.get('requeued'))
    seconds, _ = wait_indexed(api, expected, 600)
    report('offline.reextract_seconds', round(seconds, 1))
    _, article = api.owner('GET', f'/api/v1/knowledge/article?source_id={fx.PUBLIC}&path=billing.md')
    entities = {e['name'] for e in (article.get('knowledge') or {}).get('entities', [])}
    report('offline.billing_ready_after_reextract', article.get('status') == 'ready' and 'db-01' in entities)
    return status == 200 and out[:1] == ['1'] and out[1:3] == ['False', 'False'] and code == 202


def run(args, tmp):
    configure_stack(tmp)
    t0 = time.monotonic()
    if ks.cmd_up(SimpleNamespace(extractor=True, fetch_model=False, timeout=args.timeout)) != 0:
        raise RuntimeError('knowledge stack did not start')
    report('stack.up_seconds', round(time.monotonic() - t0, 1))
    binary = tmp / 'veda'
    subprocess.run(['go', 'build', '-trimpath', '-o', str(binary), './cmd/veda'], cwd=ROOT, check=True)
    data = tmp / 'data'
    ports = {'private': free_port(), 'public': free_port()}
    log = open(tmp / 'veda.log', 'ab')
    veda = start_veda(binary, data, log, ports, knowledge=False)
    api = Api(ports, data)
    try:
        fx.seed(api.owner_ok, args.bulk)
    finally:
        stop(veda)
    published_sha = fx.connect_github_copy(data)
    articles = fx.all_articles(args.bulk)
    expected = len(articles) + 1  # kg-gh/deploy.md: current and publication copy
    report('fixture.articles', len(articles))
    report('fixture.active_snapshots', expected)
    report('fixture.long_article_code_points', len(fx.long_article()))
    veda = start_veda(binary, data, log, ports, knowledge=True)
    try:
        seconds, status = wait_indexed(api, expected, args.timeout)
        report('index.seconds_all_snapshots', round(seconds, 1))
        report('index.state', status['state'])
        report('index.dropped', json.dumps(status.get('dropped'), sort_keys=True))
        for name, value in container_memory().items():
            report(f'resources.{name}', value)
        report('resources.veda_rss_mb', rss_mb(veda.pid))
        if args.example:
            # Synthetic text only: the owner's article answer for kg-public/billing.md.
            _, example = api.owner('GET', f'/api/v1/knowledge/article?source_id={fx.PUBLIC}&path=billing.md&limit=20')
            Path(args.example).write_text(json.dumps(example, ensure_ascii=False, indent=1))
        failures = check_rest(api, articles, published_sha, report)
        failures += asyncio.run(check_mcp(binary, api, report))
        for name, (p50, p95) in latency(api, args.calls).items():
            report(f'latency.{name}_ms_p50_p95', f'{p50} {p95}')
        if not offline_restart(api, expected):
            failures.append('offline restart')
        report('veda_log_lines', sum(1 for _ in open(tmp / 'veda.log', 'rb')))
        return failures
    finally:
        stop(veda)
        log.close()


def cleanup():
    ks.cmd_down(SimpleNamespace(volumes=False))
    for volume in OWN_VOLUMES:
        docker('volume', 'rm', volume, check=False)
    left = [v for v in OWN_VOLUMES if docker('volume', 'inspect', v, check=False).returncode == 0]
    report('cleanup.own_volumes_left', len(left))
    report('cleanup.model_cache_kept', docker('volume', 'inspect', MODEL_VOLUME, check=False).returncode == 0)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--bulk', type=int, default=24, help='generated short articles for timing')
    parser.add_argument('--calls', type=int, default=50, help='REST calls per latency probe')
    parser.add_argument('--timeout', type=float, default=600, help='seconds to wait for the stack and indexing')
    parser.add_argument('--example', help='write the owner REST answer for kg-public/billing.md to this file')
    args = parser.parse_args()
    preflight()
    tmp = Path(tempfile.mkdtemp(prefix='veda-kg-e2e-')).resolve()
    failures = ['did not finish']
    try:
        failures = run(args, tmp)
    finally:
        cleanup()
        if failures and os.environ.get('VEDA_SMOKE_KEEP_LOG') == '1':
            shutil.copy(tmp / 'veda.log', Path.cwd() / 'veda-knowledge-smoke.log')
        shutil.rmtree(tmp, ignore_errors=True)
    if failures:
        print('FAIL: ' + '; '.join(failures))
        return 1
    print('PASS: knowledge end to end (GLiNER2.5 CPU, Neo4j, REST, MCP, offline restart)')
    return 0


if __name__ == '__main__':
    sys.exit(main())
