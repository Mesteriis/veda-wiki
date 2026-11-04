import { useEffect, useState, type FormEvent } from "react";
import { changePassword as postPassword, login, logout as postLogout, sessionStatus } from "../../api/session";
import { clearVisits } from "../../lib/recent";
import { passwordChangeAllowed } from "./passwordChange";

function field(data: FormData, name: string): string {
  const value = data.get(name);
  return typeof value === "string" ? value : "";
}

const isAbort = (error: unknown) => typeof error === "object" && error !== null && "name" in error && error.name === "AbortError";

// Reader sign-in, password change and sign-out in the profile dialog.
export function ReaderAccess() {
  const [status, setStatus] = useState<boolean | null>(null);
  const [message, setMessage] = useState("");
  const [canChangePassword, setCanChangePassword] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    sessionStatus("/api/session", controller.signal)
      .then(async (result) => {
        setStatus(result.authenticated);
        if (result.authenticated) setCanChangePassword(await passwordChangeAllowed(controller.signal));
      })
      .catch((error: unknown) => { if (!isAbort(error)) setMessage("Вход доступен на приватном адресе через HTTPS или на этом компьютере."); });
    return () => controller.abort();
  }, []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    setMessage("");
    try {
      await login("/api/session", { username: field(data, "username"), password: field(data, "password") });
      window.location.reload();
    } catch { setMessage("Не удалось войти. Проверьте данные и адрес подключения."); }
    finally { form.reset(); }
  }
  async function changePassword(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    setMessage("");
    if (data.get("new_password") !== data.get("confirm_password")) { setMessage("Новые пароли не совпадают."); return; }
    try {
      await postPassword(field(data, "current_password"), field(data, "new_password"));
      window.location.assign("/");
    } catch { setMessage("Не удалось изменить пароль. Проверьте текущий пароль и повторите попытку."); }
    finally { form.reset(); }
  }
  async function logout() {
    try {
      await postLogout("/api/session");
      try { clearVisits(window.sessionStorage); } catch { /* Storage may be unavailable. */ }
      window.location.assign("/");
    } catch { setMessage("Не удалось завершить сеанс."); }
  }
  return (
    <div className="reader-access">
      {status === true ? <><p>Приватное чтение включено. Сеанс действует до 8 часов.</p>{canChangePassword && <form onSubmit={changePassword}><h3>Изменить пароль</h3><label>Текущий пароль<input name="current_password" type="password" autoComplete="current-password" required /></label><label>Новый пароль<input name="new_password" type="password" minLength={10} maxLength={128} autoComplete="new-password" required /></label><label>Повторите новый пароль<input name="confirm_password" type="password" minLength={10} maxLength={128} autoComplete="new-password" required /></label><button>Изменить пароль</button></form>}<button onClick={logout}>Выйти</button></> : status === false ?
        <form onSubmit={submit}><p>Войдите в свою учётную запись.</p><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Пароль<input name="password" type="password" autoComplete="current-password" required /></label><button type="submit">Войти</button></form> : null}
      {message && <p role="alert">{message}</p>}
    </div>
  );
}
