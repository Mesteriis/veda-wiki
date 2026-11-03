import test from 'node:test';
import assert from 'node:assert/strict';
import { pollCondition } from './ui_poll.mjs';

test('read-only polling tolerates a destroyed navigation context', async () => {
  let calls = 0;
  await pollCondition(async () => {
    if (++calls === 1) throw Object.assign(Error('navigation'), { contextChanged: true });
    return calls === 3;
  }, 'condition', 4, async () => {});
  assert.equal(calls, 3);
});

test('other errors propagate and a permanently unavailable context times out', async () => {
  const failure = Error('unexpected');
  await assert.rejects(pollCondition(async () => { throw failure; }, 'condition', 3, async () => {}), error => error === failure);
  let calls = 0;
  await assert.rejects(pollCondition(async () => {
    calls++;
    throw Object.assign(Error('navigation'), { contextChanged: true });
  }, 'condition', 3, async () => {}), /page condition timed out/);
  assert.equal(calls, 3);
});
