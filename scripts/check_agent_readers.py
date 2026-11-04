#!/usr/bin/env python3
"""Check four scoped VEDA reader keys without printing keys or wiki content."""
import argparse
import asyncio
import hashlib
import json
import os
from pathlib import Path
from urllib.parse import urlparse
import stat
import sys
import uuid
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import get_default_environment, stdio_client

AGENTS = ('codex', 'claude', 'opencode', 'hermes')
BINARY = Path(os.environ.get('VEDA_TEST_BINARY', Path(__file__).resolve().parents[1] / 'veda'))
READER_TOOLS = frozenset(('veda_sources', 'veda_search', 'veda_find', 'veda_read',
    'veda_read_display', 'veda_read_display_excerpt', 'veda_read_excerpt', 'veda_context',
    'veda_prepare_update', 'veda_prepare_new_article', 'veda_prepare_translation',
    'veda_translation_needed', 'veda_transfer_plan'))
REQUIRED_TOOLS = frozenset(('veda_sources', 'veda_read_excerpt', 'veda_context',
                            'veda_prepare_update', 'veda_prepare_new_article'))


def validate_reader_tools(names):
    if not REQUIRED_TOOLS.issubset(names) or not names.issubset(READER_TOOLS):
        raise ValueError('Unexpected tool set')


def validate_key_metadata(directory):
    directory = Path(directory)
    if not directory.is_absolute():
        raise ValueError('Key directory must be absolute')
    dir_fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    fingerprints = set()
    try:
        info = os.fstat(dir_fd)
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
            raise ValueError('Key directory must be owner-only')
        for agent in AGENTS:
            fd = os.open(agent + '.key', os.O_RDONLY | os.O_NOFOLLOW, dir_fd=dir_fd)
            try:
                info = os.fstat(fd)
                if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
                    raise ValueError('Agent key file must be an owner-only regular file')
                token = os.read(fd, 65)
                if len(token) != 64 or any(c not in b'0123456789abcdef' for c in token):
                    raise ValueError('Invalid agent key')
                fingerprints.add(hashlib.sha256(token).digest())
            finally:
                os.close(fd)
        if len(fingerprints) != len(AGENTS):
            raise ValueError('Agent keys must be distinct')
    finally:
        os.close(dir_fd)

def response_json(result):
    if result.isError:
        raise ValueError('VEDA refused a check')
    return json.loads(next(item.text for item in result.content if item.type == 'text'))


def adapter_parameters(key, base, env):
    """Launch `veda mcp` as a reader for one agent key over verified HTTPS."""
    return StdioServerParameters(command=str(BINARY), args=['mcp', '--url', base,
        '--reader-key-file', str(key), '--allow-remote-reader-https'], env=env)


async def verify_agent(agent, key, base, source, article):
    env = get_default_environment()
    for name in ('SSL_CERT_FILE', 'SSL_CERT_DIR'):
        if name in os.environ:
            env[name] = os.environ[name]
    params = adapter_parameters(key, base, env)
    with open(os.devnull, 'w') as errlog:
        async with stdio_client(params, errlog=errlog) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                names = {tool.name for tool in (await session.list_tools()).tools}
                validate_reader_tools(names)
                result = response_json(await session.call_tool('veda_sources', {}))
                private_ids = {item['id'] for item in result['sources'] if item.get('visibility') == 'private'}
                if private_ids != {source}:
                    raise ValueError('Private source scope does not match the expected source')
                excerpt = response_json(await session.call_tool('veda_read_excerpt', {
                    'source_id': source, 'path': article, 'limit': 1}))
                context = response_json(await session.call_tool('veda_context', {
                    'source_id': source, 'path': article}))
                if not isinstance(context.get('source_status'), dict):
                    raise ValueError('Source context unavailable')
                update = response_json(await session.call_tool('veda_prepare_update', {
                    'source_id': source, 'path': article, 'expected_revision': excerpt['revision'],
                    'text': '# Synthetic unsaved proposal\n'}))
                if (update.get('saved') is not False or update.get('expected_revision') != excerpt['revision']
                        or update.get('base_sha256') != excerpt['sha256']):
                    raise ValueError('Update proposal does not match the read revision')
                proposal = response_json(await session.call_tool('veda_prepare_new_article', {
                    'source_id': source, 'path': 'agent-check-' + uuid.uuid4().hex + '.md',
                    'text': '# Synthetic unsaved proposal\n'}))
                if proposal.get('saved') is not False or proposal.get('expected_revision') != 0:
                    raise ValueError('Proposal was not marked unsaved')
    print(agent + ': reader scope, context and unsaved proposals verified')


async def run(args):
    if urlparse(args.url).scheme != 'https':
        raise ValueError('HTTPS is required')
    directory = Path(args.key_dir)
    validate_key_metadata(directory)
    for agent in AGENTS:
        await asyncio.wait_for(verify_agent(agent, directory / (agent + '.key'), args.url, args.source, args.article), timeout=30)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', required=True, help='Trusted HTTPS VEDA origin')
    parser.add_argument('--source', required=True, help='Only authorized source ID')
    parser.add_argument('--article', required=True, help='Existing article within that source')
    parser.add_argument('--key-dir', required=True, help='Owner-only directory with one key per agent')
    args = parser.parse_args()
    try:
        asyncio.run(run(args))
    except Exception:
        print('Agent reader check failed; inspect source scope, permissions and connection.', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
