import { useState, type FormEvent } from "react";
import { setOwnTelegramID } from "../../api/notify";
import { field } from "../../lib/forms";

// The user's own Telegram ID; administrators who set it receive notifications.
export function TelegramIDForm({ initial }: { initial: number }) {
  const [saved, setSaved] = useState(initial);
  const [message, setMessage] = useState("");
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const raw = field(new FormData(event.currentTarget), "telegram_id").trim();
    const id = raw === "" ? 0 : Number(raw);
    if (!Number.isSafeInteger(id) || id < 0) { setMessage("Telegram ID — это число."); return; }
    try { await setOwnTelegramID(id); setSaved(id); setMessage(id ? "Сохранено." : "Telegram ID удалён."); }
    catch { setMessage("Не удалось сохранить Telegram ID."); }
  }
  return <form onSubmit={submit} className="telegram-id">
    <h3>Telegram</h3>
    <label>Telegram ID<input name="telegram_id" inputMode="numeric" defaultValue={saved || ""} placeholder="например, 123456789" /></label>
    <p>Уведомления VEDA получают только администраторы. Свой ID можно узнать у бота @userinfobot; затем нажмите «Start» у бота VEDA, иначе он не сможет написать.</p>
    <button>Сохранить</button>
    {message && <p role="status">{message}</p>}
  </form>;
}
