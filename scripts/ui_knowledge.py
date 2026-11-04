"""Deterministic fake knowledge extractor and synthetic articles for the browser fixture.

The fake speaks the extractor wire contract v1 (internal/knowledge/extract.go): it finds a
fixed dictionary of names in each block and a few relation phrases. It is not a model and
proves nothing about extraction quality; it only lets the browser screens show knowledge
data. Offsets are Python string indexes, which are Unicode code points like the contract.
"""
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import re
import threading
import time
import urllib.parse

# The pinned model identity: the VEDA pipeline refuses any other (internal/knowledge/pipeline.go).
MODEL = {
    'id': 'fastino/gliner2.5-multi-v1', 'revision': 'cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3', 'device': 'cpu',
    'pipeline': {'model_id': 'fastino/gliner2.5-multi-v1', 'model_revision': 'cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3',
                 'chunker_version': 1, 'window_tokens': 512, 'window_margin': 16, 'overlap_tokens': 64,
                 'max_entities_per_block': 512, 'max_relations_per_block': 1024,
                 'decoder': {'fake': 'veda-ui-fixture'}},
}
ENTITIES = {'billing': 'service', 'ledger': 'service', 'db1': 'host', 'Redis': 'technology', 'PostgreSQL': 'technology'}
RELATIONS = [
    ('billing', 'runs_on', 'db1', ('runs on', 'работает на')),
    ('billing', 'uses', 'Redis', ('uses', 'использует')),
    ('ledger', 'uses', 'PostgreSQL', ('uses', 'использует')),
    ('ledger', 'depends_on', 'billing', ('depends on', 'зависит от')),
]
ARTICLES = {
    'services/billing.md': '# Сервис billing\n\nСервис billing работает на db1 и использует Redis.\n\nЖурнал ledger зависит от billing.',
    'services/ledger.md': '# Сервис ledger\n\nledger uses PostgreSQL and depends on billing.\n\nКэш Redis общий с billing.',
}


def extract_block(block):
    text = block['text']
    entities, local = [], {}
    for name, kind in ENTITIES.items():
        match = re.search(r'(?<!\w)' + re.escape(name) + r'(?!\w)', text)
        if match:
            local[name] = 'e%d' % (len(entities) + 1)
            entities.append({'local_id': local[name], 'type': kind, 'start': match.start(), 'end': match.end(),
                             'text': name, 'confidence': 0.9})
    relations = [{'type': relation, 'head': local[head], 'tail': local[tail], 'confidence': 0.8,
                  'head_confidence': 0.9, 'tail_confidence': 0.9}
                 for head, relation, tail, phrases in RELATIONS
                 if head in local and tail in local and any(phrase in text for phrase in phrases)]
    return {'id': block['id'], 'chunks': [{'start': 0, 'end': len(text), 'feasible': True, 'status': 'ok'}],
            'entities': entities, 'relations': relations,
            'dropped': {'invalid_spans': 0, 'partial_spans': 0, 'invalid_relations': 0, 'over_limit': 0}}


def handler_for(key):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def answer(self, status, body):
            data = json.dumps(body).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            if self.path != '/ready':
                return self.answer(404, {'error': {'code': 'not_found'}})
            self.answer(200, {'status': 'ready', 'model': MODEL})

        def do_POST(self):
            if self.path != '/v1/extract' or self.headers.get('X-Veda-Extractor-Key') != key:
                return self.answer(401, {'error': {'code': 'unauthorized'}})
            request = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
            self.answer(200, {'schema_version': 1, 'request_id': request['request_id'], 'model': MODEL,
                              'blocks': [extract_block(block) for block in request['blocks']]})
    return Handler


@contextmanager
def fake_extractor(key):
    """Serve the fake extractor on a free loopback port; yields its URL."""
    server = ThreadingHTTPServer(('127.0.0.1', 0), handler_for(key))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield 'http://127.0.0.1:%d' % server.server_address[1]
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


def seed_knowledge(post, get):
    """Write the synthetic articles to the public fixture source and wait until they are indexed."""
    for path, text in ARTICLES.items():
        post('/api/v1/articles', {'source_id': 'fixture', 'path': path, 'text': text, 'expected_revision': 0})
    for path in ARTICLES:
        query = urllib.parse.urlencode({'source_id': 'fixture', 'path': path})
        for _ in range(200):
            if get('/api/v1/knowledge/article?' + query).get('status') == 'ready':
                break
            time.sleep(.05)
        else:
            raise RuntimeError('synthetic knowledge not indexed: ' + path)
