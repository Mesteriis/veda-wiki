// Connect only to the temporary Chrome DevTools port and synthetic VEDA URLs.
const [port, url, repo] = process.argv.slice(2);
const sourceId = (`github-${repo.toLowerCase().replace(/[^a-z0-9_-]/g, '-')}`).slice(0, 64).replace(/-+$/, '');
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
  const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
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
await send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false });
await send('Page.navigate', {url});
await until("!!document.querySelector('button[aria-label=Войти]')");
await evaluate("document.querySelector('button[aria-label=Войти]').click()");
await until("!!document.querySelector('.reader-access button')");
await evaluate("[...document.querySelectorAll('.reader-access button')].find(b=>b.innerText==='Войти по ключу').click()");
await until("!!document.querySelector('.reader-access input[name=key]')");
await evaluate(`document.querySelector('.reader-access input[name=key]').value=${JSON.stringify('e'.repeat(64))};document.querySelector('.reader-access form').requestSubmit()`);
await until("!![...document.querySelectorAll('#main-navigation .nav-item')].find(e=>e.innerText==='GitHub')");
await evaluate("[...document.querySelectorAll('#main-navigation .nav-item')].find(e=>e.innerText==='GitHub').click()");
await until("!!document.querySelector('dialog[open] .github-connect input[name=key]')");
await evaluate(`document.querySelector('dialog input[name=key]').value=${JSON.stringify('d'.repeat(64))};document.querySelector('dialog input[name=key]').closest('form').requestSubmit()`);
await until("!!document.querySelector('dialog input[name=repository]')");
await evaluate(`(() => {const r=document.querySelector('dialog input[name=repository]'), f=r.form; if(f.querySelector('input[name=write]').checked) throw Error('write unexpectedly selected'); r.value=${JSON.stringify(repo)}; f.requestSubmit();})()`);
await until("document.querySelector('dialog').innerText.includes('Репозиторий подключён')");
let ready=false;
for(let i=0;i<180;i++) {
  if(await evaluate("document.querySelector('dialog').innerText.includes('Синхронизирован')")) {ready=true;break;}
  if(await evaluate("document.querySelector('dialog').innerText.includes('Нет папки docs/')")) throw Error('program documentation missing');
  await new Promise(resolve=>setTimeout(resolve,500));
}
if(!ready) throw Error('GitHub source did not become ready');
const connection=await evaluate(`(async () => {const c=await (await fetch('/api/admin/github/connections')).json(); const v=c.find(x=>x.id===${JSON.stringify(sourceId)}); return v ? {write:v.write,account:v.account_id || ''} : null;})()`);
if(!connection || connection.write !== false || connection.account !== '') throw Error('connection is not anonymous read-only');
const result=await evaluate(`(async () => { const s=await (await fetch('/api/v1/sources')).json(); const p=await (await fetch('/api/v1/articles')).json(); const id=${JSON.stringify(sourceId)}; return {source:s.find(x=>x.id===id)?.kind, count:p.length, docsOnly:p.every(x=>x.environment===id && x.path.startsWith(id+'/docs/'))}; })()`);
if(result.source!=='program' || !result.count || !result.docsOnly) throw Error('program import mismatch');
await evaluate("document.querySelector('dialog button[aria-label=Закрыть]').click()");
await evaluate("location.assign('/?env=all')");
await until("document.body.innerText.includes('Все страницы') && !!document.querySelector('.search-results a')");
console.log(JSON.stringify({status:'passed',browserConnect:true,publicProgram:true,pages:result.count,docsOnly:true,writeEnabled:false}));
ws.send(JSON.stringify({id:++id,method:'Browser.close'}));
