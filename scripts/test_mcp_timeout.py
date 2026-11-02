"""Exercise the owner adapter's timeout for long private adoption requests."""
import importlib.util
import io
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('veda_mcp_adapter', Path(__file__).resolve().parents[1] / 'mcp' / 'server.py')
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class Opener:
    def __init__(self):
        self.timeouts = []

    def open(self, request, timeout):
        self.timeouts.append(timeout)
        return io.BytesIO(b'{}')


class MCPTimeoutTests(unittest.TestCase):
    def test_adoption_gets_longer_timeout_only_when_explicit(self):
        client = adapter.Client('http://127.0.0.1:8080', key_file='/fixture')
        client._read_key = lambda: 'fixture'
        client.opener = Opener()
        client.request('POST', '/api/v1/github/connections', {'id': 'test', 'adopt_existing': True})
        client.request('POST', '/api/v1/github/connections', {'id': 'test'})
        client.request('GET', '/api/v1/github/connections')
        self.assertEqual(client.opener.timeouts, [360, 15, 15])


if __name__ == '__main__':
    unittest.main()
