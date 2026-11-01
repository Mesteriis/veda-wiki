import test from 'node:test';
import assert from 'node:assert/strict';
import { githubErrorMessage } from './githubErrors.js';

test('explains known errors without displaying arbitrary remote content', () => {
  assert.match(githubErrorMessage('rate_limited', 503, 'fallback'), /лимит/);
  assert.match(githubErrorMessage('', 409, 'fallback'), /настройки/i);
  assert.equal(githubErrorMessage('<private upstream response>', 503, 'fallback'), 'fallback');
  assert.equal(githubErrorMessage('constructor', 503, 'fallback'), 'fallback');
});
