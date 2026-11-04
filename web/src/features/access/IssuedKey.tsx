import type { IssuedKey as Issued } from "../../api/types";

interface IssuedKeyProps {
  issued: Issued;
  onHide: () => void;
}

// The one-time view of a new reader key and its sign-in link.
export function IssuedKey({ issued, onHide }: IssuedKeyProps) {
  return <div className="access-issued" role="status"><strong>Ключ {issued.id} создан</strong><p>Сохраните его сейчас: повторно VEDA его не покажет.</p><textarea readOnly value={issued.key} aria-label="Новый ключ чтения" rows={3} onFocus={(e) => e.target.select()} /><label>Ссылка для входа<textarea readOnly value={`${window.location.origin}/?key=${encodeURIComponent(issued.key)}`} rows={3} onFocus={(e) => e.target.select()} /></label><p>Ссылка содержит ключ доступа. Передавайте её только доверенному получателю.</p><button type="button" onClick={onHide}>Скрыть ключ</button></div>;
}
