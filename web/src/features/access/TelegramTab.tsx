import { useEffect, useState, type FormEvent } from "react";
import { removeTelegramToken, sendTelegramTest, setTelegramEvent, setTelegramToken, telegramStatus, type NotifyEvent, type TelegramStatus } from "../../api/notify";
import { field } from "../../lib/forms";

const eventNames: [NotifyEvent, string][] = [
  ["requests", "Запросы доступа к API/MCP"],
  ["sync", "Ошибки синхронизации GitHub и их исправление"],
  ["keys", "Ключи доступа, истекающие в течение суток"],
];

// The outbound-only Telegram bot that notifies administrators.
export function TelegramTab({ busy, setBusy }: { busy: boolean; setBusy: (busy: boolean) => void }) {
  const [status, setStatus] = useState<TelegramStatus | null>(null);
  const [message, setMessage] = useState("");
  const refresh = () => telegramStatus().then(setStatus).catch(() => setMessage("Не удалось загрузить настройки Telegram."));
  useEffect(() => { void refresh(); }, []);
  async function run(action: () => Promise<unknown>, done: string, failed: string) {
    setBusy(true); setMessage("");
    try { await action(); setMessage(done); await refresh(); }
    catch { setMessage(failed); }
    finally { setBusy(false); }
  }
  function saveToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const token = field(new FormData(form), "token");
    form.reset();
    void run(() => setTelegramToken(token), "Бот подключён.", "Telegram не принял токен. Проверьте его у @BotFather.");
  }
  async function test() {
    setBusy(true); setMessage("");
    try { const { delivered } = await sendTelegramTest(); setMessage(`Пробное сообщение доставлено: ${delivered}.`); }
    catch { setMessage("Не доставлено. Нужен администратор с Telegram ID в профиле, который нажал у бота «Start»."); }
    finally { setBusy(false); }
  }
  if (!status) return <p>{message || "Загружаем…"}</p>;
  return <div className="telegram-tab">
    {status.configured ? <div className="telegram-bot">
      <p>Бот <strong>@{status.bot}</strong> подключён. Он только отправляет сообщения и ничего не принимает.</p>
      <div className="settings-row-actions">
        <button disabled={busy} onClick={() => { void test(); }}>Отправить пробное</button>
        <button disabled={busy} onClick={() => { if (window.confirm("Отключить бота? Уведомления перестанут приходить.")) void run(removeTelegramToken, "Бот отключён.", "Не удалось отключить бота."); }}>Отключить</button>
      </div>
    </div> : <form onSubmit={saveToken} className="telegram-token">
      <p>Создайте бота у @BotFather и вставьте его токен. Токен хранится на сервере и больше не показывается.</p>
      <label>Токен бота<input name="token" type="password" autoComplete="off" required /></label>
      <button disabled={busy}>Подключить</button>
    </form>}
    {status.error && <p role="alert">Бот настроен, но Telegram сейчас недоступен или отклонил токен.</p>}
    <fieldset className="telegram-events" disabled={busy}><legend>Уведомлять о</legend>{eventNames.map(([event, label]) =>
      <label key={event}><input type="checkbox" checked={status.events[event]} onChange={(e) => { void run(() => setTelegramEvent(event, e.target.checked), "Сохранено.", "Не удалось сохранить."); }} />{label}</label>)}</fieldset>
    <p className="settings-note">Получатели — администраторы, указавшие Telegram ID в профиле. Бот может написать только тем, кто нажал у него «Start».</p>
    {message && <p role="status">{message}</p>}
  </div>;
}
