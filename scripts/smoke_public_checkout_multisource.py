"""Opt-in local-checkout acceptance for two anonymously cloned public repos.

This tests the checkout import path, not the GitHub connection or write-back.
All data is disposable and no credentials or real wiki data are used.
"""
import asyncio
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
from urllib.parse import urlencode
from urllib.request import ProxyHandler, Request, build_opener

from smoke import ROOT, free_port, start
from smoke_public_multisource import inspect_mcp, repository


def main():
    if os.environ.get('VEDA_TEST_PUBLIC_CHECKOUT_MULTI') != '1':
        print('SKIPPED: set VEDA_TEST_PUBLIC_CHECKOUT_MULTI=1 and two public repositories')
        return
    repos = [repository(os.environ.get('VEDA_TEST_PUBLIC_REPO_ONE')),
             repository(os.environ.get('VEDA_TEST_PUBLIC_REPO_TWO'))]
    if repos[0].lower() == repos[1].lower():
        raise ValueError('repositories must be distinct')
    env = {k: v for k, v in os.environ.items() if not k.startswith('GIT_') and k not in ('GH_TOKEN', 'GITHUB_TOKEN', 'SSH_ASKPASS')}
    env.update({'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': os.devnull,
                'GIT_TERMINAL_PROMPT': '0', 'GIT_ASKPASS': '/usr/bin/false'})
    with tempfile.TemporaryDirectory(prefix='veda-public-checkouts-') as tmp:
        root = Path(tmp)
        checkouts = []
        for index, repo in enumerate(repos):
            dest = root / ('repo' + str(index))
            subprocess.run(['git', '-c', 'credential.helper=', '-c', 'core.hooksPath=/dev/null',
                            'clone', '--quiet', '--depth=1', '--',
                            'https://github.com/' + repo + '.git', str(dest)],
                           env=env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=120)
            checkouts.append(dest)
        data = root / 'data'
        private, public = free_port(), free_port()
        proc = start(data, private, public)
        opener = build_opener(ProxyHandler({}))
        try:
            for ident, checkout in zip(('one', 'two'), checkouts):
                result = subprocess.run([str(ROOT / 'veda'), 'import', '--checkout', str(checkout), '--source', ident,
                    '--create', '--visibility', 'public', '--url', f'http://127.0.0.1:{private}',
                    '--key-file', str(data / 'owner.key')], check=True, stdout=subprocess.PIPE,
                    stderr=subprocess.DEVNULL, timeout=120)
                output = json.loads(result.stdout)
                if output.get('status') == 'skipped' or output.get('withheld', 0):
                    raise RuntimeError('checkout had no documentation or incomplete coverage')
            def public_api(route):
                with opener.open(Request(f'http://127.0.0.1:{public}' + route), timeout=20) as response:
                    return json.load(response)
            sources = public_api('/api/v1/sources')
            if {s['id'] for s in sources} != {'one', 'two'} or any(s['kind'] != 'program' or s.get('import_partial') for s in sources):
                raise RuntimeError('unexpected public source classification')
            pages = public_api('/api/v1/articles')
            if {p['environment'] for p in pages} != {'one', 'two'} or any('/docs/' not in p['path'] for p in pages):
                raise RuntimeError('merged page list differs')
            proc.terminate(); proc.wait(timeout=10); proc = None
            subprocess.run([str(ROOT / 'veda'), 'migrate-sqlite', '--data', str(data)], check=True,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=60)
            proc = start(data, private, public)
            for ident, checkout in zip(('one', 'two'), checkouts):
                page = next(p for p in pages if p['environment'] == ident)
                relative = page['path'][len(ident)+1:]
                article = public_api('/api/v1/articles?' + urlencode({'source_id': ident, 'path': relative}))
                original = (checkout / relative).read_bytes()
                revision = article['versions'][-1]
                if revision['text'].encode('utf-8') != original or revision['sha256'] != hashlib.sha256(original).hexdigest():
                    raise RuntimeError('imported article differs from its checkout')
            asyncio.run(inspect_mcp(data, {'one', 'two'}))
            print(json.dumps({'status':'passed', 'anonymous_git_clones':2, 'public_pages':len(pages),
                              'checkout_import':True, 'sqlite_restart':True, 'one_exact_article_per_source':True,
                              'mcp_merged_search_read_and_proposals':True,
                              'github_connection_tested':False}))
        finally:
            if proc is not None:
                proc.terminate(); proc.wait(timeout=10)


if __name__ == '__main__':
    main()
