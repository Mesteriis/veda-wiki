import type { FormEvent } from "react";
import type { Credentials } from "../../api/session";
import { field } from "../../lib/forms";

interface LoginFormProps {
  /** Text of the submit button. */
  submitLabel: string;
  /** Signs in; it reports its own errors. The form is cleared afterwards either way. */
  onLogin: (credentials: Credentials) => Promise<void>;
  /** Disables the button while a request runs; the reader form has no busy state. */
  busy?: boolean;
  /** A line shown at the top of the form (the reader dialog). */
  intro?: string;
  /** The reader form has always declared its button as type="submit". */
  explicitSubmit?: boolean;
}

// The user name and password form shared by the reader, access and GitHub sessions.
export function LoginForm({ submitLabel, onLogin, busy, intro, explicitSubmit }: LoginFormProps) {
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    try { await onLogin({ username: field(data, "username"), password: field(data, "password") }); }
    finally { form.reset(); }
  }
  return <form onSubmit={submit}>{intro !== undefined && <p>{intro}</p>}<label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Пароль<input name="password" type="password" autoComplete="current-password" required /></label><button type={explicitSubmit ? "submit" : undefined} disabled={busy}>{submitLabel}</button></form>;
}
