import { useState } from "react";
import { clients, defaultEditor, defaultReader, registerCommand, remoteCommand, serverCommand, type AccessMode, type AgentClient, type EditorSettings, type ReaderSettings } from "../../lib/mcpCommands";

const storageKey = "veda.mcp.connect";

interface Saved { client: AgentClient; mode: AccessMode; reader: ReaderSettings; editor: EditorSettings }

// Paths and hosts the owner typed are kept only in this browser.
function load(): Saved {
  const fallback: Saved = { client: "claude", mode: "remote", reader: defaultReader, editor: defaultEditor };
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) || "null") as Partial<Saved> | null;
    return saved ? { ...fallback, ...saved, reader: { ...defaultReader, ...saved.reader }, editor: { ...defaultEditor, ...saved.editor } } : fallback;
  } catch { return fallback; }
}

function store(value: Saved) {
  try { localStorage.setItem(storageKey, JSON.stringify(value)); } catch { /* private mode: keep in memory only */ }
}

export function ConnectTab({ onKeys }: { onKeys: () => void }) {
  const [saved, setSaved] = useState(load);
  const [copied, setCopied] = useState(false);
  const update = (change: Partial<Saved>) => { const next = { ...saved, ...change }; setSaved(next); store(next); setCopied(false); };
  const origin = window.location.origin;
  const command = saved.mode === "remote"
    ? remoteCommand(saved.client, origin).join("\n")
    : registerCommand(saved.client, serverCommand(saved.mode, origin, saved.reader, saved.editor));
  async function copy() {
    try { await navigator.clipboard.writeText(command); setCopied(true); } catch { setCopied(false); }
  }
  const readerField = (name: keyof ReaderSettings, label: string, hint: string) =>
    <label>{label}<input value={saved.reader[name]} placeholder={hint} onChange={(e) => update({ reader: { ...saved.reader, [name]: e.target.value } })} /></label>;
  const editorField = (name: keyof EditorSettings, label: string, hint: string) =>
    <label>{label}<input value={saved.editor[name]} placeholder={hint} onChange={(e) => update({ editor: { ...saved.editor, [name]: e.target.value } })} /></label>;
  return <div className="mcp-connect">
    <div className="mcp-choice" role="group" aria-label="Клиент">{clients.map(([id, label]) =>
      <button type="button" key={id} aria-pressed={saved.client === id} onClick={() => update({ client: id })}>{label}</button>)}</div>
    <div className="mcp-choice" role="group" aria-label="Доступ">
      <button type="button" aria-pressed={saved.mode === "remote"} onClick={() => update({ mode: "remote" })}>По адресу и ключу</button>
      <button type="button" aria-pressed={saved.mode === "reader"} onClick={() => update({ mode: "reader" })}>Через локальный veda</button>
      <button type="button" aria-pressed={saved.mode === "editor"} onClick={() => update({ mode: "editor" })}>Редактор через SSH</button>
    </div>
    {saved.mode === "remote" && <>
      <p>Для любого человека: ничего ставить не нужно, только клиент и ключ. Агент подключается к <code>{origin}/mcp</code>, видит инструменты чтения и только источники своего ключа. Через сеть записать ничего нельзя. <button type="button" className="link-button" onClick={onKeys}>Создать ключ чтения</button></p>
      <p className="mcp-note">Вместо <code>{"<ключ>"}</code> подставьте выданный ключ.{saved.client === "hermes" && " Hermes спросит заголовок: введите Authorization: Bearer <ключ> (вид запроса может отличаться в вашей версии)."}</p>
    </>}
    {saved.mode === "reader" && <>
      <p>Работает с любой машины, где есть бинарник <code>veda</code>: агент читает выбранные источники по ключу через HTTPS этого сайта и не может ничего записать. <button type="button" className="link-button" onClick={onKeys}>Создать ключ чтения</button></p>
      <div className="mcp-fields">{readerField("veda", "Команда veda на машине агента", "veda")}{readerField("keyFile", "Файл ключа чтения", "~/.config/veda/reader.key")}</div>
    </>}
    {saved.mode === "editor" && <>
      <p>Только для владельца, в его локальной среде. Агент читает всё и обновляет статьи через <code>veda_write_article</code>; VEDA сама отправляет правки в репозиторий. Источники, GitHub, ключи и переиндексация недоступны. Ключ владельца остаётся на сервере и в браузере не показывается — команда только ссылается на его файл.</p>
      <div className="mcp-fields">
        {editorField("host", "SSH-хост сервера", "например, pve")}
        {editorField("prefix", "Вход в окружение сервиса (необязательно)", "например, pct exec 161 -- runuser -u veda --")}
        {editorField("veda", "Команда veda на сервере", "/opt/veda/veda")}
        {editorField("port", "Порт владельца на loopback", "8080")}
        {editorField("keyFile", "Файл ключа владельца на сервере", "/var/lib/veda/owner.key")}
      </div>
    </>}
    <div className="mcp-command"><pre><code>{command}</code></pre><button type="button" onClick={() => { void copy(); }}>{copied ? "Скопировано" : "Скопировать"}</button></div>
    <p className="mcp-note">После добавления перезапустите клиент или переподключите сервер MCP: уже открытые сессии продолжают работать в прежнем режиме. Значения полей хранятся только в этом браузере.</p>
  </div>;
}
