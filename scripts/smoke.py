"""Exercise the actual Go binary and official MCP client using disposable data."""
import asyncio
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen
from datetime import datetime, timedelta, timezone

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

ROOT = Path(__file__).resolve().parents[1]


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def start(data, private_port, public_port, require_socket_key=False):
    args = [
        str(ROOT / "veda"), "--data", str(data), "--listen", f"127.0.0.1:{private_port}",
        "--public-listen", f"127.0.0.1:{public_port}", "--reader-socket", str(data / "reader.sock")]
    if require_socket_key:
        args.append("--reader-socket-require-key")
    process = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
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
                    assert {tool.name for tool in tools.tools} == {
                        "veda_sources", "veda_search", "veda_find", "veda_read", "veda_read_excerpt", "veda_prepare_update", "veda_prepare_new_article", "veda_prepare_translation", "veda_create_source",
                        "veda_write_article", "veda_copy_article", "veda_transfer_plan", "veda_moves", "veda_cancel_move",
                        "veda_move_article", "veda_translation_needed", "veda_translate_article", "veda_context",
                        "veda_conflicts", "veda_resolve_conflict", "veda_github_status", "veda_github_connect",
                        "veda_sync", "veda_publish_job", "veda_cancel_bundle"}

                    async def call(name, args):
                        result = await session.call_tool(name, args)
                        assert not result.isError, (name, result.content)
                        return json.loads(result.content[0].text)

                    status=await call("veda_github_status",{})
                    assert status["connections"]==[] and status["outbox"]["total"]==0

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
                    await call("veda_create_source", {"id": "copy", "title": "copy", "kind": "wiki", "visibility": "private"})
                    copied = await call("veda_copy_article", {"source_id": "closed", "source_path": "README.md", "expected_revision": 2, "destination_id": "copy", "destination_path": "copied.md"})
                    assert copied["revision"] == 1
                    copy_read = await call("veda_read", {"source_id": "copy", "path": "copied.md"})
                    assert copy_read["text"] == "# Private updated"
                    await call("veda_create_source", {"id": "program", "title": "program", "kind": "program", "visibility": "private"})
                    original = await call("veda_write_article", {"source_id": "program", "path": "docs/setup.md", "text": "# Setup\nRun `install`.", "expected_revision": 0})
                    waiting = await call("veda_translation_needed", {})
                    assert any(c["source_id"] == "program" and c["original_sha256"] == original["sha256"] for c in waiting["candidates"])
                    translated = await call("veda_translate_article", {"source_id": "program", "path": "docs/setup.md", "expected_revision": 1, "original_sha256": original["sha256"], "text": "# Установка\nЗапустите `install`."})
                    assert translated["language"] == "ru"
                    unchanged = await call("veda_read", {"source_id": "program", "path": "docs/setup.md"})
                    assert unchanged["text"] == "# Setup\nRun `install`."
                    assert not (await call("veda_translation_needed", {}))["candidates"]
                    await call("veda_write_article", {"source_id": "program", "path": "docs/pending.md", "text": "# Pending", "expected_revision": 0})
                    await call("veda_create_source", {"id": "hidden-program", "title": "hidden", "kind": "program", "visibility": "private"})
                    await call("veda_write_article", {"source_id": "hidden-program", "path": "docs/hidden.md", "text": "# Hidden", "expected_revision": 0})
                    await call("veda_write_article", {"source_id":"closed", "path":"move.md", "text":"# Move fixture", "expected_revision":0})
                    moved = await call("veda_move_article", {"source_id":"closed", "source_path":"move.md", "expected_revision":1, "destination_id":"copy", "destination_path":"moved.md", "confirm_move":True})
                    assert moved["state"] == "complete"
                    assert (await call("veda_read", {"source_id":"copy", "path":"moved.md"}))["text"] == "# Move fixture"
                    assert (await session.call_tool("veda_read", {"source_id":"closed", "path":"move.md"})).isError
                    assert (await call("veda_moves", {}))["total"] == 1
                    # Exercise the actual CLI -> REST batch path on a real local
                    # checkout. Fixture data never enters the project repository.
                    checkout = Path(tmp) / "checkout"
                    checkout.mkdir()
                    (checkout / "veda.yaml").write_text("schema_version: 1\nkind: wiki\ntitle: Import fixture\n")
                    (checkout / "README.md").write_text("# Imported original")
                    git = ["git", "-C", str(checkout), "-c", "core.hooksPath=/dev/null",
                           "-c", "commit.gpgsign=false", "-c", "user.name=Fixture",
                           "-c", "user.email=fixture@example.invalid"]
                    for args in (["init", "-q"], ["add", "."], ["commit", "-qm", "Fixture"]):
                        subprocess.run(git + args, check=True, capture_output=True)
                    command = [str(ROOT / "veda"), "import", "--checkout", str(checkout),
                               "--source", "imported", "--url", f"http://127.0.0.1:{private_port}",
                               "--key-file", str(data / "owner.key")]
                    initial = subprocess.run(command + ["--create"], check=True, capture_output=True, text=True)
                    assert json.loads(initial.stdout)["created"] == 1
                    imported_context = await call("veda_context", {"source_id": "imported", "path": "README.md"})
                    assert imported_context["source_status"]["last_import_observed_at"]
                    assert imported_context["source_status"]["unresolved_conflicts"] == 0
                    await call("veda_write_article", {"source_id": "imported", "path": "README.md",
                        "text": "# Imported local edit", "expected_revision": 1})
                    (checkout / "README.md").write_text("# Imported upstream edit")
                    imported = subprocess.run(command, check=True, capture_output=True, text=True)
                    assert json.loads(imported.stdout)["conflicts"] == 1
                    pending = await call("veda_conflicts", {})
                    assert len(pending["conflicts"]) == 1
                    conflict_context = await call("veda_context", {"source_id": "imported", "path": "README.md"})
                    assert conflict_context["source_status"]["unresolved_conflicts"] == 1
                    await call("veda_resolve_conflict", {"source_id": "imported", "path": "README.md",
                        "index": 0, "expected_revision": 2, "resolution": "upstream"})
                    resolved = await call("veda_read", {"source_id": "imported", "path": "README.md"})
                    assert resolved["revision"] == 3 and resolved["text"] == "# Imported upstream edit"
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
                    reader_params = StdioServerParameters(command=sys.executable, args=[
                        str(ROOT / "mcp/server.py"), "--url", f"http://127.0.0.1:{private_port}",
                        "--reader-key-file", str(data / "reader.key")])
                    async with stdio_client(reader_params) as (reader_in, reader_out):
                        async with ClientSession(reader_in, reader_out) as reader:
                            await reader.initialize()
                            result = await reader.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                            assert not result.isError and json.loads(result.content[0].text)["text"] == "# Private updated"
                            names = {tool.name for tool in (await reader.list_tools()).tools}
                            assert names == {"veda_sources", "veda_search", "veda_find", "veda_read", "veda_read_excerpt", "veda_context", "veda_prepare_update", "veda_prepare_new_article", "veda_prepare_translation", "veda_translation_needed"}
                    def owner_post(route, value):
                        req = Request(f"http://127.0.0.1:{private_port}" + route,
                            data=json.dumps(value).encode(), method="POST",
                            headers={"Authorization": "Bearer " + (data / "owner.key").read_text(), "Content-Type": "application/json"})
                        with urlopen(req, timeout=3) as response:
                            return json.load(response)
                    issued = owner_post("/api/v1/access-keys", {"id": "agent-test", "sources": ["closed", "program"],
                        "expires_at": (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat()})
                    scoped_path = data / "scoped.key"
                    fd = os.open(scoped_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                    with os.fdopen(fd, "w") as stream:
                        stream.write(issued["key"])
                    scoped_params = StdioServerParameters(command=sys.executable, args=[
                        str(ROOT / "mcp/server.py"), "--url", f"http://127.0.0.1:{private_port}",
                        "--reader-key-file", str(scoped_path)])
                    async with stdio_client(scoped_params) as (scoped_in, scoped_out):
                        async with ClientSession(scoped_in, scoped_out) as scoped:
                            await scoped.initialize()
                            names = {tool.name for tool in (await scoped.list_tools()).tools}
                            assert names == {"veda_sources", "veda_search", "veda_find", "veda_read", "veda_read_excerpt", "veda_context", "veda_prepare_update", "veda_prepare_new_article", "veda_prepare_translation", "veda_translation_needed"}
                            visible = await scoped.call_tool("veda_sources", {})
                            assert {item["id"] for item in json.loads(visible.content[0].text)["sources"]} == {"closed", "program"}
                            found = await scoped.call_tool("veda_find", {"query": "Private updated", "limit": 1})
                            result = json.loads(found.content[0].text)
                            assert not found.isError and result["total"] == 1 and result["results"][0]["source_id"] == "closed"
                            hidden_find = await scoped.call_tool("veda_find", {"query": "Hidden", "source_id": "hidden-program"})
                            assert not hidden_find.isError and json.loads(hidden_find.content[0].text)["total"] == 0
                            queue = await scoped.call_tool("veda_translation_needed", {})
                            assert not queue.isError
                            candidates = json.loads(queue.content[0].text)
                            assert candidates["total"] == 1 and candidates["candidates"][0]["source_id"] == "program" and candidates["candidates"][0]["path"] == "docs/pending.md"
                            allowed = await scoped.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                            assert not allowed.isError
                            proposal = await scoped.call_tool("veda_prepare_update", {"source_id": "closed", "path": "README.md", "expected_revision": 2, "text": "# Proposed"})
                            prepared = json.loads(proposal.content[0].text)
                            assert not proposal.isError and prepared["saved"] is False and prepared["expected_revision"] == 2
                            stale = await scoped.call_tool("veda_prepare_update", {"source_id": "closed", "path": "README.md", "expected_revision": 1, "text": "# Stale"})
                            assert stale.isError
                            unchanged = await scoped.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                            assert json.loads(unchanged.content[0].text)["text"] == "# Private updated"
                            forbidden = await scoped.call_tool("veda_prepare_update", {"source_id": "copy", "path": "copied.md", "expected_revision": 1, "text": "# Forbidden"})
                            assert forbidden.isError
                            new_article = await scoped.call_tool("veda_prepare_new_article", {"source_id": "closed", "path": "new.md", "text": "# Proposed new article"})
                            assert not new_article.isError and json.loads(new_article.content[0].text)["expected_revision"] == 0 and json.loads(new_article.content[0].text)["saved"] is False
                            exists = await scoped.call_tool("veda_prepare_new_article", {"source_id": "closed", "path": "README.md", "text": "# Existing"})
                            assert exists.isError
                            missing = await scoped.call_tool("veda_read", {"source_id": "closed", "path": "new.md"})
                            assert missing.isError
                            unsafe = await scoped.call_tool("veda_prepare_new_article", {"source_id": "program", "path": "../outside.md", "text": "# Unsafe"})
                            assert unsafe.isError
                            unauthorized = await scoped.call_tool("veda_prepare_new_article", {"source_id": "copy", "path": "new.md", "text": "# Forbidden"})
                            assert unauthorized.isError
                            translation = await scoped.call_tool("veda_prepare_translation", {"source_id": "program", "path": "docs/setup.md", "expected_revision": 1, "text": "# Предложение\nЗапустите `install`."})
                            draft = json.loads(translation.content[0].text)
                            assert not translation.isError and draft["saved"] is False and draft["original_sha256"] == original["sha256"]
                            assert draft["validation"]["structural_valid"] is True and draft["validation"]["russian_prose"] is True
                            broken_translation = await scoped.call_tool("veda_prepare_translation", {"source_id": "program", "path": "docs/setup.md", "expected_revision": 1, "text": "# Ошибка\nЗапустите `different`."})
                            assert not broken_translation.isError and json.loads(broken_translation.content[0].text)["validation"]["structural_valid"] is False
                            stale_translation = await scoped.call_tool("veda_prepare_translation", {"source_id": "program", "path": "docs/setup.md", "expected_revision": 2, "text": "# Ошибка"})
                            assert stale_translation.isError
                            wrong_kind = await scoped.call_tool("veda_prepare_translation", {"source_id": "closed", "path": "README.md", "expected_revision": 2, "text": "# Ошибка"})
                            assert wrong_kind.isError
                            forbidden_translation = await scoped.call_tool("veda_prepare_translation", {"source_id": "copy", "path": "docs/a.md", "expected_revision": 1, "text": "# Ошибка"})
                            assert forbidden_translation.isError
                            denied = await scoped.call_tool("veda_read", {"source_id": "copy", "path": "copied.md"})
                            assert denied.isError
                            ctx = await scoped.call_tool("veda_context", {"source_id": "closed", "path": "README.md"})
                            assert not ctx.isError
                            forbidden_context = await scoped.call_tool("veda_context", {"source_id": "copy", "path": "copied.md"})
                            assert forbidden_context.isError
                            scoped_socket_params = StdioServerParameters(command=sys.executable, args=[
                                str(ROOT / "mcp/server.py"), "--reader-socket", str(data / "reader.sock"),
                                "--reader-key-file", str(scoped_path)])
                            async with stdio_client(scoped_socket_params) as (key_in, key_out):
                                async with ClientSession(key_in, key_out) as keyed_socket:
                                    await keyed_socket.initialize()
                                    allowed = await keyed_socket.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                                    assert not allowed.isError
                                    denied = await keyed_socket.call_tool("veda_read", {"source_id": "copy", "path": "copied.md"})
                                    assert denied.isError
                                    owner_post("/api/v1/access-keys/revoke", {"id": "agent-test"})
                                    revoked_socket = await keyed_socket.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                                    assert revoked_socket.isError
                            revoked = await scoped.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                            assert revoked.isError
                    socket_params = StdioServerParameters(command=sys.executable, args=[
                        str(ROOT / "mcp/server.py"), "--reader-socket", str(data / "reader.sock")])
                    async with stdio_client(socket_params) as (socket_in, socket_out):
                        async with ClientSession(socket_in, socket_out) as socket_reader:
                            await socket_reader.initialize()
                            checked = await socket_reader.call_tool("veda_prepare_translation", {"source_id": "program", "path": "docs/setup.md", "expected_revision": 1, "text": "# Перевод\nЗапустите `install`."})
                            assert not checked.isError and json.loads(checked.content[0].text)["validation"]["structural_valid"] is True
                            names = {tool.name for tool in (await socket_reader.list_tools()).tools}
                            assert names == {"veda_sources", "veda_search", "veda_find", "veda_read", "veda_read_excerpt", "veda_context", "veda_prepare_update", "veda_prepare_new_article", "veda_prepare_translation", "veda_translation_needed"}
                            item = await socket_reader.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                            assert not item.isError and json.loads(item.content[0].text)["text"] == "# Private updated"
                            ctx = await socket_reader.call_tool("veda_context", {"source_id": "closed", "path": "nested/example.md"})
                            assert not ctx.isError
                            context = json.loads(ctx.content[0].text)
                            assert context["instructions"][0]["path"] == "AGENTS.md" and "nested/AGENTS.md" in context["missing"]
                            refused = await socket_reader.call_tool("veda_write_article", {"source_id": "closed", "path": "README.md", "text": "# forbidden", "expected_revision": 2})
                            assert refused.isError
                    process.terminate()
                    process.wait(timeout=10)
                    process = start(data, private_port, public_port, require_socket_key=True)
                    restored = await call("veda_read", {"source_id": "closed", "path": "README.md"})
                    assert restored["revision"] == 2 and restored["text"] == "# Private updated"
                    after_restart = await call("veda_context", {"source_id": "imported", "path": "README.md"})
                    assert after_restart["source_status"]["last_import_observed_at"] is None
                    async with stdio_client(socket_params) as (socket_in, socket_out):
                        async with ClientSession(socket_in, socket_out) as socket_reader:
                            await socket_reader.initialize()
                            denied = await socket_reader.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                            assert denied.isError
                    issued = owner_post("/api/v1/access-keys", {"id": "required-test", "sources": ["closed"],
                        "expires_at": (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat()})
                    required_path = data / "required.key"
                    fd = os.open(required_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                    with os.fdopen(fd, "w") as stream:
                        stream.write(issued["key"])
                    os.chmod(required_path, 0o644)
                    invalid = subprocess.run([sys.executable, str(ROOT / "mcp/keyed_stdio_launcher.py"),
                        "--key-file", str(required_path), "--", sys.executable, "-c", "pass"],
                        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=5)
                    assert invalid.returncode != 0 and issued["key"].encode() not in invalid.stdout + invalid.stderr
                    os.chmod(required_path, 0o600)
                    required_params = StdioServerParameters(command=sys.executable, args=[
                        str(ROOT / "mcp/keyed_stdio_launcher.py"), "--key-file", str(required_path), "--",
                        sys.executable, str(ROOT / "mcp/server.py"), "--reader-socket", str(data / "reader.sock"),
                        "--reader-key-preamble"])
                    async with stdio_client(required_params) as (key_in, key_out):
                        async with ClientSession(key_in, key_out) as keyed_socket:
                            await keyed_socket.initialize()
                            allowed = await keyed_socket.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                            assert not allowed.isError
                            denied = await keyed_socket.call_tool("veda_read", {"source_id": "copy", "path": "copied.md"})
                            assert denied.isError
                            owner_post("/api/v1/access-keys/revoke", {"id": "required-test"})
                            revoked = await keyed_socket.call_tool("veda_read", {"source_id": "closed", "path": "README.md"})
                            assert revoked.isError
            print("PASS: owner MCP; scoped MCP read and revocation; context; import; conflicts; public isolation; copy; move; translation; reader socket; restart")
        finally:
            process.terminate()
            process.wait(timeout=10)


if __name__ == "__main__":
    asyncio.run(main())
