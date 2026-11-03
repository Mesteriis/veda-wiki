#!/usr/bin/env python3
"""Dump the MCP server identity and tool list in owner and reader mode.

The output is the golden file internal/mcp/testdata/tools.json. Listing tools
sends no REST request, so the adapter is pointed at an unused loopback URL
with a synthetic key file; no VEDA service or real key is involved.

Pass --veda BINARY to dump `BINARY mcp` and compare it with the golden file.
The golden file itself was produced from the former Python adapter
(mcp/server.py as of commit 4c595a2); to reproduce it, extract that file with
`git show 4c595a2:mcp/server.py > SERVER.py` (it needs the mcp SDK) and pass
--adapter SERVER.py.
"""
import argparse
import asyncio
import json
import os
from pathlib import Path
import sys
import tempfile
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

UNUSED_URL = 'http://127.0.0.1:9'


def command(veda, adapter, args):
    if veda:
        return veda, ['mcp', *args]
    return sys.executable, [adapter, *args]


async def dump(veda, adapter, args):
    program, argv = command(veda, adapter, ['--url', UNUSED_URL, *args])
    with open(os.devnull, 'w') as errlog:
        async with stdio_client(StdioServerParameters(command=program, args=argv), errlog=errlog) as (read, write):
            async with ClientSession(read, write) as session:
                init = await session.initialize()
                tools = (await session.list_tools()).tools
    server = {'name': init.serverInfo.name, 'version': init.serverInfo.version, 'instructions': init.instructions}
    listed = [{'name': tool.name, 'description': tool.description, 'inputSchema': tool.inputSchema} for tool in tools]
    return server, listed


async def run(veda, adapter):
    with tempfile.TemporaryDirectory(prefix='veda-dump-') as tmp:
        key = Path(tmp) / 'synthetic.key'
        fd = os.open(key, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as stream:
            stream.write('0' * 64)
        owner_server, owner = await dump(veda, adapter, ['--key-file', str(key)])
        reader_server, reader = await dump(veda, adapter, ['--reader-key-file', str(key)])
    if owner_server != reader_server:
        raise RuntimeError('server identity differs between modes')
    return {'server': owner_server, 'owner': owner, 'reader': reader}


def by_name(dumped):
    return {**dumped, 'owner': sorted(dumped['owner'], key=lambda t: t['name']),
            'reader': sorted(dumped['reader'], key=lambda t: t['name'])}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--veda', help='dump `VEDA mcp`')
    source.add_argument('--adapter', help='dump this extracted Python adapter file (historic)')
    parser.add_argument('--sort', action='store_true', help='sort tools by name (the Go server lists them sorted, the old adapter in definition order)')
    parser.add_argument('--output', help='write here instead of standard output')
    args = parser.parse_args()
    dumped = asyncio.run(run(args.veda, args.adapter))
    text = json.dumps(by_name(dumped) if args.sort else dumped, ensure_ascii=False, indent=2) + '\n'
    if args.output:
        Path(args.output).write_text(text, encoding='utf-8')
    else:
        sys.stdout.write(text)
    return 0


if __name__ == '__main__':
    sys.exit(main())
