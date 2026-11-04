#!/usr/bin/env python3
"""Score a running knowledge extractor on the annotated ru/en sentence set.

    python3 services/knowledge-extractor/eval/evaluate.py \
        --url http://127.0.0.1:8790 --key-file <0600 key file> [--json out.json]

Sends every sentence as its own block with the schema VEDA sends
(internal/knowledge/schema/v1.json), then compares the returned entities and
relations with the hand annotations in sentences.jsonl:

- strict: same type and the same surface text (case-insensitive);
- lenient: same type and one surface text contains the other.

Matching is one-to-one. Relations match on type and both end texts. The
annotation lists what the sentence states, whatever its modality; the
modality column (current, negation, plan, example, past) is scored on the Go
side by `go test -run TestMarkersOnEvalSet -v ./internal/knowledge/`, because
GLiNER2.5 does not classify modality and VEDA marks such claims "uncertain".

Uses only the standard library. Prints metrics and every error; the texts
are synthetic. Not part of CI: it needs the real model.
"""
import argparse
from collections import defaultdict
import json
from pathlib import Path
import time
import urllib.request

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]


def wire_schema(path):
    schema = json.loads(Path(path).read_text())
    return {
        'entities': {e['type']: e['description'] for e in schema['entities']},
        'relations': {r['type']: {'head': r['head'], 'tail': r['tail'], 'desc': r['description']}
                      for r in schema['relations']},
    }


def extract(url, key, schema, sentence):
    body = json.dumps({'schema_version': 1, 'request_id': f"eval-{sentence['id']}", 'schema': schema,
                       'blocks': [{'id': 'b1', 'text': sentence['text']}]}).encode()
    request = urllib.request.Request(url.rstrip('/') + '/v1/extract', data=body, method='POST', headers={
        'Content-Type': 'application/json', 'X-Veda-Extractor-Key': key})
    started = time.perf_counter()
    with urllib.request.urlopen(request, timeout=120) as response:
        result = json.loads(response.read())
    return result, time.perf_counter() - started


def predicted(result, text):
    block = result['blocks'][0]
    by_id = {}
    entities = []
    for e in block['entities']:
        assert text[e['start']:e['end']] == e['text'], 'quote does not match code-point offsets'
        by_id[e['local_id']] = e
        entities.append((e['type'], e['text'], e['confidence']))
    relations = [(r['type'], by_id[r['head']]['text'], by_id[r['tail']]['text'], r.get('confidence'))
                 for r in block['relations']]
    return entities, relations, block


def same(a, b, lenient):
    a, b = a.casefold().strip(), b.casefold().strip()
    return a == b or (lenient and (a in b or b in a))


def match(gold, pred, key, lenient):
    """One-to-one greedy matching; returns (matched pairs, unmatched gold, unmatched pred)."""
    left = list(pred)
    pairs, missed = [], []
    for g in gold:
        hit = next((p for p in left if key(g, p, lenient)), None)
        if hit is None:
            missed.append(g)
        else:
            left.remove(hit)
            pairs.append((g, hit))
    return pairs, missed, left


def entity_key(g, p, lenient):
    return g[0] == p[0] and same(g[1], p[1], lenient)


def relation_key(g, p, lenient):
    return g[0] == p[0] and same(g[1], p[1], lenient) and same(g[2], p[2], lenient)


class Counts:
    def __init__(self):
        self.tp, self.fp, self.fn = defaultdict(int), defaultdict(int), defaultdict(int)

    def add(self, pairs, missed, extra):
        for g, _ in pairs:
            self.tp[g[0]] += 1
        for g in missed:
            self.fn[g[0]] += 1
        for p in extra:
            self.fp[p[0]] += 1

    def rows(self):
        types = sorted(set(self.tp) | set(self.fp) | set(self.fn))
        out = []
        for t in types + ['ALL']:
            tp = sum(self.tp.values()) if t == 'ALL' else self.tp[t]
            fp = sum(self.fp.values()) if t == 'ALL' else self.fp[t]
            fn = sum(self.fn.values()) if t == 'ALL' else self.fn[t]
            p = tp / (tp + fp) if tp + fp else None
            r = tp / (tp + fn) if tp + fn else None
            f = 2 * p * r / (p + r) if p and r else None
            out.append({'type': t, 'tp': tp, 'fp': fp, 'fn': fn, 'precision': p, 'recall': r, 'f1': f})
        return out


