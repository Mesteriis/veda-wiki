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
    params = StdioServerParameters(command=sys.executable, args=[str(ROOT / 'mcp/server.py'),
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
        key.write_text(issued['key'])
        key.chmod(0o600)
        asyncio.run(check_tls(f'https://127.0.0.1:{tls}', key, cert,
            lambda: owner_post('/api/v1/access-keys/revoke', {'id': 'tls-agent'})))
        print('PASS: TLS MCP source scope, read, unsaved proposal, denial and revocation')
    finally:
        process.terminate()
        process.wait(timeout=10)
