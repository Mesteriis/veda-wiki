"""Exercise the packaged MCP adapter against a disposable installed service."""
import argparse
import asyncio
import json
import os
import sys
from urllib.request import ProxyHandler, build_opener

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client


async def run(adapter, data, create, private_url, public_url):
    async def call(session, name, arguments):
        result = await session.call_tool(name, arguments)
        if result.isError or len(result.content) != 1:
            raise RuntimeError(f'MCP tool failed: {name}')
        return json.loads(result.content[0].text)

    if create:
        owner = StdioServerParameters(command=sys.executable, args=[adapter, '--url', private_url, '--key-file', f'{data}/owner.key'])
        async with stdio_client(owner) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                await call(session, 'veda_create_source', {'id': 'fixture', 'title': 'Fixture', 'kind': 'wiki', 'visibility': 'private'})
                await call(session, 'veda_write_article', {'source_id': 'fixture', 'path': 'guide.md', 'text': '# Synthetic fixture', 'expected_revision': 0})

    reader = StdioServerParameters(command=sys.executable, args=[adapter, '--url', private_url, '--reader-key-file', f'{data}/reader.key'])
    async with stdio_client(reader) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            names = {tool.name for tool in (await session.list_tools()).tools}
            assert 'veda_read' in names and 'veda_prepare_update' in names and 'veda_write_article' not in names
            article = await call(session, 'veda_read', {'source_id': 'fixture', 'path': 'guide.md'})
            assert article['text'] == '# Synthetic fixture' and article['revision'] == 1
            proposal = await call(session, 'veda_prepare_update', {'source_id': 'fixture', 'path': 'guide.md', 'expected_revision': 1, 'text': '# Proposed fixture'})
            assert proposal['saved'] is False and proposal['expected_revision'] == 1
    opener = build_opener(ProxyHandler({}))
    with opener.open(public_url + '/api/v1/articles', timeout=2) as response:
        assert json.load(response) == []
    print('PASS: packaged owner/reader MCP, private isolation and unsaved proposal')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--adapter', required=True)
    parser.add_argument('--data', required=True)
    parser.add_argument('--create', action='store_true')
    parser.add_argument('--private-url', default='http://127.0.0.1:8080')
    parser.add_argument('--public-url', default='http://127.0.0.1:8081')
    args = parser.parse_args()
    asyncio.run(run(os.path.abspath(args.adapter), os.path.abspath(args.data), args.create, args.private_url, args.public_url))
