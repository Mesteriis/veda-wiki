// Capture reference screenshots of the reading interface over CDP.
//
// Known limits of the captures (they are a regression net, not a pixel-exact truth):
// - time is frozen: API timestamps are rewritten to one instant, the client Date is pinned, timezone is UTC,
//   so real date-handling regressions are not visible (the formatting itself still renders);
// - Chrome runs with --hide-scrollbars (see screens.py) and prefers-reduced-motion, animations and caret blink are off;
// - pages are captured at full content height (a page taller than MAX_HEIGHT fails the run) at the given width;
// - the baseline depends on this machine's Chrome version and fonts: recapture it when either changes.
// usage: node ui_screens.mjs PORT PUBLIC_URL PRIVATE_URL KEY_FILE OUT_DIR
import fs from 'node:fs';
import path from 'node:path';
import { pollCondition } from './ui_poll.mjs';

const [port, publicUrl, privateUrl, keyFile, outDir] = process.argv.slice(2);
// Synthetic fixture keys only: the owner key is the constant from scripts/ui_fixture.py, the reader key is read from keyFile.
const readerKey = fs.readFileSync(keyFile, 'utf8');
const ownerKey = 'd'.repeat(64);
const WIDTHS = [1672, 390, 320];
const HEIGHT = 941;
const MAX_HEIGHT = 8000;
const FREEZE_CSS = '*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important;scroll-behavior:auto!important}';

// Client clock pinned to the same instant as the API timestamps (history times, relative dates).
const FREEZE_DATE = `(() => { const T = Date.parse('2026-01-15T12:00:00Z'), Real = Date;
  function FrozenDate(...args) { return args.length ? new Real(...args) : new Real(T); }
  FrozenDate.prototype = Real.prototype; FrozenDate.now = () => T; FrozenDate.parse = Real.parse; FrozenDate.UTC = Real.UTC;
  globalThis.Date = FrozenDate; })()`;

let pages;
for (let i = 0; i < 1200 && !pages; i++) {
  try { pages = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json(); }
  catch { await new Promise(resolve => setTimeout(resolve, 50)); }
}
if (!pages) throw Error('debugger unavailable');
const ws = new WebSocket(pages.find(page => page.type === 'page').webSocketDebuggerUrl);
await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
let id = 0;
const inflight = new Map(); // requestId -> start time
let lastNetwork = Date.now();
const pending = new Map();
ws.onmessage = event => {
  const message = JSON.parse(event.data);
  if (message.id && pending.has(message.id)) {
    const call = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) call.reject(Error(`CDP error: ${call.method} code=${message.error.code}`));
    else call.resolve(message.result);
  } else if (message.method === 'Fetch.requestPaused') pinTimes(message.params).catch(() => {}).finally(() => inflight.delete(message.params.networkId));
  else if (message.method === 'Network.requestWillBeSent') { inflight.set(message.params.requestId, Date.now()); lastNetwork = Date.now(); }
  else if (message.method === 'Network.loadingFinished' || message.method === 'Network.loadingFailed') { inflight.delete(message.params.requestId); lastNetwork = Date.now(); }
};
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const next = ++id;
  pending.set(next, { resolve, reject, method });
  ws.send(JSON.stringify({ id: next, method, params }));
});
// Server timestamps depend on when the fixture was seeded; rewrite them in API responses to one fixed instant.
const TIMESTAMP = /"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)"/g;
const PINNED = '"2026-01-15T12:00:00Z"';
async function pinTimes({ requestId, responseStatusCode, responseHeaders = [] }) {
  const json = responseHeaders.some(h => h.name.toLowerCase() === 'content-type' && h.value.includes('json'));
  if (!json || responseStatusCode !== 200) return send('Fetch.continueRequest', { requestId });
  const { body, base64Encoded } = await send('Fetch.getResponseBody', { requestId });
  const text = (base64Encoded ? Buffer.from(body, 'base64').toString('utf8') : body).replace(TIMESTAMP, PINNED);
  return send('Fetch.fulfillRequest', {
    requestId, responseCode: 200, body: Buffer.from(text).toString('base64'),
    responseHeaders: responseHeaders.filter(h => !['content-length', 'content-encoding'].includes(h.name.toLowerCase())),
  });
}
const evaluate = async expression => {
  const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw Object.assign(Error('page evaluation failed'), { pageException: result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'exception' });
  return result.result.value;
};
const until = expression => pollCondition(evaluate, expression);
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

