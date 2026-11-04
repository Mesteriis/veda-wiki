import { test } from 'vitest';
import assert from 'node:assert/strict';
import { fetchState, shouldPoll, type StateFetcher, type StateResponse } from './state';
import type { StatePayload } from './types';

test('conditional polling retains state only for a previously observed tag', async () => {
  let options: RequestInit | undefined;
  const fetcher: StateFetcher = async (_url, opts) => { options = opts; return { status: 304, ok: false } as StateResponse; };
  assert.deepEqual(await fetchState(fetcher, '/api/state', null, '"tag"'), { unchanged: true, etag: '"tag"' });
  assert.equal((options?.headers as Record<string, string>)['If-None-Match'], '"tag"');
  await assert.rejects(fetchState(fetcher, '/api/state', null, ''), /Wiki временно недоступна/);
});

test('new state and missing pages are handled explicitly', async () => {
  const value = { view: { Count: 1 } } as StatePayload;
  const ok: StateFetcher = async () => ({ status: 200, ok: true, headers: { get: () => '"next"' }, json: async () => value });
  assert.deepEqual(await fetchState(ok, '/api/state', null, ''), { data: value, etag: '"next"' });
  const missing: StateFetcher = async () => ({ status: 404, ok: false }) as StateResponse;
  await assert.rejects(fetchState(missing, '/api/state', null, '"old"'), (error: { status?: number }) => error.status === 404);
});

test('hidden tabs stop polling only after a state is loaded', () => {
  assert.equal(shouldPoll('visible', '"tag"'), true);
  assert.equal(shouldPoll('hidden', ''), true);
  assert.equal(shouldPoll('hidden', '"tag"'), false);
});

test('a payload with absent optional fields is accepted as typed', async () => {
  const payload: StatePayload = {
    view: {
      GraphEdges: null, Cards: null, SourceWarnings: null, Groups: [], Pages: null, Page: null,
      Public: true, Query: '', Selected: '', Generated: '', Withheld: 0, Count: 0, GitHubCount: 0, TOC: '',
    },
    html: '', toc: '',
  };
  const ok: StateFetcher = async () => ({ status: 200, ok: true, headers: { get: () => null }, json: async () => payload });
  const result = await fetchState(ok, '/api/state', null, '');
  assert.deepEqual(result, { data: payload, etag: '' });
  assert.equal('data' in result && result.data.view.GraphEdges, null);
});
