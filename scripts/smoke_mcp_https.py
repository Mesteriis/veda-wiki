#!/usr/bin/env python3
"""Verify scoped MCP over TLS against an isolated real VEDA backend."""
import asyncio
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.request
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from save_reader_key import save_reader_key

ROOT = Path(__file__).resolve().parents[1]
BINARY = os.environ.get('VEDA_TEST_BINARY', str(ROOT / 'veda'))


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def parse(result):
    if result.isError:
        raise RuntimeError('MCP call was refused')
    return json.loads(next(block.text for block in result.content if block.type == 'text'))


async def check_tls(base, key, cert, revoke):
    params = StdioServerParameters(command=BINARY, args=['mcp',
        '--url', base, '--reader-key-file', str(key), '--allow-remote-reader-https'],
        env={**os.environ, 'SSL_CERT_FILE': str(cert)})
    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            names = {item.name for item in (await session.list_tools()).tools}
            if 'veda_prepare_new_article' not in names or 'veda_write_article' in names:
                raise RuntimeError('reader tool boundary mismatch')
            sources = parse(await session.call_tool('veda_sources', {}))['sources']
            if {source['id'] for source in sources} != {'allowed'}:
                raise RuntimeError('source scope mismatch')
            readable = await session.call_tool('veda_read', {'source_id': 'allowed', 'path': 'guide.md'})
            if parse(readable).get('text') != '# Synthetic allowed\n':
                raise RuntimeError('authorized article unavailable')
            denied = await session.call_tool('veda_read', {'source_id': 'hidden', 'path': 'guide.md'})
            if not denied.isError:
                raise RuntimeError('unauthorized article visible')
            proposal = parse(await session.call_tool('veda_prepare_new_article', {
                'source_id': 'allowed', 'path': 'proposal.md', 'text': '# Synthetic proposal\n'}))
            if proposal.get('saved') is not False:
                raise RuntimeError('proposal was not marked unsaved')
            revoke()
            denied = await session.call_tool('veda_read', {'source_id': 'allowed', 'path': 'guide.md'})
            if not denied.isError:
                raise RuntimeError('revoked key remained authorized')


async def check_independent_revocation(base, key_dir, cert):
    for name in ('codex', 'claude', 'opencode', 'hermes'):
        params = StdioServerParameters(command=BINARY, args=['mcp',
            '--url', base, '--reader-key-file', str(key_dir / (name + '.key')), '--allow-remote-reader-https'],
            env={**os.environ, 'SSL_CERT_FILE': str(cert)})
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                result = await session.call_tool('veda_read_excerpt', {
                    'source_id': 'allowed', 'path': 'guide.md', 'limit': 1})
                if result.isError != (name == 'codex'):
                    raise RuntimeError('revocation affected the wrong agent key')


