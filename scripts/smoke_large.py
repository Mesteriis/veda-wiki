"""Exercise a synthetic large article through the official MCP SDK."""
import asyncio
import json
import sys
import tempfile
from pathlib import Path
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from smoke import ROOT, free_port, start

async def main():
    with tempfile.TemporaryDirectory(prefix='veda-large-smoke-') as tmp:
        data=Path(tmp)/'data'
        private,public=free_port(),free_port()
        proc=start(data,private,public)
        try:
            args=[str(ROOT/'mcp/server.py'),'--url',f'http://127.0.0.1:{private}','--key-file',str(data/'owner.key')]
            async with stdio_client(StdioServerParameters(command=sys.executable,args=args)) as (reader,writer):
                async with ClientSession(reader,writer) as session:
                    await session.initialize()
                    async def call(name,values):
                        result=await session.call_tool(name,values)
                        if result.isError: raise RuntimeError('synthetic MCP call failed')
                        return json.loads(result.content[0].text)
                    await call('veda_create_source',{'id':'fixture','title':'Fixture','kind':'wiki','visibility':'private'})
                    text='# Synthetic\n'+'я'*300000
                    await call('veda_write_article',{'source_id':'fixture','path':'large.md','text':text,'expected_revision':0})
                    current=await call('veda_read',{'source_id':'fixture','path':'large.md'})
                    assert current['text']==text and current['revision']==1
                    first=await call('veda_read_excerpt',{'source_id':'fixture','path':'large.md','limit':7})
                    assert first['text']==text[:7] and first['next_offset']==7
                    second=await call('veda_read_excerpt',{'source_id':'fixture','path':'large.md','offset':7,'limit':12,'expected_sha256':first['sha256']})
                    assert second['text']==text[7:19] and second['next_offset']==19
                    proposal=await call('veda_prepare_update',{'source_id':'fixture','path':'large.md','text':text,'expected_revision':1})
                    assert proposal['saved'] is False and proposal['text']==text
            state=json.loads((data/'state.json').read_text())
            assert state['version']==13
            print('PASS: synthetic large REST/MCP write, read, proposal and v13')
        finally:
            proc.terminate();proc.wait(timeout=10)

if __name__=='__main__':
    asyncio.run(main())