def fmt(value):
    return '  -  ' if value is None else f'{value:.2f}'


def print_table(title, rows):
    print(f'\n{title}')
    print(f"{'type':<14}{'tp':>4}{'fp':>4}{'fn':>4}  {'P':>5} {'R':>5} {'F1':>5}")
    for row in rows:
        print(f"{row['type']:<14}{row['tp']:>4}{row['fp']:>4}{row['fn']:>4}  "
              f"{fmt(row['precision']):>5} {fmt(row['recall']):>5} {fmt(row['f1']):>5}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--url', default='http://127.0.0.1:8790')
    parser.add_argument('--key-file', required=True)
    parser.add_argument('--data', default=str(HERE / 'sentences.jsonl'))
    parser.add_argument('--schema', default=str(ROOT / 'internal' / 'knowledge' / 'schema' / 'v1.json'))
    parser.add_argument('--json', help='write the metrics and errors to this file')
    args = parser.parse_args()
    key = Path(args.key_file).read_text().strip()
    schema = wire_schema(args.schema)
    sentences = [json.loads(line) for line in Path(args.data).read_text().splitlines() if line.strip()]
    counts = {(kind, mode): Counts() for kind in ('entities', 'relations') for mode in ('strict', 'lenient')}
    errors, seconds, statuses = [], [], defaultdict(int)
    for s in sentences:
        result, elapsed = extract(args.url, key, schema, s)
        seconds.append(elapsed)
        entities, relations, block = predicted(result, s['text'])
        for chunk in block['chunks']:
            statuses[chunk['status']] += 1
        gold_e = [tuple(e) for e in s['entities']]
        gold_r = [tuple(r[:3]) for r in s['relations']]
        for mode, lenient in (('strict', False), ('lenient', True)):
            e_pairs, e_missed, e_extra = match(gold_e, entities, entity_key, lenient)
            r_pairs, r_missed, r_extra = match(gold_r, relations, relation_key, lenient)
            counts['entities', mode].add(e_pairs, e_missed, e_extra)
            counts['relations', mode].add(r_pairs, r_missed, r_extra)
            if lenient:
                errors += [(s['id'], 'missed entity', g) for g in e_missed]
                errors += [(s['id'], 'extra entity', p[:2] + (round(p[2], 2),)) for p in e_extra]
                errors += [(s['id'], 'missed relation', g) for g in r_missed]
                errors += [(s['id'], 'extra relation', p[:3] + (p[3] if p[3] is None else round(p[3], 2),)) for p in r_extra]
                errors += [(s['id'], 'loose entity span', (g[0], g[1], p[1])) for g, p in e_pairs if not same(g[1], p[1], False)]
    model = result['model']
    print(f"model {model['id']}@{model['revision'][:12]} device {model['device']}; "
          f"{len(sentences)} sentences; chunk statuses {dict(statuses)}")
    print(f'latency per sentence request: median {sorted(seconds)[len(seconds) // 2]:.3f}s, '
          f'max {max(seconds):.3f}s, total {sum(seconds):.1f}s')
    report = {'sentences': len(sentences), 'device': model['device'], 'statuses': dict(statuses), 'metrics': {}}
    for (kind, mode), c in counts.items():
        rows = c.rows()
        report['metrics'][f'{kind}_{mode}'] = rows
        print_table(f'{kind} ({mode})', rows)
    print(f'\nerrors (lenient matching): {len(errors)}')
    for sid, kind, item in errors:
        print(f'  {sid}: {kind}: {item}')
    report['errors'] = [{'id': sid, 'kind': kind, 'item': list(item)} for sid, kind, item in errors]
    if args.json:
        Path(args.json).write_text(json.dumps(report, ensure_ascii=False, indent=1))


if __name__ == '__main__':
    main()
