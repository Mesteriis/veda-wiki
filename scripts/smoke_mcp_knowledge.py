"""Knowledge MCP tools through the official SDK against `veda --knowledge`.

The server uses the in-memory index and a deterministic fake extractor on
loopback that marks every "db1" as a host; no model, Docker or network is
involved. Called from smoke.py with its server helpers.
"""
import json
import os
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

MODEL = {"id": "fastino/gliner2.5-multi-v1", "revision": "cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3",
         "device": "cpu", "pipeline": {"chunker_version": 1}}
GENERIC_REFUSAL = "VEDA request refused: invalid input, unavailable source, authorization or revision conflict."


class FakeExtractor(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def answer(self, status, value):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path != "/ready":
            return self.answer(404, {"error": {"code": "not_found"}})
        self.answer(200, {"status": "ready", "model": MODEL})

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        blocks = []
        for block in request["blocks"]:
            text = block["text"]
            entities = []
            start = text.find("db1")
            if start >= 0 and "host" in request["schema"]["entities"]:
                entities.append({"local_id": "e1", "type": "host", "start": start, "end": start + 3,
                                 "text": "db1", "confidence": 0.9})
            blocks.append({"id": block["id"], "chunks": [{"start": 0, "end": len(text), "feasible": True, "status": "ok"}],
                           "entities": entities, "relations": []})
        self.answer(200, {"schema_version": 1, "request_id": request["request_id"], "model": MODEL, "blocks": blocks})


def text_json(result):
    assert not result.isError, result.content
    return json.loads(result.content[0].text)


async def knowledge_smoke(start, free_port, root, reader_tools, owner_tools):
    extractor = ThreadingHTTPServer(("127.0.0.1", 0), FakeExtractor)
    thread = threading.Thread(target=extractor.serve_forever, daemon=True)
    thread.start()
    with tempfile.TemporaryDirectory(prefix="veda-kg-smoke-") as tmp:
        data = Path(tmp).resolve() / "data"
        key = Path(tmp).resolve() / "extractor.key"
        fd = os.open(key, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            stream.write("synthetic-extractor-key\n")
        private_port, public_port = free_port(), free_port()
        process = start(data, private_port, public_port, extra=(
            "--knowledge", "--knowledge-index", "memory", "--knowledge-extractor-key-file", str(key),
            "--knowledge-extractor-url", f"http://127.0.0.1:{extractor.server_address[1]}"))
        try:
            owner = StdioServerParameters(command=str(root / "veda"), args=[
                "mcp", "--url", f"http://127.0.0.1:{private_port}", "--key-file", str(data / "owner.key")])
            async with stdio_client(owner) as (read, write):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    assert {tool.name for tool in (await session.list_tools()).tools} == owner_tools
                    text_json(await session.call_tool("veda_create_source", {
                        "id": "kg", "title": "kg", "kind": "wiki", "visibility": "private"}))
                    text_json(await session.call_tool("veda_write_article", {
                        "source_id": "kg", "path": "billing.md", "expected_revision": 0,
                        "text": "# Billing\n\nThe billing service runs on db1.\n"}))
                    deadline = time.monotonic() + 30
                    while True:
                        status = text_json(await session.call_tool("veda_knowledge_status", {}))
                        entities = text_json(await session.call_tool("veda_entities", {"query": "db1"}))
                        if status["enabled"] and entities["total"] >= 1:
                            break
                        assert time.monotonic() < deadline, status
                        time.sleep(0.2)
                    entity = entities["entities"][0]
                    assert entity["name"] == "db1" and entity["type"] == "host"
                    card = text_json(await session.call_tool("veda_entity", {"id": entity["id"], "limit": 5}))
                    evidence = card["mention_list"][0]["evidence"]
                    assert evidence["source_id"] == "kg" and evidence["path"] == "billing.md" and evidence["quote"] == "db1"
                    links = text_json(await session.call_tool("veda_article_links", {"source_id": "kg", "path": "billing.md"}))
                    assert links["status"] == "ready"
                    graph = text_json(await session.call_tool("veda_graph", {"entity_id": entity["id"], "depth": 1}))
                    assert any(node["id"] == entity["id"] for node in graph["nodes"])
                    context = text_json(await session.call_tool("veda_graph_context", {"query": "db1", "limit_bytes": 2000}))
                    assert context["bytes"] <= 2000 and context["items"]
                    reindex = text_json(await session.call_tool("veda_knowledge_reindex", {
                        "scope": "article", "source_id": "kg", "path": "billing.md"}))
                    assert reindex["operation_id"].startswith("reindex-")
                    unknown = await session.call_tool("veda_entities", {"query": "db1", "cypher": "MATCH (n) RETURN n"})
                    assert unknown.isError and unknown.content[0].text == "Input validation error: unknown property 'cypher'"
            reader = StdioServerParameters(command=str(root / "veda"), args=["mcp", "--reader-socket", str(data / "reader.sock")])
            async with stdio_client(reader) as (read, write):
                async with ClientSession(read, write) as socket_reader:
                    await socket_reader.initialize()
                    assert {tool.name for tool in (await socket_reader.list_tools()).tools} == reader_tools
                    found = text_json(await socket_reader.call_tool("veda_entities", {"query": "db1"}))
                    assert found["entities"][0]["id"] == entity["id"]
                    for name, args in (("veda_knowledge_reindex", {"scope": "all", "force": True}), ("veda_knowledge_status", {})):
                        refused = await socket_reader.call_tool(name, args)
                        assert refused.isError and refused.content[0].text == GENERIC_REFUSAL, (name, refused.content)
            print("PASS: knowledge MCP owner reads, status and reindex; reader socket reads, owner tools refused")
        finally:
            process.terminate()
            process.wait(timeout=10)
            extractor.shutdown()
            extractor.server_close()
            thread.join(timeout=5)
