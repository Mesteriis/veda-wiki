import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import zlib from 'node:zlib';
import { encodePng, decodePng } from './ui_png.mjs';
import { compareImages, compareDirectories } from './ui_compare.mjs';

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

// Independent reference: forward-filter pixel rows by the PNG specification, then wrap in chunks.
const crcTable = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
const crc = buf => { let c = 0xffffffff; for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
const chunk = (type, body) => {
  const out = Buffer.alloc(12 + body.length);
  out.writeUInt32BE(body.length, 0);
  out.write(type, 4, 'latin1');
  body.copy(out, 8);
  out.writeUInt32BE(crc(out.subarray(4, 8 + body.length)), 8 + body.length);
  return out;
};
const paethRef = (a, b, c) => { const p = a + b - c, pa = Math.abs(p - a), pb = Math.abs(p - b), pc = Math.abs(p - c); return pa <= pb && pa <= pc ? a : pb <= pc ? b : c; };
function buildPng(width, rows, colorType, filters) {
  const bpp = colorType === 6 ? 4 : 3;
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0); ihdr.writeUInt32BE(rows.length, 4); ihdr.set([8, colorType, 0, 0, 0], 8);
  const lines = rows.map((row, y) => {
    const out = Buffer.alloc(row.length + 1);
    out[0] = filters[y];
    for (let x = 0; x < row.length; x++) {
      const left = x >= bpp ? row[x - bpp] : 0, up = y ? rows[y - 1][x] : 0, upLeft = y && x >= bpp ? rows[y - 1][x - bpp] : 0;
      const predictor = [0, left, up, (left + up) >> 1, paethRef(left, up, upLeft)][filters[y]];
      out[x + 1] = (row[x] - predictor) & 0xff;
    }
    return out;
  });
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr), chunk('IDAT', zlib.deflateSync(Buffer.concat(lines))), chunk('IEND', Buffer.alloc(0))]);
}
const rowsOf = (width, bpp) => [0, 1, 2, 3, 4, 5].map(y => Buffer.from(Array.from({ length: width * bpp }, (_, x) => (x * 37 + y * 91 + (x * y) % 7) & 0xff)));

test('decoder handles PNG filters none, sub, up, average and paeth on RGBA', () => {
  const rows = rowsOf(5, 4);
  const image = decodePng(buildPng(5, rows, 6, [0, 1, 2, 3, 4, 4]));
  assert.deepEqual(image.data, Buffer.concat(rows));
});

test('decoder handles every filter on RGB and expands to opaque RGBA', () => {
  const rows = rowsOf(4, 3);
  const image = decodePng(buildPng(4, rows, 2, [4, 3, 2, 1, 0, 4]));
  const expected = Buffer.alloc(4 * 6 * 4);
  rows.forEach((row, y) => { for (let x = 0; x < 4; x++) expected.set([row[x * 3], row[x * 3 + 1], row[x * 3 + 2], 255], (y * 4 + x) * 4); });
  assert.deepEqual(image.data, expected);
});

test('directory comparison rejects empty, extra, corrupt and unreadable inputs', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ui-compare-dirs-'));
  try {
    const base = path.join(dir, 'base'), next = path.join(dir, 'next');
    fs.mkdirSync(base); fs.mkdirSync(next);
    assert.ok(compareDirectories(base, next, 1).violations.some(v => /no PNG/.test(v.reason)));
    fs.writeFileSync(path.join(base, 'a-1.png'), encodePng(solid(4, 4)));
    fs.writeFileSync(path.join(next, 'a-1.png'), encodePng(solid(4, 4)));
    fs.writeFileSync(path.join(next, 'extra-1.png'), encodePng(solid(4, 4)));
    assert.deepEqual(compareDirectories(base, next, 0).violations.map(v => v.name), ['extra-1.png']);
    fs.unlinkSync(path.join(next, 'extra-1.png'));
    fs.writeFileSync(path.join(next, 'a-1.png'), Buffer.from('not a png'));
    assert.match(compareDirectories(base, next, 1).violations[0].reason, /unreadable/);
    fs.writeFileSync(path.join(next, 'a-1.png'), encodePng(solid(4, 5)));
    assert.equal(compareDirectories(base, next, 1).violations[0].reason, 'size differs');
    const missing = path.join(dir, 'nope');
    assert.match(compareDirectories(missing, next, 1).violations[0].reason, /cannot read directory/);
    const run = spawnSync('node', [new URL('./ui_compare.mjs', import.meta.url).pathname, missing, next], { encoding: 'utf8' });
    assert.equal(run.status, 1);
    assert.equal(run.stderr, '');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
