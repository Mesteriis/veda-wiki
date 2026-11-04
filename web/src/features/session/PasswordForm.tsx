import type { FormEvent } from "react";
import { field } from "../../lib/forms";

interface PasswordFormProps {
  /** Called when the two new passwords differ; the form keeps its values. */
  onMismatch: () => void;
  /** Changes the password; it reports its own errors. The form is cleared afterwards. */
  onChange: (current: string, next: string) => Promise<void>;
}

// The reader's own password change in the profile dialog.
export function PasswordForm({ onMismatch, onChange }: PasswordFormProps) {
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const next = field(data, "new_password");
    if (next !== field(data, "confirm_password")) { onMismatch(); return; }
    try { await onChange(field(data, "current_password"), next); }
    finally { form.reset(); }
  }
  return <form onSubmit={submit}><h3>Изменить пароль</h3><label>Текущий пароль<input name="current_password" type="password" autoComplete="current-password" required /></label><label>Новый пароль<input name="new_password" type="password" minLength={10} maxLength={128} autoComplete="new-password" required /></label><label>Повторите новый пароль<input name="confirm_password" type="password" minLength={10} maxLength={128} autoComplete="new-password" required /></label><button>Изменить пароль</button></form>;
}
