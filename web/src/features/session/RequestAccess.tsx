import { useRef, useState, type FormEvent } from "react";
import { statusOf } from "../../api/http";
import { requestAccess } from "../../api/notify";
import type { Group } from "../../api/types";
import { I } from "../../components/Icon";
import { field, fields } from "../../lib/forms";

const nickPattern = /^@[A-Za-z0-9_]{5,32}$/;

// A guest asks for an API/MCP key to public sections; the owner is notified.
export function RequestAccess({ groups, className = "" }: { groups: Group[]; className?: string }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [message, setMessage] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
  function open() { setMessage(""); setSent(false); dialog.current?.showModal(); }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget; const data = new FormData(form);
    const sources = fields(data, "sources");
    let telegram = field(data, "telegram").trim();
    if (telegram && !telegram.startsWith("@")) telegram = "@" + telegram;
    if (!sources.length) { setMessage("Выберите хотя бы один раздел."); return; }
    if (!nickPattern.test(telegram)) { setMessage("Укажите ник в Telegram: от 5 до 32 латинских букв, цифр или «_»."); return; }
    setBusy(true); setMessage("");
    try {
      await requestAccess(sources, telegram, field(data, "comment"), field(data, "veda_trap_field"));
      form.reset(); setSent(true);
    } catch (error) {
      setMessage(statusOf(error) === 429 ? "Слишком много запросов. Попробуйте позже." : "Не удалось отправить запрос. Проверьте поля и попробуйте ещё раз.");
    } finally { setBusy(false); }
  }
  return <>
    <button type="button" className={"nav-item request-access " + className} onClick={open}><I name="key" /><span>Запросить доступ к API/MCP</span></button>
    <dialog ref={dialog} className="request-dialog" aria-labelledby="request-title">
      <div className="dialog-head"><h2 id="request-title">Доступ к API и MCP</h2><button type="button" aria-label="Закрыть" onClick={() => dialog.current?.close()}><I name="x" /></button></div>
      {sent ? <><p>Запрос отправлен владельцу. Ключ вам передадут в Telegram, если доступ одобрят.</p><button type="button" onClick={() => dialog.current?.close()}>Готово</button></> :
        <form onSubmit={submit}>
          <p>Ключ даёт агенту только чтение выбранных разделов через MCP или REST API.</p>
          <fieldset><legend>Разделы</legend>{groups.length ? groups.map((group) =>
            <label key={group.ID}><input type="checkbox" name="sources" value={group.ID} />{group.Name}</label>) : <p>Публичных разделов пока нет.</p>}</fieldset>
          <label>Ник в Telegram<input name="telegram" placeholder="@username" autoComplete="off" required maxLength={33} /></label>
          <label>Комментарий<textarea name="comment" maxLength={1000} rows={3} placeholder="Зачем нужен доступ" /></label>
          <label className="request-trap">Не заполняйте это поле<input name="veda_trap_field" tabIndex={-1} autoComplete="new-password" /></label>
          <button disabled={busy || !groups.length}>Отправить запрос</button>
          {message && <p role="alert">{message}</p>}
        </form>}
    </dialog>
  </>;
}
