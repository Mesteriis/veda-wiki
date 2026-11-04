import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { encodePng, decodePng } from './ui_png.mjs';
import { compareImages } from './ui_compare.mjs';

const size = 100;
const solid = (w, h, rgba = [20, 40, 60, 255]) => {
  const data = Buffer.alloc(w * h * 4);
  for (let i = 0; i < w * h; i++) data.set(rgba, i * 4);
  return { width: w, height: h, data };
};

test('png round trip preserves pixels', () => {
  const image = solid(7, 5);
  image.data[13] = 99;
  const back = decodePng(encodePng(image));
  assert.equal(back.width, 7);
  assert.equal(back.height, 5);
  assert.deepEqual(back.data, image.data);
});

test('identical images differ in 0 pixels', () => {
  const a = encodePng(solid(size, size));
  assert.equal(compareImages(a, encodePng(solid(size, size))).ratio, 0);
});

test('one percent changed pixels is above a 0.001 threshold', () => {
  const changed = solid(size, size);
  for (let i = 0; i < size * size / 100; i++) changed.data.set([255, 0, 0, 255], i * 4);
  const result = compareImages(encodePng(solid(size, size)), encodePng(changed));
  assert.equal(result.differing, 100);
  assert.equal(result.ratio, 0.01);
});

test('different sizes are a full difference', () => {
  const result = compareImages(encodePng(solid(10, 10)), encodePng(solid(10, 11)));
  assert.equal(result.ratio, 1);
  assert.equal(result.sizeMismatch, true);
});

test('cli reports screens above the threshold and fails', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ui-compare-'));
  try {
    const base = path.join(dir, 'base');
    const next = path.join(dir, 'next');
    fs.mkdirSync(base);
    fs.mkdirSync(next);
    const changed = solid(size, size);
    for (let i = 0; i < 100; i++) changed.data.set([255, 0, 0, 255], i * 4);
    fs.writeFileSync(path.join(base, 'same-390.png'), encodePng(solid(size, size)));
    fs.writeFileSync(path.join(next, 'same-390.png'), encodePng(solid(size, size)));
    fs.writeFileSync(path.join(base, 'moved-390.png'), encodePng(solid(size, size)));
    fs.writeFileSync(path.join(next, 'moved-390.png'), encodePng(changed));
    fs.writeFileSync(path.join(base, 'gone-390.png'), encodePng(solid(size, size)));
    const run = threshold => spawnSync('node', [new URL('./ui_compare.mjs', import.meta.url).pathname, base, next, '--threshold', threshold], { encoding: 'utf8' });
    const strict = run('0.001');
    assert.equal(strict.status, 1);
    assert.match(strict.stdout, /moved-390/);
    assert.match(strict.stdout, /gone-390/);
    assert.doesNotMatch(strict.stdout, /same-390/);
    fs.unlinkSync(path.join(base, 'gone-390.png'));
    assert.equal(run('0.02').status, 0);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
