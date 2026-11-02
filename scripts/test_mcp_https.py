"""Verify the optional remote reader transport without using real credentials."""
import asyncio
import importlib.util
import json
import os
from pathlib import Path
import ssl
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('veda_mcp_adapter', Path(__file__).resolve().parents[1] / 'mcp' / 'server.py')
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class Handler(BaseHTTPRequestHandler):
    requests = []

    def log_message(self, *args):
        pass

    def do_GET(self):
        type(self).requests.append((self.path, self.headers.get('Authorization')))
        if self.path == '/redirect':
            self.send_response(302)
            self.send_header('Location', '/target')
            self.end_headers()
            return
        body = json.dumps({'ok': True}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class ReaderHTTPSTests(unittest.TestCase):
    def test_remote_requires_explicit_read_only_https(self):
        for base, opts in [
            ('https://example.org', {'read_only': True}),
            ('https://example.org', {'allow_remote_reader_https': True}),
            ('http://example.org', {'read_only': True, 'allow_remote_reader_https': True}),
            ('https://example.org/path', {'read_only': True, 'allow_remote_reader_https': True}),
            ('https://user@example.org', {'read_only': True, 'allow_remote_reader_https': True}),
            ('https://example.org:99999', {'read_only': True, 'allow_remote_reader_https': True}),
        ]:
            with self.subTest(base=base, opts=opts), self.assertRaises(ValueError):
                adapter.Client(base, key_file='/fixture', **opts)
        client = adapter.Client('https://example.org', key_file='/fixture', read_only=True, allow_remote_reader_https=True)
        self.assertTrue(client.read_only)
        self.assertFalse(adapter.Client('http://127.0.0.1:8080', key_file='/fixture').read_only)

    def test_tls_verification_and_redirect_refusal(self):
        with tempfile.TemporaryDirectory(prefix='veda-mcp-https-') as tmp:
            root = Path(tmp)
            cert, private = root / 'cert.pem', root / 'test-private.pem'
            subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                            '-subj', '/CN=localhost', '-addext', 'subjectAltName=DNS:localhost',
                            '-keyout', str(private), '-out', str(cert)], check=True,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            key = root / 'reader.key'
            key.write_text('a' * 64)
            key.chmod(0o600)
            server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(cert, private)
            server.socket = context.wrap_socket(server.socket, server_side=True)
            worker = threading.Thread(target=server.serve_forever, daemon=True)
            worker.start()
            Handler.requests.clear()
            try:
                base = f'https://localhost:{server.server_port}'
                with patch.dict(os.environ, {'SSL_CERT_FILE': str(root / 'missing-ca.pem')}):
                    client = adapter.Client(base, key_file=str(key), read_only=True, allow_remote_reader_https=True)
                    with self.assertRaises(OSError):
                        client.request('GET', '/untrusted')
                self.assertEqual(Handler.requests, [])
                with patch.dict(os.environ, {'SSL_CERT_FILE': str(cert)}):
                    client = adapter.Client(base, key_file=str(key), read_only=True, allow_remote_reader_https=True)
                    self.assertEqual(client.request('GET', '/sources'), {'ok': True})
                    with self.assertRaises(ValueError):
                        client.request('GET', '/redirect')
                self.assertEqual([path for path, _ in Handler.requests], ['/sources', '/redirect'])
                self.assertTrue(all(auth == 'Bearer ' + 'a' * 64 for _, auth in Handler.requests))
                async def sdk_check():
                    from mcp import ClientSession, StdioServerParameters
                    from mcp.client.stdio import stdio_client
                    params = StdioServerParameters(command=sys.executable, args=[
                        str(Path(__file__).resolve().parents[1] / 'mcp' / 'server.py'),
                        '--url', base, '--reader-key-file', str(key), '--allow-remote-reader-https'],
                        env={**os.environ, 'SSL_CERT_FILE': str(cert)})
                    async with stdio_client(params) as (read, write):
                        async with ClientSession(read, write) as session:
                            await session.initialize()
                            names = {tool.name for tool in (await session.list_tools()).tools}
                            self.assertIn('veda_sources', names)
                            self.assertIn('veda_prepare_new_article', names)
                            self.assertNotIn('veda_write_article', names)
                            response = await session.call_tool('veda_sources', {})
                            self.assertFalse(response.isError)
                asyncio.run(sdk_check())
            finally:
                server.shutdown()
                server.server_close()
                worker.join(timeout=5)


if __name__ == '__main__':
    unittest.main()
