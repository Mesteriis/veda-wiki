// Connect only to the temporary Chrome DevTools port and synthetic VEDA URLs.
import fs from 'node:fs';
import { pollCondition } from './ui_poll.mjs';
const [port, url, privateUrl, keyFile] = process.argv.slice(2);
const key = fs.readFileSync(keyFile, 'utf8');
let pages;
for (let i = 0; i < 1200; i++) {
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
  if (result.exceptionDetails) throw Object.assign(Error('page evaluation failed'), { pageException: result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'exception' });
  return result.result.value;
};
const until = expression => pollCondition(evaluate, expression);
await send('Page.enable');
for (const width of [1672, 390, 320]) {
  currentWidth = width;
  await send('Emulation.setDeviceMetricsOverride', { width, height: 941, deviceScaleFactor: 1, mobile: width <= 390 });
  await send('Page.navigate', { url });
  await until("!!document.querySelector('.home-page-row')");
  if (!await evaluate(`innerWidth === ${width} && document.documentElement.scrollWidth === ${width}`)) throw Error('layout overflow');
  if (width <= 390) {
    if (!await evaluate("(() => { const b=document.querySelector('button[aria-label=Меню]'), r=b.getBoundingClientRect(), hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2); return r.width > 0 && r.height > 0 && (hit === b || b.contains(hit)); })()")) throw Error('mobile menu button unreachable');
    for (const title of ['Граф знаний', 'Недавние']) {
      await evaluate("document.querySelector('button[aria-label=Меню]').click()");
      await until("document.querySelector('button[aria-label=Меню]').getAttribute('aria-expanded') === 'true'");
      if (!await evaluate(`(() => { const b=[...document.querySelectorAll('#main-navigation .nav-item')].find(e=>e.innerText===${JSON.stringify(title)}), r=b.getBoundingClientRect(), hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2); return r.width > 0 && r.height > 0 && (hit === b || b.contains(hit)); })()`)) throw Error('mobile navigation item unreachable');
      await evaluate(`[...document.querySelectorAll('#main-navigation .nav-item')].find(e=>e.innerText===${JSON.stringify(title)}).click()`);
      await until(title === 'Граф знаний' ? "!!document.querySelector('.graph-section')" : "!!document.querySelector('.recent-history')");
      await send('Page.navigate', { url });
      await until("!!document.querySelector('.home-page-row')");
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
  await until("!!document.querySelector('.home-page-row')");
  if (width === 1672) {
    await evaluate("(() => { const original=window.fetch.bind(window); window.__vedaConditionalPoll=false; window.fetch=async (...args) => { const response=await original(...args); if (String(args[0]).startsWith('/api/state') && args[1]?.headers?.['If-None-Match'] && response.status===304) window.__vedaConditionalPoll=true; return response; }; })()");
    await pollCondition(evaluate, 'window.__vedaConditionalPoll === true', 160);
    if (!await evaluate("!!document.querySelector('.home-page-row')")) throw Error('conditional poll lost the displayed state');
  }
  if (await evaluate("document.body.innerText.includes('Источники знаний')")) throw Error('integration panel visible anonymously');
  if (await evaluate("!!document.querySelector('.sidebar-bottom .storage, .sidebar-bottom .system')")) throw Error('sidebar status panels visible anonymously');
  if (await evaluate("!!document.querySelector('.shortcuts')")) throw Error('quick actions visible anonymously');
  if (await evaluate("!!document.querySelector('.activity')")) throw Error('activity visible anonymously');
  if (await evaluate("[...document.querySelectorAll('#main-navigation .nav-item')].some(e=>['GitHub','API и MCP','Настройки','Избранное'].includes(e.textContent.trim())) || !!document.querySelector('.reading-mode, button[aria-label=Уведомления], button[aria-label=Оформление]') || !document.querySelector('button[aria-label=Войти]')")) throw Error('anonymous navigation visibility incorrect');
  if (await evaluate("document.body.innerText.includes('Приватная тестовая страница')")) throw Error('private content visible anonymously');
  if (!await evaluate("(() => { const b=[...document.querySelectorAll('button[aria-label=Войти]')].find(e=>e.getBoundingClientRect().width>0), r=b.getBoundingClientRect(), hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2); return !!b && getComputedStyle(b).display !== 'none' && r.width > 0 && r.height > 0 && (hit === b || b.contains(hit)); })()")) throw Error('profile button is not visible or reachable: ' + await evaluate("(() => { const b=[...document.querySelectorAll('button[aria-label=Войти]')].find(e=>e.getBoundingClientRect().width>0), r=b.getBoundingClientRect(), hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2); if (!b) return 'no visible button'; return JSON.stringify({x:r.x,y:r.y,w:r.width,h:r.height,hit:hit && (hit.className||hit.tagName)}); })()"));
  for (const query of ['?settings=1', '?github=1']) {
    await send('Page.navigate', { url: privateUrl + query });
    await until("!!document.querySelector('.home-page-row')");
    if (await evaluate("!!document.querySelector('.settings-page, .github-page') || document.body.innerText.includes('Вход с ключом администратора')")) throw Error('admin view exposed anonymously');
  }
  await send('Page.navigate', { url: privateUrl });
  await until("!!document.querySelector('.home-page-row')");
  if (width === 1672) {
    await send('Page.navigate', { url: privateUrl + '?key=invalid-one&key=invalid-two&page=missing' });
    await until("!new URL(location.href).searchParams.has('key') && document.body.innerText.includes('Страница не найдена') && document.querySelector('[role=alert]')?.innerText.includes('Не удалось войти по ссылке')");
    await send('Page.navigate', { url: privateUrl });
    await until("!!document.querySelector('.home-page-row')");
  }
  await evaluate("document.querySelector('button[aria-label=Войти]').click()");
  await until("!!document.querySelector('.reader-access button')");
  if (!await evaluate("document.querySelector('dialog[open] h2')?.innerText === 'Вход'")) throw Error('login dialog title missing');
  if (await evaluate("document.querySelector('.reader-access').innerText.includes('Войти по ключу')")) throw Error('key login control still visible');
  await send('Page.navigate', { url: privateUrl + '?key=' + encodeURIComponent(key) + '&example=1' });
  await until("!new URL(location.href).searchParams.has('key') && new URL(location.href).searchParams.get('example') === '1'");
  await until("document.body.innerText.includes('Приватная тестовая страница')");
  if (await evaluate("document.body.innerText.includes('Недоступная тестовая страница') || document.body.innerText.includes('Недоступный пример')")) throw Error('scoped link session showed out-of-scope source');
  if (await evaluate("(async () => (await fetch('/api/v1/articles?source_id=other-private&path=secret.md')).status)()") !== 404) throw Error('scoped link session allowed out-of-scope article');
  if (!await evaluate("!!document.querySelector('.sidebar-bottom .storage') && !!document.querySelector('.sidebar-bottom .system')")) throw Error('sidebar status panels missing for authenticated reader');
  if (!await evaluate("['GitHub','API и MCP','Настройки','Избранное'].every(t=>[...document.querySelectorAll('#main-navigation .nav-item')].some(e=>e.textContent.trim()===t)) && !!document.querySelector('.reading-mode') && !!document.querySelector('button[aria-label=Уведомления]') && !!document.querySelector('button[aria-label=Оформление]')")) throw Error('authenticated navigation missing');
  await evaluate("document.querySelector('.home-page-row[href*=private]').click()");
  await until("document.body.innerText.includes('Закрытый синтетический текст')");
  if (await evaluate("!!document.querySelector('.conflict-warning')")) throw Error('clean article showed conflict warning');
  await send('Page.navigate', { url: privateUrl + '?page=conflict-fixture%2Fguide.md' });
  await until("document.body.innerText.includes('Локальный пример') && !!document.querySelector('.conflict-warning[role=status]')");
  if (!await evaluate("document.querySelector('.source-warning')?.innerText.includes('последний импорт:')")) throw Error('import observation missing from source warning');
  await evaluate("document.querySelector('button[aria-label=Профиль]').click()");
  await until("[...document.querySelectorAll('.reader-access button')].some(b => b.innerText === 'Выйти')");
  await evaluate("[...document.querySelectorAll('.reader-access button')].find(b => b.innerText === 'Выйти').click()");
  await until("!location.search && !!document.querySelector('.home-page-row') && !document.body.innerText.includes('Приватная тестовая страница')");
  if (await evaluate("(async () => (await (await fetch('/api/session/status')).json()).authenticated)()")) throw Error('key session survived logout');
  await evaluate("document.querySelector('button[aria-label=Войти]').click()");
  await until("!!document.querySelector('.reader-access input[name=username]')");
  await evaluate("document.querySelector('.reader-access input[name=username]').value='browser-user'; document.querySelector('.reader-access input[name=password]').value='synthetic fixture password'; document.querySelector('.reader-access form').requestSubmit()");
  await until("document.body.innerText.includes('Приватная тестовая страница')");
  if (await evaluate("document.body.innerText.includes('Недоступная тестовая страница') || document.body.innerText.includes('Недоступный пример')")) throw Error('user saw out-of-scope source');
  if (await evaluate("(async () => (await fetch('/api/v1/articles?source_id=other-private&path=secret.md')).status)()") !== 404) throw Error('out-of-scope article available');
  if (width === 1672) {
    await send('Page.navigate', { url: privateUrl + '?page=program-fixture%2Fdocs%2Fcurrent.md' });
    await until("document.querySelector('.markdown')?.innerText.includes('Read the guide.')");
    await send('Page.navigate', { url: privateUrl });
    await until("document.body.innerText.includes('Приватная тестовая страница')");
  }
  if (width !== 1672) {
    await evaluate("document.querySelector('button[aria-label=Профиль]').click()");
    await until("[...document.querySelectorAll('.reader-access button')].some(b => b.innerText === 'Выйти')");
    await evaluate("[...document.querySelectorAll('.reader-access button')].find(b => b.innerText === 'Выйти').click()");
    await until("!location.search && !!document.querySelector('.home-page-row') && !document.body.innerText.includes('Приватная тестовая страница')");
    if (await evaluate("(async () => (await (await fetch('/api/session/status')).json()).authenticated)()")) throw Error('user session survived logout');
    await evaluate("document.querySelector('button[aria-label=Войти]').click()");
    await until("!!document.querySelector('.reader-access input[name=username]')");
    await evaluate("document.querySelector('.reader-access input[name=username]').value='browser-user'; document.querySelector('.reader-access input[name=password]').value='synthetic fixture password'; document.querySelector('.reader-access form').requestSubmit()");
    await until("document.body.innerText.includes('Приватная тестовая страница')");
  }
  let issuedLink;
  for (const title of ['GitHub', 'Настройки']) {
    if (width <= 390) await evaluate("document.querySelector('button[aria-label=Меню]').click()");
    await evaluate(`[...document.querySelectorAll('#main-navigation .nav-item')].find(e=>e.innerText===${JSON.stringify(title)}).click()`);
    const scope = title === 'GitHub' ? '.github-page' : '.settings-page';
    try { await until(`!!document.querySelector(${JSON.stringify(scope + ' input[name=username]')})`); }
    catch (error) { throw Error(`admin login unavailable: ${title}, path=${await evaluate('location.pathname + location.search')}, page=${await evaluate("!!document.querySelector('.github-page')")}, dialog=${await evaluate("!!document.querySelector('dialog[open]')")}`); }
    if (await evaluate(`!!document.querySelector(${JSON.stringify(scope + ' input[name=key]')})`)) throw Error('admin key form is still visible');
    const sessionRoute = title === 'GitHub' ? '/api/admin/session/login' : '/api/access/session/login';
    if (!await evaluate(`(async () => (await fetch(${JSON.stringify(sessionRoute)}, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({key:${JSON.stringify('d'.repeat(64))}})})).ok)()`)) throw Error('synthetic admin session failed');
    await send('Page.navigate', { url: privateUrl + (title === 'GitHub' ? '?github=1' : '?settings=1') });
    const expected = title === 'GitHub' ? 'Подключить репозиторий' : 'Пользователи';
    await until(`!!document.querySelector(${JSON.stringify(scope)})?.textContent.includes(${JSON.stringify(expected)})`);
    if (title === 'GitHub' && !await evaluate("!!document.querySelector('.github-page input[name=source]') && !!document.querySelector('.github-page select[name=destination]') && document.querySelector('.github-page').textContent.includes('Фоновое согласование')")) throw Error('mirror configuration controls missing');
    if (title === 'GitHub') {
      await evaluate("[...document.querySelectorAll('.github-page-actions button')].find(b=>b.innerText==='Подключить репозиторий').click()");
      await until("!!document.querySelector('.github-action-dialog[open] input[name=repository]')");
      if (!await evaluate("(() => {const f=document.querySelector('.github-action-dialog input[name=repository]')?.form; return !!f && !f.querySelector('input[name=id]').required && f.textContent.includes('Идентификатор источника будет выбран автоматически');})()")) throw Error('automatic source ID unavailable');
      await evaluate("document.querySelector('.github-action-dialog button[aria-label=Закрыть]').click()");
      await until("!document.querySelector('.github-action-dialog[open]')");
    }
    if (title === 'Настройки') {
      await evaluate("[...document.querySelectorAll('.settings-tabs button')].find(b=>b.innerText==='Ключи доступа').click()");
      await evaluate("document.querySelector('.settings-add').click()");
      await until("!!document.querySelector('.settings-modal[open] input[name=days]')");
      await evaluate(`(() => { const form = document.querySelector('.settings-modal form'); form.querySelector('input[name=id]').value = 'browser-link-${width}'; form.querySelector('input[name=sources][value=fixture]').checked = true; form.requestSubmit(); })()`);
      await until("!!document.querySelector('.access-issued textarea[aria-label=\"Новый ключ чтения\"]') && !document.querySelector('.settings-modal[open]')");
      if (!await evaluate("(() => { const box=document.querySelector('.access-issued'); const key=box.querySelector('textarea[aria-label=\"Новый ключ чтения\"]').value; const link=new URL(box.querySelector('label textarea').value); return link.origin === location.origin && link.pathname === '/' && link.searchParams.get('key') === key && !!key; })()")) throw Error('issued link does not contain the new key');
      issuedLink = await evaluate("document.querySelector('.access-issued label textarea').value");
      await evaluate("document.querySelector('.access-issued button').click()");
      await until("!document.querySelector('.access-issued')");
      if (width === 1672) {
        for (const intermediate of [1201, 1024, 800]) {
          await send('Emulation.setDeviceMetricsOverride', { width: intermediate, height: 941, deviceScaleFactor: 1, mobile: false });
          await until(`innerWidth === ${intermediate}`);
          if (!await evaluate(`document.documentElement.scrollWidth === ${intermediate}`)) throw Error('settings intermediate viewport overflow');
        }
        await send('Emulation.setDeviceMetricsOverride', { width, height: 941, deviceScaleFactor: 1, mobile: false });
        await until(`innerWidth === ${width}`);
      }
    }
    const writeStatus = await evaluate("(async () => (await fetch('/api/v1/articles',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({source_id:'fixture',path:'browser-write.md',text:'# Synthetic',expected_revision:0})})).status)()");
    if (writeStatus !== 403 && writeStatus !== 404) throw Error('browser admin session allowed article write');
    await until(`[...document.querySelectorAll(${JSON.stringify(scope + ' button')})].some(b => b.innerText.startsWith('Выйти из управления') && !b.disabled)`);
    await evaluate(`[...document.querySelectorAll(${JSON.stringify(scope + ' button')})].find(b=>b.innerText.startsWith('Выйти из управления')).click()`);
    await until(`!!document.querySelector(${JSON.stringify(scope + ' input[name=username]')})`);
    await send('Page.navigate', { url: privateUrl });
    await until("!!document.querySelector('.home-page-row')");
  }
  if (width === 1672) {
    await send('Page.navigate', { url: issuedLink });
    await until("!new URL(location.href).searchParams.has('key') && !!document.querySelector('.home-page-row')");
    if (await evaluate("(async () => (await fetch('/api/v1/articles?source_id=private-fixture&path=private.md')).status)()") !== 404) throw Error('issued link allowed out-of-scope article');
    if (!await evaluate("(async () => (await (await fetch('/api/session/status')).json()).authenticated)()")) throw Error('issued link did not create a session');
  }
  await evaluate("document.querySelector('button[aria-label=Профиль]').click()");
  await until("[...document.querySelectorAll('.reader-access button')].some(b => b.innerText === 'Выйти')");
  await evaluate("[...document.querySelectorAll('.reader-access button')].find(b => b.innerText === 'Выйти').click()");
  await until("!location.search && !!document.querySelector('.home-page-row') && !document.body.innerText.includes('Приватная тестовая страница')");
  if (width === 1672) {
    await until("!!document.querySelector('button[aria-label=Войти]')");
    await evaluate("document.querySelector('button[aria-label=Войти]').click()");
    await until("!!document.querySelector('.reader-access input[name=username]')");
    await evaluate("document.querySelector('.reader-access input[name=username]').value='browser-admin'; document.querySelector('.reader-access input[name=password]').value='synthetic admin password'; document.querySelector('.reader-access form').requestSubmit()");
    await until("!!document.querySelector('button[aria-label=Профиль]')");
    for (const [query, scope] of [['?github=1', '.github-page'], ['?settings=1', '.settings-page']]) {
      await send('Page.navigate', { url: privateUrl + query });
      await until(`!!document.querySelector(${JSON.stringify(scope)})?.textContent.includes('Вход выполнен через учётную запись')`);
      if (await evaluate(`!!document.querySelector(${JSON.stringify(scope)} + ' input[name=key]')`)) throw Error('admin key form visible');
    }
    await send('Page.navigate', { url: privateUrl });
    await until("!!document.querySelector('button[aria-label=Профиль]')");
    await evaluate("document.querySelector('button[aria-label=Профиль]').click()");
    await until("[...document.querySelectorAll('.reader-access button')].some(b => b.innerText === 'Выйти')");
    await evaluate("[...document.querySelectorAll('.reader-access button')].find(b => b.innerText === 'Выйти').click()");
    await until("!!document.querySelector('button[aria-label=Войти]')");
  }
  console.log(JSON.stringify({ width, article: true, search: true, collidingSources: true, resultLink: true, keyLoginAndLogout: true, userLoginAndScope: true, adminSections: true, namedAdminLogin: width === 1672, browserWriteDenied: true, mobileNavigation: width <= 390, noHorizontalOverflow: true }));
}
// Let Chrome close its profile and children before the temporary directory is removed.
ws.send(JSON.stringify({ id: ++id, method: 'Browser.close' }));
