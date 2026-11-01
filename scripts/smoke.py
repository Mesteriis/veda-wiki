"""Exercise the actual Go binary and official MCP client using disposable data."""
import asyncio
import json
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import urlopen

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

ROOT = Path(__file__).resolve().parents[1]


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def start(data, private_port, public_port):
    process = subprocess.Popen([
        str(ROOT / "veda"), "--data", str(data), "--listen", f"127.0.0.1:{private_port}",
        "--public-listen", f"127.0.0.1:{public_port}"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError("Go server exited")
        try:
            with urlopen(f"http://127.0.0.1:{private_port}/health", timeout=1):
                return process
        except OSError:
            time.sleep(0.05)
    process.terminate()
    process.wait(timeout=10)
    raise RuntimeError("Go server did not become ready")


def public_json(port, route):
    with urlopen(f"http://127.0.0.1:{port}{route}", timeout=3) as response:
        return json.load(response)


async def main():
    with tempfile.TemporaryDirectory(prefix="veda-smoke-") as tmp:
        data = Path(tmp) / "data"
        private_port, public_port = free_port(), free_port()
        process = start(data, private_port, public_port)
        try:
            parameters = StdioServerParameters(command=sys.executable, args=[
                str(ROOT / "mcp/server.py"), "--url", f"http://127.0.0.1:{private_port}",
                "--key-file", str(data / "owner.key")])
            async with stdio_client(parameters) as (read, write):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    tools = await session.list_tools()
                    assert len(tools.tools) == 6

                    async def call(name, args):
                        result = await session.call_tool(name, args)
                        assert not result.isError, (name, result.content)
                        return json.loads(result.content[0].text)

                    for source, visibility in [("open", "public"), ("closed", "private")]:
                        await call("veda_create_source", {"id": source, "title": source,
                            "kind": "wiki", "visibility": visibility})
                        await call("veda_write_article", {"source_id": source, "path": "README.md",
                            "text": "# Shared fixture" if source == "open" else "# Private canary",
                            "expected_revision": 0})
                    current = await call("veda_read", {"source_id": "closed", "path": "README.md"})
                    assert current["revision"] == 1 and current["text"] == "# Private canary"
                    await call("veda_write_article", {"source_id": "closed", "path": "README.md",
                        "text": "# Private updated", "expected_revision": 1})
                    conflict = await session.call_tool("veda_write_article", {"source_id": "closed",
                        "path": "README.md", "text": "# Stale", "expected_revision": 1})
                    assert conflict.isError
                    await call("veda_write_article", {"source_id": "closed", "path": "AGENTS.md",
                        "text": "# Read source context first", "expected_revision": 0})
                    context = await call("veda_context", {"source_id": "closed", "path": "README.md"})
                    assert context["instructions"][0]["path"] == "AGENTS.md"
                    for port in (private_port, public_port):
                        public = public_json(port, "/api/state")
                        encoded = json.dumps(public)
                        assert public["view"]["Count"] == 1
                        assert "closed" not in encoded and "Private" not in encoded
                        try:
                            public_json(port, "/api/state?page=closed/README.md")
                            raise AssertionError("private article disclosed")
                        except HTTPError as error:
                            assert error.code == 404
                    process.terminate()
                    process.wait(timeout=10)
                    process = start(data, private_port, public_port)
                    restored = await call("veda_read", {"source_id": "closed", "path": "README.md"})
                    assert restored["revision"] == 2 and restored["text"] == "# Private updated"
            print("PASS: six MCP tools; create/read/update; conflict; context; public isolation; restart persistence")
        finally:
            process.terminate()
            process.wait(timeout=10)


if __name__ == "__main__":
    asyncio.run(main())
