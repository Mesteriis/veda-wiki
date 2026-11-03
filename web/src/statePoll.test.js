import test from 'node:test';
import assert from 'node:assert/strict';
import { fetchState } from './statePoll.js';

test('conditional polling retains state only for a previously observed tag', async () => {
  let options;
  const fetcher = async (_url, opts) => { options = opts; return { status: 304, ok: false }; };
  assert.deepEqual(await fetchState(fetcher, '/api/state', null, '"tag"'), { unchanged: true, etag: '"tag"' });
  assert.equal(options.headers['If-None-Match'], '"tag"');
  await assert.rejects(fetchState(fetcher, '/api/state', null, ''), /Wiki временно недоступна/);
});

test('new state and missing pages are handled explicitly', async () => {
  const value = { view: { Count: 1 } };
  const ok = async () => ({ status: 200, ok: true, headers: { get: () => '"next"' }, json: async () => value });
  assert.deepEqual(await fetchState(ok, '/api/state', null, ''), { data: value, etag: '"next"' });
  const missing = async () => ({ status: 404, ok: false });
  await assert.rejects(fetchState(missing, '/api/state', null, '"old"'), error => error.status === 404);
});
