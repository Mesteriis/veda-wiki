"""Opt-in check of real public wiki/program checkouts with no remote writes."""
import asyncio
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from urllib.parse import urlencode
from urllib.request import ProxyHandler, Request, build_opener

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from smoke import ROOT, free_port, start
from smoke_public_multisource import repository

WIKI = 'Mesteriis/veda-wiki'
WIKI_BRANCH = 'feature/veda-live-source-20261101-221944'


async def inspect_mcp(data):
    params = StdioServerParameters(command=sys.executable, args=[str(ROOT / 'mcp/server.py'),
                                   '--reader-socket', str(data / 'reader.sock')])
    async with stdio_client(params) as (reader, writer):
        async with ClientSession(reader, writer) as session:
            await session.initialize()
            async def call(name, args):
                response = await session.call_tool(name, args)
                if response.isError:
                    raise RuntimeError('MCP request failed: ' + name)
                return json.loads(response.content[0].text)
            sources = await call('veda_sources', {})
            if {x['id'] for x in sources['sources']} != {'wiki', 'program'}:
                raise RuntimeError('MCP source mismatch')
            for source, query in [('wiki', 'synthetic'), ('program', 'docs')]:
                hits = await call('veda_find', {'source_id': source, 'query': query, 'limit': 1})
                if not hits['results']:
                    raise RuntimeError('MCP search omitted source')
                path = hits['results'][0]['path']
                article = await call('veda_read', {'source_id': source, 'path': path})
                proposal = await call('veda_prepare_update', {'source_id': source, 'path': path,
                    'expected_revision': article['revision'], 'text': article['text']})
                if proposal['saved'] is not False or proposal['base_sha256'] != article['sha256']:
                    raise RuntimeError('proposal mismatch')


def main():
    if os.environ.get('VEDA_TEST_PUBLIC_WIKI_PROGRAM') != '1':
        print('SKIPPED: set VEDA_TEST_PUBLIC_WIKI_PROGRAM=1 and VEDA_TEST_PUBLIC_PROGRAM_REPO')
        return
    program_repo = repository(os.environ.get('VEDA_TEST_PUBLIC_PROGRAM_REPO'))
    if program_repo.lower() == WIKI.lower():
        raise ValueError('repositories must be distinct')
    env = {k: v for k, v in os.environ.items() if not k.startswith('GIT_') and k not in ('GH_TOKEN', 'GITHUB_TOKEN', 'SSH_ASKPASS')}
    env.update({'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': os.devnull,
                'GIT_TERMINAL_PROMPT': '0', 'GIT_ASKPASS': '/usr/bin/false'})
    with tempfile.TemporaryDirectory(prefix='veda-public-wiki-program-') as tmp:
        root = Path(tmp)
        roots = []
        for ident, repo, branch in [('wiki', WIKI, WIKI_BRANCH), ('program', program_repo, None)]:
            dest = root / ident
            command = ['git', '-c', 'credential.helper=', '-c', 'core.hooksPath=/dev/null', 'clone', '--quiet', '--depth=1']
            if branch:
                command += ['--branch', branch]
            command += ['--', 'https://github.com/' + repo + '.git', str(dest)]
            subprocess.run(command, env=env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=120)
            roots.append(dest)
        data = root / 'data'
        private, public = free_port(), free_port()
        proc = start(data, private, public)
        opener = build_opener(ProxyHandler({}))
        try:
            for ident, checkout in zip(('wiki', 'program'), roots):
                result = subprocess.run([str(ROOT / 'veda'), 'import', '--checkout', str(checkout), '--source', ident,
                    '--create', '--visibility', 'public', '--url', f'http://127.0.0.1:{private}',
                    '--key-file', str(data / 'owner.key')], check=True, stdout=subprocess.PIPE,
                    stderr=subprocess.DEVNULL, timeout=120)
                output = json.loads(result.stdout)
                if output.get('status') == 'skipped' or output.get('withheld', 0):
                    raise RuntimeError('documentation missing or import incomplete')
            def public_api(route):
                with opener.open(Request(f'http://127.0.0.1:{public}' + route), timeout=20) as response:
                    return json.load(response)
            sources = public_api('/api/v1/sources')
            if {s['id']:s['kind'] for s in sources} != {'wiki':'wiki', 'program':'program'}:
                raise RuntimeError('source classification mismatch')
            wiki = next(s for s in sources if s['id'] == 'wiki')
            if wiki.get('metadata', {}).get('title') != 'Handbook':
                raise RuntimeError('wiki metadata missing')
            pages = public_api('/api/v1/articles')
            if {p['environment'] for p in pages} != {'wiki', 'program'} or any(not p['path'].startswith('program/docs/') for p in pages if p['environment'] == 'program'):
                raise RuntimeError('page coverage mismatch')
            with opener.open(f'http://127.0.0.1:{public}/media/wiki/img/icon.png', timeout=5) as response:
                image = response.read()
                if response.headers.get('Content-Type') != 'image/png' or hashlib.sha256(image).digest() != hashlib.sha256((roots[0] / 'img/icon.png').read_bytes()).digest():
                    raise RuntimeError('wiki image differs from checkout')
            for ident, checkout in zip(('wiki', 'program'), roots):
                page = next(p for p in pages if p['environment'] == ident)
                relative = page['path'][len(ident)+1:]
                article = public_api('/api/v1/articles?' + urlencode({'source_id': ident, 'path': relative}))
                original = (checkout / relative).read_bytes()
                revision = article['versions'][-1]
                if revision['text'].encode() != original or revision['sha256'] != hashlib.sha256(original).hexdigest():
                    raise RuntimeError('article differs from checkout')
            proc.terminate(); proc.wait(timeout=10); proc = None
            subprocess.run([str(ROOT / 'veda'), 'migrate-sqlite', '--data', str(data)], check=True,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=60)
            proc = start(data, private, public)
            asyncio.run(inspect_mcp(data))
            print(json.dumps({'status':'passed', 'wiki_metadata_and_image':True, 'program_docs_only':True,
                              'public_pages':len(pages), 'one_exact_article_per_source':True,
                              'sqlite_restart':True, 'mcp_read_and_proposals':True, 'github_api_tested':False}))
        finally:
            if proc is not None:
                proc.terminate(); proc.wait(timeout=10)


if __name__ == '__main__':
    main()
