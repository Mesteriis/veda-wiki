"""Exercise MCP search routing and the refusal details returned to agents."""
import asyncio
import importlib.util
import io
import json
from pathlib import Path
import unittest
from urllib.error import HTTPError
from urllib.parse import parse_qs, urlparse

from mcp import types

spec = importlib.util.spec_from_file_location('veda_mcp_adapter', Path(__file__).resolve().parents[1] / 'mcp' / 'server.py')
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class Opener:
    def __init__(self, status=200, body=b'{}'):
        self.status, self.body, self.urls = status, body, []

    def open(self, request, timeout):
        self.urls.append(request.full_url)
        if self.status >= 300:
            raise HTTPError(request.full_url, self.status, 'refused', {}, io.BytesIO(self.body))
        return io.BytesIO(self.body)


def call(opener, name, arguments):
    client = adapter.Client('http://127.0.0.1:8080', key_file='/fixture')
    client._read_key = lambda: 'fixture'
    client.opener = opener
    handler = adapter.make_server(client).request_handlers[types.CallToolRequest]
    request = types.CallToolRequest(method='tools/call', params=types.CallToolRequestParams(name=name, arguments=arguments))
    return asyncio.run(handler(request)).root


class MCPSearchTests(unittest.TestCase):
    def test_search_uses_bounded_search_endpoint(self):
        hits = {'results': [{'source_id': 'ops', 'path': 'vps.md', 'title': 'VPS'}], 'total': 1, 'offset': 0}
        opener = Opener(body=json.dumps(hits).encode())
        result = call(opener, 'veda_search', {'query': 'vps lxc'})
        self.assertFalse(result.isError)
        url = urlparse(opener.urls[0])
        self.assertEqual(url.path, '/api/v1/search')
        self.assertEqual(parse_qs(url.query), {'q': ['vps lxc'], 'limit': ['20']})
        self.assertEqual(result.structuredContent, hits)


class MCPRefusalTests(unittest.TestCase):
    def refusal(self, status, body):
        result = call(Opener(status, body), 'veda_read', {'source_id': 'ops', 'path': 'guide.md'})
        self.assertTrue(result.isError)
        return result.content[0].text

    def test_conflict_is_distinguishable(self):
        text = self.refusal(409, b'{"error":"revision conflict"}\n')
        self.assertIn('HTTP 409', text)
        self.assertIn('reread', text)
        self.assertIn('revision conflict', text)

    def test_not_found_and_access_are_distinct(self):
        self.assertIn('HTTP 404', self.refusal(404, b'{"error":"not found"}'))
        self.assertIn('HTTP 403', self.refusal(403, b'Read-only access\n'))
        self.assertNotIn('Read-only access', self.refusal(403, b'Read-only access\n'))

    def test_unexpected_error_text_is_not_echoed(self):
        text = self.refusal(502, b'{"error":"/private/path canary"}')
        self.assertIn('HTTP 502', text)
        self.assertNotIn('canary', text)


if __name__ == '__main__':
    unittest.main()