// Fonts loaded, no new request for 500 ms and none younger than 3 s still pending
// (a stuck long-lived request must not stall the capture), two animation frames painted.
const busy = () => [...inflight.values()].some(started => Date.now() - started < 3000);
async function settle() {
  await pollCondition(evaluate, 'document.fonts.status === "loaded"');
  for (let i = 0; i < 100; i++) {
    if (!busy() && Date.now() - lastNetwork > 500) break;
    await sleep(100);
  }
  await evaluate('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
}

const go = async (url, ready) => { await send('Page.navigate', { url }); await until(ready); };
const HOME = "!!document.querySelector('.home-page-row')";
const article = 'fixture%2Fprojects%2Farchitecture.md';
const adminLogin = route => evaluate(`(async () => (await fetch(${JSON.stringify(route)}, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({key:${JSON.stringify(ownerKey)}})})).ok)()`);

const guest = [
  ['home', () => go(publicUrl, HOME)],
  ['catalog', () => go(publicUrl + '?env=all', "!!document.querySelector('.catalog')")],
  ['article', () => go(publicUrl + '?page=' + article, "!!document.querySelector('.markdown') && document.body.innerText.includes('Демонстрационное содержимое')")],
  ['graph', () => go(publicUrl + '?graph=1', "!!document.querySelector('.graph-section')")],
  ['recent', async () => {
    // The previous width seeded history in this tab; the plain recent screen must start empty.
    await go(publicUrl, HOME);
    await evaluate("sessionStorage.removeItem('veda.recent.v1')");
    await go(publicUrl + '?recent=1', "!!document.querySelector('.recent-history-empty')");
  }],
  ['recent-filled', async () => {
    // History lives in sessionStorage (web/src/recent.js); seed it with fixed times before opening the view.
    await go(publicUrl, HOME);
    const visits = ['fixture/projects/architecture.md', 'fixture/guides/intro.md', 'mirror/projects/architecture.md']
      .map((visitedPath, index) => ({ path: visitedPath, visitedAt: Date.parse('2026-01-15T11:00:00Z') - index * 3600000 }));
    await evaluate(`sessionStorage.setItem('veda.recent.v1', ${JSON.stringify(JSON.stringify(visits))})`);
    await go(publicUrl + '?recent=1', "!!document.querySelector('.recent-history-row')");
  }, () => evaluate("sessionStorage.removeItem('veda.recent.v1')")],
  ['login', async () => {
    await go(privateUrl, HOME);
    await evaluate("document.querySelector('button[aria-label=Войти]').click()");
    await until("!!document.querySelector('.reader-access button')");
  }],
];
const admin = [
  ['settings', '/api/access/session/login', '?settings=1', "!!document.querySelector('.settings-page')?.textContent.includes('Пользователи')"],
  ['github', '/api/admin/session/login', '?github=1', "!!document.querySelector('.github-page')?.textContent.includes('Подключить репозиторий')"],
];

fs.mkdirSync(outDir, { recursive: true });
await send('Page.enable');
await send('Network.enable');
await send('Fetch.enable', { patterns: [{ urlPattern: '*/api/*', requestStage: 'Response' }] });
await send('Emulation.setTimezoneOverride', { timezoneId: 'UTC' });
await send('Page.addScriptToEvaluateOnNewDocument', { source: FREEZE_DATE });
await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
await send('Page.addScriptToEvaluateOnNewDocument', { source: `document.addEventListener('DOMContentLoaded', () => { const s = document.createElement('style'); s.textContent = ${JSON.stringify(FREEZE_CSS)}; document.head.append(s); });` });

const capture = async (name, width) => {
  await settle();
  const { cssContentSize } = await send('Page.getLayoutMetrics');
  const contentHeight = Math.ceil(cssContentSize.height);
  if (contentHeight > MAX_HEIGHT) throw new Error(`${name}-${width}: page height ${contentHeight} exceeds MAX_HEIGHT ${MAX_HEIGHT}`);
  const height = Math.max(HEIGHT, contentHeight);
  const { data } = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip: { x: 0, y: 0, width, height, scale: 1 } });
  fs.writeFileSync(path.join(outDir, `${name}-${width}.png`), Buffer.from(data, 'base64'));
};

for (const width of WIDTHS) {
  await send('Emulation.setDeviceMetricsOverride', { width, height: HEIGHT, deviceScaleFactor: 1, mobile: width <= 390 });
  for (const [name, prepare, cleanup] of guest) {
    try { await prepare(); } catch (error) { throw Error(`${name}-${width}: ${error.message}`); }
    await capture(name, width);
    if (cleanup) await cleanup();
  }
  // Administration views need a reader session first, exactly as the smoke test signs in.
  await send('Page.navigate', { url: privateUrl + '?key=' + encodeURIComponent(readerKey) });
  await until("!new URL(location.href).searchParams.has('key') && !!document.querySelector('button[aria-label=Профиль]')");
  for (const [name, route, query, ready] of admin) {
    if (!await adminLogin(route)) throw Error('synthetic admin session failed');
    try { await go(privateUrl + query, ready); } catch (error) { console.error(await evaluate('location.href + " " + document.body.innerText.slice(0,400)')); throw Error(`${name}-${width}: ${error.message}`); }
    await capture(name, width);
    await evaluate("fetch(" + JSON.stringify(route.replace('login', 'logout')) + ", {method:'POST'}).then(() => 0)");
  }
  await evaluate("fetch('/api/session/logout', {method:'POST'}).then(() => 0)");
  console.log(JSON.stringify({ width, screens: guest.length + admin.length }));
}
ws.send(JSON.stringify({ id: ++id, method: 'Browser.close' }));