with tempfile.TemporaryDirectory(prefix='veda-mcp-tls-') as tmp:
    root = Path(tmp)
    data = root / 'data'
    data.mkdir(mode=0o700)
    owner = data / 'owner.key'
    owner.write_text('d' * 64)
    owner.chmod(0o600)
    cert, certkey = root / 'cert.pem', root / 'tls-key.pem'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
        '-keyout', str(certkey), '-out', str(cert), '-days', '1', '-subj', '/CN=127.0.0.1',
        '-addext', 'subjectAltName=IP:127.0.0.1'], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    ports = set()
    while len(ports) < 2:
        ports.add(port())
    plain, tls = ports
    process = subprocess.Popen([BINARY, '--data', str(data), '--listen', f'127.0.0.1:{plain}',
        '--public-listen', '127.0.0.1:0',
        '--https-listen', f'127.0.0.1:{tls}', '--tls-cert', str(cert), '--tls-key', str(certkey)],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        context = ssl.create_default_context(cafile=str(cert))
        secure = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context))
        local = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        for _ in range(100):
            if process.poll() is not None:
                raise RuntimeError('VEDA exited')
            try:
                with secure.open(f'https://127.0.0.1:{tls}/health', timeout=2):
                    break
            except OSError:
                time.sleep(.05)
        else:
            raise RuntimeError('HTTPS was not ready')

        def owner_post(route, body):
            req = urllib.request.Request(f'http://127.0.0.1:{plain}' + route, method='POST',
                data=json.dumps(body).encode(), headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + 'd' * 64})
            with local.open(req, timeout=5) as response:
                return json.load(response)

        for source in ('allowed', 'hidden'):
            owner_post('/api/v1/sources', {'id': source, 'title': 'Synthetic', 'kind': 'wiki', 'visibility': 'private'})
            owner_post('/api/v1/articles', {'source_id': source, 'path': 'guide.md',
                'text': '# Synthetic ' + source + '\n', 'expected_revision': 0})
        issued = owner_post('/api/v1/access-keys', {'id': 'tls-agent', 'sources': ['allowed'],
            'expires_at': (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat()})
        key = root / 'agent.key'
        save_reader_key(key, issued['key'])
        asyncio.run(check_tls(f'https://127.0.0.1:{tls}', key, cert,
            lambda: owner_post('/api/v1/access-keys/revoke', {'id': 'tls-agent'})))
        owner_post('/api/v1/sources', {'id': 'public', 'title': 'Synthetic public', 'kind': 'wiki', 'visibility': 'public'})
        key_dir = root / 'agents'
        key_dir.mkdir(mode=0o700)
        values = []
        for name in ('codex', 'claude', 'opencode', 'hermes'):
            created = owner_post('/api/v1/access-keys', {'id': 'fixture-' + name, 'sources': ['allowed'],
                'expires_at': (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat()})
            values.append(created['key'])
            save_reader_key(key_dir / (name + '.key'), created['key'])
        if len(set(values)) != 4:
            raise RuntimeError('agent keys are not independent')
        command = [sys.executable, str(ROOT / 'scripts/check_agent_readers.py'), '--url', f'https://127.0.0.1:{tls}',
            '--source', 'allowed', '--article', 'guide.md', '--key-dir', str(key_dir)]
        env = {**os.environ, 'SSL_CERT_FILE': str(cert)}
        checked = subprocess.run(command, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        if checked.returncode or any(token.encode() in checked.stdout + checked.stderr for token in values):
            raise RuntimeError('four-agent reader check failed or exposed a key')
        duplicate_dir = root / 'duplicate-agents'
        duplicate_dir.mkdir(mode=0o700)
        for index, name in enumerate(('codex', 'claude', 'opencode', 'hermes')):
            save_reader_key(duplicate_dir / (name + '.key'), values[0] if index == 1 else values[index])
        duplicate = command.copy()
        duplicate[duplicate.index('--key-dir') + 1] = str(duplicate_dir)
        rejected_duplicate = subprocess.run(duplicate, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        if rejected_duplicate.returncode == 0 or any(token.encode() in rejected_duplicate.stdout + rejected_duplicate.stderr for token in values):
            raise RuntimeError('four-agent check accepted duplicate keys or exposed a key')
        wrong_source = command.copy()
        wrong_source[wrong_source.index('--source') + 1] = 'hidden'
        denied = subprocess.run(wrong_source, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        if denied.returncode == 0 or any(token.encode() in denied.stdout + denied.stderr for token in values):
            raise RuntimeError('four-agent check accepted the wrong scope or exposed a key')
        broad = owner_post('/api/v1/access-keys', {'id': 'fixture-broad', 'sources': ['allowed', 'hidden'],
            'expires_at': (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat()})['key']
        broad_dir = root / 'broad-agents'
        broad_dir.mkdir(mode=0o700)
        for index, name in enumerate(('codex', 'claude', 'opencode', 'hermes')):
            save_reader_key(broad_dir / (name + '.key'), broad if index == 0 else values[index])
        too_broad = command.copy()
        too_broad[too_broad.index('--key-dir') + 1] = str(broad_dir)
        rejected = subprocess.run(too_broad, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        if rejected.returncode == 0 or any(token.encode() in rejected.stdout + rejected.stderr for token in [broad, *values]):
            raise RuntimeError('four-agent check accepted a broader private scope or exposed a key')
        owner_post('/api/v1/access-keys/revoke', {'id': 'fixture-codex'})
        revoked = subprocess.run(command, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        if revoked.returncode == 0 or any(token.encode() in revoked.stdout + revoked.stderr for token in values):
            raise RuntimeError('four-agent check accepted a revoked key or exposed a key')
        asyncio.run(check_independent_revocation(f'https://127.0.0.1:{tls}', key_dir, cert))
        print('PASS: TLS MCP source scope, read, unsaved proposal, denial, independent revocation and four-agent checks')
    finally:
        process.terminate()
        process.wait(timeout=10)
