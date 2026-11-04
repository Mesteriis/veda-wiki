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

export function compareDirectories(baseDir, nextDir, threshold) {
  const names = [...new Set([...pngs(baseDir), ...pngs(nextDir)])].sort();
  const violations = [];
  for (const name of names) {
    const basePath = path.join(baseDir, name), nextPath = path.join(nextDir, name);
    if (!fs.existsSync(basePath)) violations.push({ name, reason: 'missing in baseline' });
    else if (!fs.existsSync(nextPath)) violations.push({ name, reason: 'missing in new capture' });
    else {
      const result = compareImages(fs.readFileSync(basePath), fs.readFileSync(nextPath));
      if (result.ratio > threshold) violations.push({ name, reason: result.sizeMismatch ? 'size differs' : `${(result.ratio * 100).toFixed(3)}% pixels differ (${result.differing})` });
    }
  }
  return { checked: names.length, violations };
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
