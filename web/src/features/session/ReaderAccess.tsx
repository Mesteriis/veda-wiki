import { useState } from "react";
import { HttpError } from "../../api/http";
import { changePassword as postPassword, login, logout as postLogout, profile, type Credentials } from "../../api/session";
import { useSessionBootstrap } from "../../hooks/useSessionBootstrap";
import { clearVisits } from "../../lib/recent";
import { LoginForm } from "./LoginForm";
import { PasswordForm } from "./PasswordForm";
import { TelegramIDForm } from "./TelegramIDForm";

// Reader sign-in, password change and sign-out in the profile dialog.
export function ReaderAccess() {
  const [message, setMessage] = useState("");
  const [canChangePassword, setCanChangePassword] = useState(false);
  const [telegramID, setTelegramID] = useState<number | null>(null);
  const { status } = useSessionBootstrap("/api/session", {
    unavailableMessage: "Вход доступен на приватном адресе через HTTPS или на этом компьютере.",
    onMessage: setMessage,
    onAuthenticated: async (signal) => {
      // A refused profile only hides the forms; it is not a sign-in problem.
      try {
        const answer = await profile(signal);
        setCanChangePassword(answer.password_change);
        if (answer.telegram) setTelegramID(answer.telegram_id ?? 0);
      } catch (error) { if (!(error instanceof HttpError)) throw error; }
    },
  });
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
      {status === true ? <><p>Приватное чтение включено. Сеанс действует до 8 часов.</p>{canChangePassword && <PasswordForm onMismatch={() => setMessage("Новые пароли не совпадают.")} onChange={changePassword} />}{telegramID !== null && <TelegramIDForm initial={telegramID} />}<button onClick={logout}>Выйти</button></> : status === false ?
        <LoginForm intro="Войдите в свою учётную запись." submitLabel="Войти" onLogin={submit} explicitSubmit /> : null}
      {message && <p role="alert">{message}</p>}
    </div>
  );
}
