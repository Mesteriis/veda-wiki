// Connect only to the temporary Chrome DevTools port and synthetic VEDA URLs.
import fs from 'node:fs';
import { pollCondition } from './ui_poll.mjs';
const [port, url, privateUrl, keyFile] = process.argv.slice(2);
const key = fs.readFileSync(keyFile, 'utf8');
let pages;
for (let i = 0; i < 400; i++) {
  try {
    pages = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
    break;
  } catch {
    await new Promise(resolve => setTimeout(resolve, 50));
  }
}
if (!pages) throw Error('debugger unavailable');
const ws = new WebSocket(pages.find(page => page.type === 'page').webSocketDebuggerUrl);
await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
let id = 0;
let currentWidth = 0;
const pending = new Map();
ws.onmessage = event => {
  const message = JSON.parse(event.data);
  if (message.id && pending.has(message.id)) {
    const call = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) {
      const code = Number.isInteger(message.error.code) ? message.error.code : 'unknown';
      const known = ['Cannot find context with specified id', 'Inspected target navigated or closed'];
      const reason = known.includes(message.error.message) ? message.error.message : 'other';
      const error = Error(`CDP error: method=${call.method} width=${currentWidth} code=${code} reason=${reason}`);
      error.contextChanged = call.method === 'Runtime.evaluate' && code === -32000 && known.includes(message.error.message);
      call.reject(error);
    } else call.resolve(message.result);
  }
};
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const next = ++id;
  pending.set(next, { resolve, reject, method });
  ws.send(JSON.stringify({ id: next, method, params }));
});
const evaluate = async expression => {
  const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw Error('page evaluation failed');
  return result.result.value;
};
const until = expression => pollCondition(evaluate, expression);
await send('Page.enable');
for (const width of [1672, 390, 320]) {
  currentWidth = width;
  await send('Emulation.setDeviceMetricsOverride', { width, height: 941, deviceScaleFactor: 1, mobile: width <= 390 });
  await send('Page.navigate', { url });
  await until("!!document.querySelector('.page-card')");
  if (!await evaluate(`innerWidth === ${width} && document.documentElement.scrollWidth === ${width}`)) throw Error('layout overflow');
  if (width <= 390) {
    if (!await evaluate("(() => { const b=document.querySelector('button[aria-label=Меню]'), r=b.getBoundingClientRect(), hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2); return r.width > 0 && r.height > 0 && (hit === b || b.contains(hit)); })()")) throw Error('mobile menu button unreachable');
    for (const title of ['GitHub', 'Настройки']) {
      await evaluate("document.querySelector('button[aria-label=Меню]').click()");
      await until("document.querySelector('button[aria-label=Меню]').getAttribute('aria-expanded') === 'true'");
      if (!await evaluate(`(() => { const b=[...document.querySelectorAll('#main-navigation .nav-item')].find(e=>e.innerText===${JSON.stringify(title)}), r=b.getBoundingClientRect(), hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2); return r.width > 0 && r.height > 0 && (hit === b || b.contains(hit)); })()`)) throw Error('mobile navigation item unreachable');
      await evaluate(`[...document.querySelectorAll('#main-navigation .nav-item')].find(e=>e.innerText===${JSON.stringify(title)}).click()`);
      await until(`!!document.querySelector('dialog[open]') && document.querySelector('dialog h2').innerText===${JSON.stringify(title)}`);
      await evaluate("document.querySelector('dialog button[aria-label=Закрыть]').click()");
      await until("!document.querySelector('dialog[open]') && document.querySelector('button[aria-label=Меню]').getAttribute('aria-expanded') === 'false'");
    }
  }
  await until("!!document.querySelector('.knowledge-tree summary span[title=projects]')");
  await evaluate("document.querySelector('.knowledge-tree summary span[title=projects]').closest('summary').click()");
  if (!await evaluate("(() => { const a=[...document.querySelectorAll('.knowledge-tree details[open] a')].map(x=>decodeURIComponent(x.getAttribute('href'))); return a.some(x=>x.includes('fixture/projects/architecture.md')) && a.some(x=>x.includes('mirror/projects/architecture.md')); })()")) throw Error('merged tree lost colliding source paths');
  await evaluate("document.querySelector('.knowledge-tree a[title=\"mirror/projects/architecture.md\"]').click()");
  await until("location.search.includes('page=') && document.body.innerText.includes('Архитектура второго источника')");
  await evaluate("location.assign('/')");
  await until("!!document.querySelector('.knowledge-tree summary span[title=projects]')");
  await evaluate("document.querySelector('.knowledge-tree summary span[title=projects]').closest('summary').click()");
  await evaluate("document.querySelector('.knowledge-tree a[title=\"fixture/projects/architecture.md\"]').click()");
  await until("location.search.includes('page=') && document.body.innerText.includes('Демонстрационное содержимое')");
  if (!await evaluate("document.body.innerText.includes('Тестовая архитектура')")) throw Error('article title missing');
  await evaluate("[...document.querySelectorAll('.markdown a')].find(a => a.innerText === 'Документация проекта').click()");
  await until("document.body.innerText.includes('Синтетическая страница раздела')");
  await evaluate("history.back()");
  await until("document.body.innerText.includes('Тестовая архитектура') && !!document.querySelector('.markdown')");
  await evaluate("let input=document.querySelector('#search'); input.value='архитектура'; input.form.requestSubmit()");
  await until("location.search.includes('q=') && document.body.innerText.includes('Результаты поиска')");
  if (!await evaluate("document.body.innerText.includes('Тестовая архитектура') && document.body.innerText.includes('Архитектура второго источника') && !document.body.innerText.includes('Тестовые заметки')")) throw Error('search results mismatch');
  if (!await evaluate("(() => { const a=[...document.querySelectorAll('.search-results a')].map(x=>decodeURIComponent(x.getAttribute('href'))); return a.some(x=>x.includes('fixture/projects/architecture.md')) && a.some(x=>x.includes('mirror/projects/architecture.md')); })()")) throw Error('search merged colliding articles');
  await evaluate("document.querySelector('.search-results a[href*=architecture]').click()");
  await until("location.search.includes('page=') && document.body.innerText.includes('Демонстрационное содержимое')");
  await send('Page.navigate', { url: privateUrl });
  await until("!!document.querySelector('.page-card')");
  if (await evaluate("document.body.innerText.includes('Приватная тестовая страница')")) throw Error('private content visible anonymously');
  if (!await evaluate("(() => { const b=document.querySelector('button[aria-label=Профиль]'), r=b.getBoundingClientRect(), hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2); return getComputedStyle(b).display !== 'none' && r.width > 0 && r.height > 0 && (hit === b || b.contains(hit)); })()")) throw Error('profile button is not visible or reachable');
  await evaluate("document.querySelector('button[aria-label=Профиль]').click()");
  await until("!!document.querySelector('.reader-access button')");
  await evaluate("[...document.querySelectorAll('.reader-access button')].find(b => b.innerText === 'Войти по ключу').click()");
  await until("!!document.querySelector('.reader-access input[name=key]')");
  await evaluate(`document.querySelector('.reader-access input[name=key]').value = ${JSON.stringify(key)}; document.querySelector('.reader-access form').requestSubmit()`);
  await until("document.body.innerText.includes('Приватная тестовая страница')");
  await evaluate("document.querySelector('.page-card[href*=private]').click()");
  await until("document.body.innerText.includes('Закрытый синтетический текст')");
  if (await evaluate("!!document.querySelector('.conflict-warning')")) throw Error('clean article showed conflict warning');
  await send('Page.navigate', { url: privateUrl + '?page=conflict-fixture%2Fguide.md' });
  await until("document.body.innerText.includes('Локальный пример') && !!document.querySelector('.conflict-warning[role=status]')");
  if (!await evaluate("document.querySelector('.source-warning')?.innerText.includes('последний импорт:')")) throw Error('import observation missing from source warning');
  await evaluate("document.querySelector('button[aria-label=Профиль]').click()");
  await until("[...document.querySelectorAll('.reader-access button')].some(b => b.innerText === 'Выйти')");
  await evaluate("[...document.querySelectorAll('.reader-access button')].find(b => b.innerText === 'Выйти').click()");
  await until("!location.search && !!document.querySelector('.page-card') && !document.body.innerText.includes('Приватная тестовая страница')");
  if (await evaluate("(async () => (await (await fetch('/api/session/status')).json()).authenticated)()")) throw Error('key session survived logout');
  await evaluate("document.querySelector('button[aria-label=Профиль]').click()");
  await until("!!document.querySelector('.reader-access input[name=username]')");
  await evaluate("document.querySelector('.reader-access input[name=username]').value='browser-user'; document.querySelector('.reader-access input[name=password]').value='synthetic fixture password'; document.querySelector('.reader-access form').requestSubmit()");
  await until("document.body.innerText.includes('Приватная тестовая страница')");
  if (await evaluate("document.body.innerText.includes('Недоступная тестовая страница') || document.body.innerText.includes('Недоступный пример')")) throw Error('user saw out-of-scope source');
  if (await evaluate("(async () => (await fetch('/api/v1/articles?source_id=other-private&path=secret.md')).status)()") !== 404) throw Error('out-of-scope article available');
  await evaluate("document.querySelector('button[aria-label=Профиль]').click()");
  await until("[...document.querySelectorAll('.reader-access button')].some(b => b.innerText === 'Выйти')");
  await evaluate("[...document.querySelectorAll('.reader-access button')].find(b => b.innerText === 'Выйти').click()");
  await until("!location.search && !!document.querySelector('.page-card') && !document.body.innerText.includes('Приватная тестовая страница')");
  if (await evaluate("(async () => (await (await fetch('/api/session/status')).json()).authenticated)()")) throw Error('user session survived logout');
  for (const title of ['GitHub', 'Настройки']) {
    if (width <= 390) await evaluate("document.querySelector('button[aria-label=Меню]').click()");
    await evaluate(`[...document.querySelectorAll('#main-navigation .nav-item')].find(e=>e.innerText===${JSON.stringify(title)}).click()`);
    await until("!!document.querySelector('dialog[open] .reader-access input[name=key]')");
    await evaluate(`document.querySelector('dialog input[name=key]').value=${JSON.stringify('d'.repeat(64))}; document.querySelector('dialog .reader-access form').requestSubmit()`);
    const expected = title === 'GitHub' ? 'Подключить репозиторий' : 'Новый ключ чтения';
    await until(`document.querySelector('dialog').innerText.includes(${JSON.stringify(expected)})`);
    const writeStatus = await evaluate("(async () => (await fetch('/api/v1/articles',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({source_id:'fixture',path:'browser-write.md',text:'# Synthetic',expected_revision:0})})).status)()");
    if (writeStatus !== 403 && writeStatus !== 404) throw Error('browser admin session allowed article write');
    await until("[...document.querySelectorAll('dialog button')].some(b => b.innerText.startsWith('Выйти из управления') && !b.disabled)");
    await evaluate("[...document.querySelectorAll('dialog button')].find(b=>b.innerText.startsWith('Выйти из управления')).click()");
    await until("!!document.querySelector('dialog .reader-access input[name=key]')");
    await evaluate("document.querySelector('dialog button[aria-label=Закрыть]').click()");
    await until("!document.querySelector('dialog[open]')");
  }
  console.log(JSON.stringify({ width, article: true, search: true, collidingSources: true, resultLink: true, keyLoginAndLogout: true, userLoginAndScope: true, adminSections: true, browserWriteDenied: true, mobileNavigation: width <= 390, noHorizontalOverflow: true }));
}
// Let Chrome close its profile and children before the temporary directory is removed.
ws.send(JSON.stringify({ id: ++id, method: 'Browser.close' }));
