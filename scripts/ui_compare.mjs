// Compare two directories of screenshots: node ui_compare.mjs BASE DIR --threshold 0.001
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { decodePng } from './ui_png.mjs';

// Share of pixels that differ in any channel; images of different size differ completely.
export function compareImages(baseBuffer, nextBuffer) {
  const base = decodePng(baseBuffer);
  const next = decodePng(nextBuffer);
  if (base.width !== next.width || base.height !== next.height) {
    return { ratio: 1, differing: Math.max(base.width * base.height, next.width * next.height), sizeMismatch: true };
  }
  let differing = 0;
  for (let i = 0; i < base.data.length; i += 4) {
    if (base.data.readUInt32LE(i) !== next.data.readUInt32LE(i)) differing++;
  }
  return { ratio: differing / (base.width * base.height), differing, sizeMismatch: false };
}

const pngs = dir => fs.readdirSync(dir).filter(name => name.endsWith('.png')).sort();

// A size mismatch is always a violation; otherwise a screen violates when its ratio exceeds the threshold.
export function compareDirectories(baseDir, nextDir, threshold) {
  const violations = [];
  let baseNames = [], nextNames = [];
  for (const [label, dir] of [['baseline', baseDir], ['new capture', nextDir]]) {
    try {
      const names = pngs(dir);
      if (label === 'baseline') baseNames = names; else nextNames = names;
    } catch (error) {
      violations.push({ name: label, reason: `cannot read directory ${dir}: ${error.code || error.message}` });
    }
  }
  if (violations.length) return { checked: 0, violations };
  const names = [...new Set([...baseNames, ...nextNames])].sort();
  let checked = 0;
  for (const name of names) {
    if (!baseNames.includes(name)) { violations.push({ name, reason: 'extra screen not in baseline' }); continue; }
    if (!nextNames.includes(name)) { violations.push({ name, reason: 'missing in new capture' }); continue; }
    try {
      const result = compareImages(fs.readFileSync(path.join(baseDir, name)), fs.readFileSync(path.join(nextDir, name)));
      checked++;
      if (result.sizeMismatch) violations.push({ name, reason: 'size differs' });
      else if (result.ratio > threshold) violations.push({ name, reason: `${(result.ratio * 100).toFixed(3)}% pixels differ (${result.differing})` });
    } catch (error) {
      violations.push({ name, reason: `unreadable image: ${error.message}` });
    }
  }
  if (!names.length) violations.push({ name: '(none)', reason: 'no PNG files found to compare' });
  return { checked, violations };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const at = args.indexOf('--threshold');
  const threshold = at < 0 ? 0.001 : Number(args.splice(at, 2)[1]);
  const [baseDir, nextDir] = args;
  if (!baseDir || !nextDir || !Number.isFinite(threshold)) {
    console.error('usage: ui_compare.mjs BASE DIR [--threshold 0.001]');
    process.exit(2);
  }
  const { checked, violations } = compareDirectories(baseDir, nextDir, threshold);
  for (const v of violations) console.log(`${v.name.replace(/\.png$/, '')}: ${v.reason}`);
  console.log(`${checked} screens compared, ${violations.length} violations`);
  process.exit(violations.length ? 1 : 0);
}
