"""REST and MCP checks for scripts/smoke_knowledge.py (counts and booleans only)."""
import json
import statistics
import time
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, build_opener, ProxyHandler

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

import smoke_knowledge_fixture as fx

GENERIC_REFUSAL = 'VEDA request refused: invalid input, unavailable source, authorization or revision conflict.'
OPENER = build_opener(ProxyHandler({}))


class Api:
    def __init__(self, ports, data):
        self.ports, self.data = ports, data

    def call(self, port, method, route, body=None, token=None):
        headers = {'Content-Type': 'application/json'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        raw = None if body is None else json.dumps(body).encode()
        request = Request(f'http://127.0.0.1:{port}{route}', data=raw, headers=headers, method=method)
        try:
            with OPENER.open(request, timeout=30) as response:
                return response.status, json.loads(response.read() or b'null')
        except HTTPError as err:
            try:
                return err.code, json.loads(err.read() or b'null')
            except ValueError:
                return err.code, None

    def owner(self, method, route, body=None):
        token = (self.data / 'owner.key').read_text().strip()
        return self.call(self.ports['private'], method, route, body, token)

    def owner_ok(self, method, route, body=None):
        code, value = self.owner(method, route, body)
        if code >= 300:
            raise RuntimeError(f'{method} {route.split("?")[0]} answered {code}')
        return value

    def public(self, route, token=False):
        # With token=True the owner key is sent to the public listener, which must ignore it.
        key = (self.data / 'owner.key').read_text().strip() if token else None
        return self.call(self.ports['public'], 'GET', route, token=key)

    def health(self):
        try:
            with OPENER.open(f"http://127.0.0.1:{self.ports['private']}/health", timeout=1):
                return True
        except OSError:
            return False


def kg(route, **params):
    return f'/api/v1/knowledge/{route}?' + urlencode(params)


def evidences(value):
    """Every evidence object anywhere in a JSON answer."""
    if isinstance(value, dict):
        if 'quote' in value and 'source_start' in value:
            yield value
        for item in value.values():
            yield from evidences(item)
    elif isinstance(value, list):
        for item in value:
            yield from evidences(item)


def entity_names(article):
    return {e['name'] for e in ((article or {}).get('knowledge') or {}).get('entities', [])}


def has(names, token):
    """The model may include the type noun in a span ("host db-01")."""
    return any(token.casefold() in name.casefold() for name in names)


def find_entity(api_get, name):
    code, page = api_get(kg('entities', q=name, limit=100))
    if code != 200:
        return None
    for entity in page['entities']:
        if entity['name'].lower() == name.lower():
            return entity
    return None


def check_rest(api, articles, published_sha, report):
    failures = []

    def expect(name, ok):
        report(name, bool(ok))
        if not ok:
            failures.append(name)

    texts = {fx.sha256(text): text for text in articles.values()}
    texts[published_sha] = fx.GH_PUBLISHED
    owner_get = lambda route: api.owner('GET', route)  # noqa: E731
    public_get = api.public

    # Evidence quotes equal the Markdown at the reported code-point offsets.
    total = exact = matched = claims = uncertain = 0
    statuses = {}
    for (source, path), text in articles.items():
        code, article = owner_get(kg('article', source_id=source, path=path, limit=100))
        statuses[article.get('status') if code == 200 else code] = statuses.get(article.get('status') if code == 200 else code, 0) + 1
        for claim in ((article or {}).get('knowledge') or {}).get('claims', []):
            claims += 1
            uncertain += claim['status'] == 'uncertain'
        for ev in evidences(article):
            total += 1
            exact += ev['exact']
            source_text = texts.get(ev['content_sha256'], '')
            matched += source_text[ev['source_start']:ev['source_end']] == ev['quote']
    report('rest.owner_article_statuses', json.dumps(statuses, sort_keys=True))
    report('rest.evidence_total', total)
    report('rest.evidence_exact', exact)
    report('rest.claims_total', claims)
    report('rest.claims_uncertain', uncertain)
    expect('rest.all_articles_ready', statuses == {'ready': len(articles)})
    expect('rest.evidence_quotes_match_offsets', total > 0 and matched == total)

    # The long article: one Cyrillic/emoji paragraph over several chunks; the tail is processed.
    long_text = articles[(fx.PRIVATE, fx.LONG_PATH)]
    tail_start = len(long_text) - len(fx.LONG_TAIL) - 1
    _, long_article = owner_get(kg('article', source_id=fx.PRIVATE, path=fx.LONG_PATH, limit=100))
    tail = [ev for ev in evidences(long_article) if ev['source_start'] >= tail_start]
    report('rest.long_article_evidence', sum(1 for _ in evidences(long_article)))
    expect('rest.long_article_tail_extracted', any(ev['quote'] in ('edge-09', 'tail-gateway') for ev in tail))

    # Published copy (public) and current revision (owner) of kg-gh/deploy.md.
    _, pub = public_get(kg('article', source_id=fx.GH, path=fx.GH_PATH, limit=100))
    _, own = owner_get(kg('article', source_id=fx.GH, path=fx.GH_PATH, limit=100))
    _, copy = owner_get(kg('article', source_id=fx.GH, path=fx.GH_PATH, sha256=published_sha, limit=100))
    expect('rest.public_reads_publication_copy', pub.get('key', {}).get('content_sha256') == published_sha
           and has(entity_names(pub), 'app-03') and not has(entity_names(pub), 'app-04'))
    expect('rest.owner_reads_current_revision', own.get('key', {}).get('content_sha256') == fx.sha256(fx.GH_CURRENT)
           and has(entity_names(own), 'app-04') and not has(entity_names(own), 'app-03'))
    expect('rest.owner_reads_copy_by_sha', has(entity_names(copy), 'app-03'))

    # Same path in two sources stays two articles; the private one is 404 publicly.
    _, pub_guide = owner_get(kg('article', source_id=fx.PUBLIC, path='guide.md'))
    _, priv_guide = owner_get(kg('article', source_id=fx.PRIVATE, path='guide.md'))
    report('rest.guide_entities_public_private', f'{len(entity_names(pub_guide))} {len(entity_names(priv_guide))}')
    expect('rest.same_path_two_sources_distinct', has(entity_names(pub_guide), 'idx-02') and has(entity_names(priv_guide), 'log-05')
           and not has(entity_names(pub_guide), 'log-05') and not has(entity_names(priv_guide), 'idx-02'))
    expect('rest.private_article_404_public', public_get(kg('article', source_id=fx.PRIVATE, path='guide.md'))[0] == 404)

    # A host key shared across sources: one entity, private evidence counted only for the owner.
    own_db, pub_db = find_entity(owner_get, 'db-01'), find_entity(public_get, 'db-01')
    expect('rest.shared_host_one_entity', bool(own_db and pub_db and own_db['id'] == pub_db['id']))
    if own_db and pub_db:
        report('rest.db-01_documents_owner_public', f"{own_db['documents']} {pub_db['documents']}")
        expect('rest.db-01_public_count_excludes_private', pub_db['documents'] == 2 and own_db['documents'] >= 3)
        _, own_graph = owner_get(kg('graph', entity_id=own_db['id'], depth=2))
        _, pub_graph = public_get(kg('graph', entity_id=own_db['id'], depth=2))
        own_nodes = {(n.get('key') or {}).get('source_id') for n in own_graph['nodes']}
        pub_nodes = {(n.get('key') or {}).get('source_id') for n in pub_graph['nodes']}
        report('rest.db-01_graph_nodes_owner_public', f"{len(own_graph['nodes'])} {len(pub_graph['nodes'])}")
        expect('rest.graph_private_only_for_owner', fx.PRIVATE in own_nodes and fx.PRIVATE not in pub_nodes)
    expect('rest.private_host_hidden_publicly', find_entity(owner_get, 'vault-07') is not None
           and public_get(kg('entities', q='vault', limit=100))[1]['total'] == 0)

    # Technology names are source-scoped: the owner sees a candidate, the public does not.
    own_redis = [e for e in owner_get(kg('entities', q='Redis', limit=100))[1]['entities'] if e['type'] == 'technology']
    pub_redis = [e for e in public_get(kg('entities', q='Redis', limit=100))[1]['entities'] if e['type'] == 'technology']
    report('rest.redis_entities_owner_public', f'{len(own_redis)} {len(pub_redis)}')
    if pub_redis:
        _, own_card = owner_get(kg('entity', id=pub_redis[0]['id']))
        _, pub_card = public_get(kg('entity', id=pub_redis[0]['id']))
        expect('rest.candidate_only_for_owner', bool(own_card.get('candidates')) and not pub_card.get('candidates'))

    # Nothing private anywhere in public answers, with or without the owner key on the public listener.
    leaks = 0
    answers = 0
    _, page = public_get(kg('entities', limit=100))
    routes = [kg('entities', limit=100), kg('context', q='Redis', depth=2, limit_bytes=16000),
              kg('context', q='db-01', depth=2, limit_bytes=16000)]
    for entity in page['entities']:
        routes += [kg('entity', id=entity['id'], limit=100), kg('graph', entity_id=entity['id'], depth=2)]
    forbidden = (fx.PRIVATE, fx.BULK, 'vault', 'app-04', 'Kafka', fx.sha256(fx.GH_CURRENT))
    for route in routes:
        for token in (False, True):
            code, value = public_get(route, token=token)
            answers += 1
            body = json.dumps(value, ensure_ascii=False)
            leaks += code != 200 or any(word in body for word in forbidden)
    report('rest.public_answers_scanned', answers)
    expect('rest.public_answers_without_private_data', answers > 0 and leaks == 0)
    same = all(public_get(r)[1] == public_get(r, token=True)[1] for r in routes[:5])
    expect('rest.public_listener_ignores_owner_key', same)
    return failures


def text_json(result):
    if result.isError:
        return {'mcp_error': result.content[0].text}
    return json.loads(result.content[0].text)


async def check_mcp(binary, api, report):
    failures = []

    def expect(name, ok):
        report(name, bool(ok))
        if not ok:
            failures.append(name)

    db = find_entity(lambda r: api.owner('GET', r), 'db-01') or {'id': 'ent-00000000000000000000'}
    pairs = [
        ('veda_entities', {'query': 'db-01', 'limit': 20}, kg('entities', q='db-01', limit=20)),
        ('veda_entity', {'id': db['id'], 'limit': 20}, kg('entity', id=db['id'], limit=20)),
        ('veda_article_links', {'source_id': fx.PUBLIC, 'path': 'billing.md', 'limit': 50},
         kg('article', source_id=fx.PUBLIC, path='billing.md', limit=50)),
        ('veda_graph_context', {'query': 'Redis', 'depth': 2, 'limit_bytes': 8000},
         kg('context', q='Redis', depth=2, limit_bytes=8000)),
    ]
    owner = StdioServerParameters(command=str(binary), args=[
        'mcp', '--url', f"http://127.0.0.1:{api.ports['private']}", '--key-file', str(api.data / 'owner.key')])
    async with stdio_client(owner) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            tools = {tool.name for tool in (await session.list_tools()).tools}
            report('mcp.owner_tools', len(tools))
            for tool, args, route in pairs:
                got = text_json(await session.call_tool(tool, args))
                _, want = api.owner('GET', route)
                evidence = sum(1 for _ in evidences(got))
                report(f'mcp.{tool}_evidence', evidence)
                # The entity list carries summaries only; the other answers carry evidence.
                expect(f'mcp.{tool}_equals_rest', got == want and (evidence > 0 or tool == 'veda_entities'))
    reader = StdioServerParameters(command=str(binary), args=['mcp', '--reader-socket', str(api.data / 'reader.sock')])
    async with stdio_client(reader) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            tools = {tool.name for tool in (await session.list_tools()).tools}
            report('mcp.reader_socket_tools', len(tools))
            got = text_json(await session.call_tool('veda_entities', {'query': 'db-01', 'limit': 20}))
            _, want = api.owner('GET', kg('entities', q='db-01', limit=20))
            expect('mcp.reader_socket_entities_equal_rest', got == want)
            for name, args in (('veda_knowledge_reindex', {'scope': 'all', 'force': True}), ('veda_knowledge_status', {})):
                refused = await session.call_tool(name, args)
                expect(f'mcp.reader_socket_refuses_{name}', refused.isError and refused.content[0].text == GENERIC_REFUSAL)
    return failures


def latency(api, calls):
    """p50/p95 in ms of warm REST reads (owner and public), sequential."""
    db = find_entity(lambda r: api.owner('GET', r), 'db-01')
    probes = {
        'owner_entities': lambda: api.owner('GET', kg('entities', q='db', limit=50)),
        'owner_article': lambda: api.owner('GET', kg('article', source_id=fx.PUBLIC, path='billing.md')),
        'owner_graph_depth2': lambda: api.owner('GET', kg('graph', entity_id=db['id'], depth=2)),
        'owner_context_depth2': lambda: api.owner('GET', kg('context', q='PostgreSQL', depth=2)),
        'public_graph_depth2': lambda: api.public(kg('graph', entity_id=db['id'], depth=2)),
    }
    out = {}
    for name, probe in probes.items():
        probe()
        samples = []
        for _ in range(calls):
            started = time.perf_counter()
            code, _ = probe()
            samples.append((time.perf_counter() - started) * 1000)
            assert code == 200, (name, code)
        samples.sort()
        out[name] = (round(statistics.median(samples), 1), round(samples[max(0, int(len(samples) * 0.95) - 1)], 1))
    return out
