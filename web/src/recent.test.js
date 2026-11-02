import { test } from 'node:test';
import assert from 'node:assert/strict';
import { recordVisit, visibleVisits, readVisits, saveVisits, clearVisits } from './recent.js';
test('recent history deduplicates visits and keeps source-qualified paths', () => {
  let entries = recordVisit([], 'private/docs/a.md', 1);
  entries = recordVisit(entries, 'public/docs/a.md', 2);
  entries = recordVisit(entries, 'private/docs/a.md', 3);
  assert.deepEqual(entries.map((item) => item.path), ['private/docs/a.md', 'public/docs/a.md']);
  const publicGroups = [{ Name: 'Public', Pages: [{ path: 'public/docs/a.md', title: 'Public article' }] }];
  assert.deepEqual(visibleVisits(entries, publicGroups).map((item) => item.title), ['Public article']);
});
test('unavailable or corrupted tab storage never breaks reading', () => {
  const broken = { getItem() { throw Error(); }, setItem() { throw Error(); }, removeItem() { throw Error(); } };
  assert.deepEqual(readVisits(broken), []);
  assert.doesNotThrow(() => { saveVisits(broken, []); clearVisits(broken); });
  assert.deepEqual(readVisits({ getItem: () => '{bad' }), []);
});
