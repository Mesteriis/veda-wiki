import { test } from 'vitest';
import assert from 'node:assert/strict';
import { catalogPages } from './catalog';

test('catalog selection keeps identical folder paths in distinct sources', () => {
  const groups = [
    { ID: 'one', Pages: [{ path: 'one/docs/a.md' }, { path: 'one/docs-old/b.md' }] },
    { ID: 'two', Pages: [{ path: 'two/docs/a.md' }] },
  ];
  assert.deepEqual(catalogPages(groups, groups.flatMap((g) => g.Pages), { sourceID: 'one', prefix: 'one/docs/' }), [{ path: 'one/docs/a.md' }]);
  assert.deepEqual(catalogPages(groups, [], { sourceID: 'two', prefix: 'two/docs/' }), [{ path: 'two/docs/a.md' }]);
  assert.deepEqual(catalogPages(groups, [], { sourceID: 'removed', prefix: 'removed/docs/' }), []);
});
