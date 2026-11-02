// Connect only to the temporary Chrome DevTools port and synthetic VEDA URLs.
import fs from 'node:fs';
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
const pending = new Map();
ws.onmessage = event => {
  const message = JSON.parse(event.data);
  if (message.id && pending.has(message.id)) {
    const call = pending.get(message.id);
    pending.delete(message.id);
    message.error ? call.reject(Error('CDP error')) : call.resolve(message.result);
  }
};
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const next = ++id;
  pending.set(next, { resolve, reject });
  ws.send(JSON.stringify({ id: next, method, params }));
});
const evaluate = async expression => {
  const result = await send('Runtime.evaluate', { expression, returnByValue: true });
  if (result.exceptionDetails) throw Error('page evaluation failed');
  return result.result.value;
};
const until = async expression => {
  for (let i = 0; i < 80; i++) {
    if (await evaluate(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw Error('page condition timed out');
};
await send('Page.enable');
for (const width of [1672, 390]) {
  await send('Emulation.setDeviceMetricsOverride', { width, height: 941, deviceScaleFactor: 1, mobile: width === 390 });
  await send('Page.navigate', { url });
  await until("!!document.querySelector('.page-card')");
  if (!await evaluate(`innerWidth === ${width} && document.documentElement.scrollWidth === ${width}`)) throw Error('layout overflow');
  await evaluate("document.querySelector('.page-card[href*=architecture]').click()");
  await until("location.search.includes('page=') && document.body.innerText.includes('Демонстрационное содержимое')");
  if (!await evaluate("document.body.innerText.includes('Тестовая архитектура')")) throw Error('article title missing');
  await evaluate("let input=document.querySelector('#search'); input.value='архитектура'; input.form.requestSubmit()");
  await until("location.search.includes('q=') && document.body.innerText.includes('Результаты поиска')");
  if (!await evaluate("document.body.innerText.includes('Тестовая архитектура') && !document.body.innerText.includes('Тестовые заметки')")) throw Error('search results mismatch');
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
  await evaluate("document.querySelector('button[aria-label=Профиль]').click()");
  await until("[...document.querySelectorAll('.reader-access button')].some(b => b.innerText === 'Выйти')");
  await evaluate("[...document.querySelectorAll('.reader-access button')].find(b => b.innerText === 'Выйти').click()");
  await until("!location.search && !!document.querySelector('.page-card')");
  if (await evaluate("document.body.innerText.includes('Приватная тестовая страница')")) throw Error('private content visible after logout');
  console.log(JSON.stringify({ width, article: true, search: true, resultLink: true, privateLoginAndLogout: true, noHorizontalOverflow: true }));
}
ws.close();
