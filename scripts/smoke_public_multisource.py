"""Opt-in read-only multi-repository acceptance using anonymous GitHub and MCP."""
import asyncio
import json
import os
import re
import subprocess
import tempfile
import time
from pathlib import Path
from urllib.request import Request, build_opener, ProxyHandler

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from smoke import ROOT, free_port, start


def repository(value):
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', value or ''):
        raise ValueError('set both repository variables to OWNER/REPOSITORY')
    return value


async def inspect_mcp(data, expected):
    params = StdioServerParameters(command=str(ROOT / 'veda'), args=[
        'mcp', '--reader-socket', str(data / 'reader.sock')])
    async with stdio_client(params) as (reader, writer):
        async with ClientSession(reader, writer) as session:
            await session.initialize()
            async def call(name, arguments):
                result = await session.call_tool(name, arguments)
                if result.isError:
                    raise RuntimeError('MCP request failed: ' + name)
                return json.loads(result.content[0].text)
            sources = await call('veda_sources', {})
            if {x['id'] for x in sources['sources']} != expected:
                raise RuntimeError('MCP sources differ')
            found, offset = set(), 0
            while True:
                page = await call('veda_find', {'query': 'docs', 'limit': 100, 'offset': offset})
                found.update(x['source_id'] for x in page['results'])
                offset += len(page['results'])
                if offset >= page['total']:
                    break
                if not page['results'] or offset > 4000:
                    raise RuntimeError('search pagination failed')
            if found != expected:
                raise RuntimeError('merged search omitted a source')
            for source_id in expected:
                page = await call('veda_find', {'query': 'docs', 'source_id': source_id, 'limit': 1})
                if not page['results']:
                    raise RuntimeError('source has no searchable docs')
                path = page['results'][0]['path']
                article = await call('veda_read', {'source_id': source_id, 'path': path})
                proposal = await call('veda_prepare_update', {'source_id': source_id, 'path': path,
                    'expected_revision': article['revision'], 'text': article['text']})
                if proposal['saved'] is not False or proposal['base_sha256'] != article['sha256']:
                    raise RuntimeError('proposal not bound to current revision')


def main():
    if os.environ.get('VEDA_TEST_PUBLIC_MULTI') != '1':
        print('SKIPPED: set VEDA_TEST_PUBLIC_MULTI=1 and supply two public program repositories')
        return
    repositories = [repository(os.environ.get('VEDA_TEST_PUBLIC_REPO_ONE')),
                    repository(os.environ.get('VEDA_TEST_PUBLIC_REPO_TWO'))]
    if repositories[0].lower() == repositories[1].lower():
        raise ValueError('repositories must be distinct')
    opener = build_opener(ProxyHandler({}))
    req = Request('https://api.github.com/rate_limit', headers={'User-Agent': 'VEDA-multisource-acceptance'})
    with opener.open(req, timeout=15) as response:
        remaining = json.load(response)['resources']['core']['remaining']
    if remaining < 40:
        print('SKIPPED: fewer than 40 anonymous GitHub core requests remain')
        return
    with tempfile.TemporaryDirectory(prefix='veda-public-multisource-') as tmp:
        data = Path(tmp) / 'data'
        data.mkdir(mode=0o700)
        for name, char in [('owner.key', 'd'), ('reader.key', 'e')]:
            file = data / name
            file.write_text(char * 64)
            file.chmod(0o600)
        private, public = free_port(), free_port()
        proc = start(data, private, public)
        def api(route, body=None, public_surface=False):
            headers = {'Content-Type': 'application/json'}
            if not public_surface:
                headers['Authorization'] = 'Bearer ' + 'd' * 64
            raw = None if body is None else json.dumps(body).encode()
            req = Request(f'http://127.0.0.1:{public if public_surface else private}' + route,
                          data=raw, headers=headers)
            with opener.open(req, timeout=30) as response:
                return json.load(response)
        try:
            for ident, repo in zip(('one', 'two'), repositories):
                api('/api/v1/github/connections', {'id': ident, 'repository': repo, 'write': False})
            deadline = time.monotonic() + 120
            while time.monotonic() < deadline:
                connections = api('/api/v1/github/connections')
                if any(x.get('state') in ('error', 'conflict', 'blocked', 'skipped') for x in connections):
                    raise RuntimeError('upstream import did not complete')
                if len(connections) == 2 and all(x.get('state') == 'ready' for x in connections):
                    break
                time.sleep(.3)
            else:
                raise RuntimeError('upstream import timed out')
            sources = api('/api/v1/sources')
            if len(sources) != 2 or any(x.get('kind') != 'program' or x.get('visibility') != 'public'
                                        or x.get('import_partial') for x in sources):
                raise RuntimeError('repository classification or import coverage mismatch')
            pages = api('/api/v1/articles', public_surface=True)
            if {p['environment'] for p in pages} != {'one', 'two'} or any('/docs/' not in p['path'] for p in pages):
                raise RuntimeError('public merged pages differ')
            proc.terminate()
            proc.wait(timeout=10)
            proc = None
            subprocess.run([str(ROOT / 'veda'), 'migrate-sqlite', '--data', str(data)],
                           check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=60)
            proc = start(data, private, public)
            asyncio.run(inspect_mcp(data, {'one', 'two'}))
            print(json.dumps({'status': 'passed', 'anonymous': True, 'distinct_repositories': 2,
                              'public_pages': len(pages), 'sqlite_restart': True,
                              'mcp_merged_search_read_and_proposals': True}))
        finally:
            if proc is not None:
                proc.terminate()
                proc.wait(timeout=10)


if __name__ == '__main__':
    main()
