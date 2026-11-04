"""Synthetic, non-personal articles for scripts/smoke_knowledge.py.

Sources:
- kg-public (local wiki, public): English and Russian service notes.
- kg-private (local wiki, private): private evidence, a long Cyrillic/emoji
  article and a guide.md with the same path as the public one.
- kg-gh (wiki, public): connected to a synthetic GitHub repository by
  editing the stopped store, so that its publication copy (rev 1) differs
  from the current revision (rev 2). The connection is never due for sync,
  so VEDA does not contact GitHub.
- kg-bulk (local wiki, private): N generated short articles for timing.
"""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

PUBLIC, PRIVATE, GH, BULK = 'kg-public', 'kg-private', 'kg-gh', 'kg-bulk'

ARTICLES = {
    (PUBLIC, 'billing.md'): (
        '# Billing\n\n'
        'The billing service runs on host db-01 and depends on PostgreSQL.\n\n'
        'Billing is part of the Payments project.\n\n'
        'The billing service no longer depends on MySQL.\n'),
    (PUBLIC, 'ledger.md'): (
        '# Леджер\n\n'
        'Сервис ledger работает на хосте db-01 и использует Redis.\n'),
    (PUBLIC, 'guide.md'): (
        '# Guide\n\n'
        'The search service runs on host idx-02 and uses Elasticsearch.\n'),
    (PRIVATE, 'vault.md'): (
        '# Vault proxy\n\n'
        'The vault-proxy service runs on host vault-07 and depends on Redis.\n\n'
        'The vault-proxy service also runs on host db-01.\n'),
    (PRIVATE, 'guide.md'): (
        '# Guide\n\n'
        'The audit service runs on host log-05 and uses ClickHouse.\n'),
}

GH_PATH = 'deploy.md'
GH_PUBLISHED = '# Deploy\n\nThe checkout service runs on host app-03.\n'
GH_CURRENT = '# Deploy\n\nThe checkout service runs on host app-04 and uses Kafka.\n'

LONG_PATH = 'long.md'
LONG_TAIL = 'В самом конце 🚀 сервис tail-gateway работает на хосте edge-09.'


def long_article():
    """One long paragraph (several extractor chunks) and a tail sentence."""
    filler = []
    for i in range(40):
        filler.append(f'Узел {i} 🧪 обрабатывает очередь заданий и пишет журнал в каталог номер {i}; '
                      f'проверка ёмкости проходит каждые {i + 5} минут без ошибок.')
    paragraph = ' '.join(filler) + ' ' + LONG_TAIL
    return '# Длинная заметка 📚\n\n' + paragraph + '\n'


def bulk_articles(n):
    out = {}
    for i in range(n):
        out[(BULK, f'svc-{i:03d}.md')] = (
            f'# Service {i}\n\n'
            f'The worker-{i:03d} service runs on host node-{i % 7:02d} and depends on PostgreSQL.\n')
    return out


def all_articles(bulk):
    """Every (source, path) -> current text, as VEDA should hold it."""
    articles = dict(ARTICLES)
    articles[(PRIVATE, LONG_PATH)] = long_article()
    articles[(GH, GH_PATH)] = GH_CURRENT
    articles.update(bulk_articles(bulk))
    return articles


def seed(api, bulk):
    """Create the sources and articles over the owner REST API."""
    for source, visibility in ((PUBLIC, 'public'), (PRIVATE, 'private'), (GH, 'public'), (BULK, 'private')):
        api('POST', '/api/v1/sources', {'id': source, 'title': source, 'kind': 'wiki', 'visibility': visibility})
    for (source, path), text in all_articles(bulk).items():
        if (source, path) == (GH, GH_PATH):
            api('POST', '/api/v1/articles', {'source_id': source, 'path': path, 'text': GH_PUBLISHED, 'expected_revision': 0})
            api('PUT', '/api/v1/articles', {'source_id': source, 'path': path, 'text': text, 'expected_revision': 1})
        else:
            api('POST', '/api/v1/articles', {'source_id': source, 'path': path, 'text': text, 'expected_revision': 0})


def connect_github_copy(data_dir):
    """Turn kg-gh into a verified public GitHub source whose publication copy
    is revision 1, in the stopped JSON store. NextAttempt is a day ahead, so
    the sync manager never contacts GitHub during the smoke run."""
    state_path = Path(data_dir) / 'state.json'
    db = json.loads(state_path.read_text())
    now = datetime.now(timezone.utc)
    commit = hashlib.sha1(b'veda-knowledge-smoke').hexdigest()
    tree = hashlib.sha1(b'veda-knowledge-smoke-tree').hexdigest()
    db.setdefault('github_connections', {})['kg-gh-conn'] = {
        'id': 'kg-gh-conn', 'repository': 'example/veda-knowledge-smoke', 'repository_id': 1, 'branch': 'main',
        'write': False, 'private': False, 'verification': 'verified',
        'verified_at': now.isoformat().replace('+00:00', 'Z'), 'state': 'ready', 'last_pull': commit,
        'next_attempt': now.replace(year=now.year + 1).isoformat().replace('+00:00', 'Z'), 'attempts': 0,
        'paused': False, 'config_revision': 1, 'tree_sha': tree}
    source = db['sources'][GH]
    source['connection_id'] = 'kg-gh-conn'
    source['import_commit'] = commit
    article = db['articles'][f'{GH}/{GH_PATH}']
    first = dict(article['versions'][0])
    assert first['text'] == GH_PUBLISHED and len(article['versions']) == 2
    first['revision'] = 0
    article['published'] = first
    article['published_local'] = False
    state_path.write_text(json.dumps(db))
    return hashlib.sha256(GH_PUBLISHED.encode()).hexdigest()


def sha256(text):
    return hashlib.sha256(text.encode()).hexdigest()
