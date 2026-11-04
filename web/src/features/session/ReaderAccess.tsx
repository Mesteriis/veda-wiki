import { useEffect, useState } from "react";
import { isAbort } from "../../api/http";
import { changePassword as postPassword, login, logout as postLogout, sessionStatus, type Credentials } from "../../api/session";
import { clearVisits } from "../../lib/recent";
import { LoginForm } from "./LoginForm";
import { PasswordForm } from "./PasswordForm";
import { passwordChangeAllowed } from "./passwordChange";

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
  async function submit(credentials: Credentials) {
    setMessage("");
    try {
      await login("/api/session", credentials);
      window.location.reload();
    } catch { setMessage("Не удалось войти. Проверьте данные и адрес подключения."); }
  }
  async function changePassword(current: string, next: string) {
    setMessage("");
    try {
      await postPassword(current, next);
      window.location.assign("/");
    } catch { setMessage("Не удалось изменить пароль. Проверьте текущий пароль и повторите попытку."); }
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
      {status === true ? <><p>Приватное чтение включено. Сеанс действует до 8 часов.</p>{canChangePassword && <PasswordForm onMismatch={() => setMessage("Новые пароли не совпадают.")} onChange={changePassword} />}<button onClick={logout}>Выйти</button></> : status === false ?
        <LoginForm intro="Войдите в свою учётную запись." submitLabel="Войти" onLogin={submit} explicitSubmit /> : null}
      {message && <p role="alert">{message}</p>}
    </div>
  );
}
