import { useEffect } from "react";
import type { Page, StatePayload } from "../../api/types";
import { readVisits, recordVisit, saveVisits } from "../../lib/recent";
import { conflictWarning } from "../../lib/sourceWarnings";
import { Toc } from "./Toc";

function provenance(page: Page): string {
  if (page.translated) return "Перевод на русский · SHA256";
  if (page.translation_state === "stale") return "Оригинал · перевод устарел · SHA256";
  if (page.program_documentation) return "Оригинал · перевод недоступен · SHA256";
  return "SHA256";
}

export function Reading({ data, page }: { data: StatePayload; page: Page }) {
  const v = data.view;
  const warning = conflictWarning(page);
  // Jump to the heading named in the address once the article is shown.
  useEffect(() => {
    if (!window.location.hash) return;
    let id;
    try {
      id = decodeURIComponent(window.location.hash.slice(1));
    } catch {
      return;
    }
    document.getElementById(id)?.scrollIntoView({ block: "start" });
  }, [page.path]);
  useEffect(() => {
    try { const storage = window.sessionStorage; saveVisits(storage, recordVisit(readVisits(storage), page.path, Date.now())); } catch { /* The article remains readable without storage. */ }
  }, [page.path]);
  return (
    <div className="reading-layout">
      <article className="panel reading">
        <a className="back-link" href="/">
          Главная
        </a>
        <div className="source-path">{page.path}</div>
        {warning && <p className="conflict-warning" role="status">{warning}</p>}
        <div className="provenance">
          {provenance(page)} <code>{page.sha256}</code>
          {page.translated && (page.translation_origin === "local_model" ? <> · Локальный автоматический перевод</> : <> · Происхождение перевода неизвестно</>)}
          {page.translated && <> · Оригинал <code>{page.original_sha256}</code></>}
        </div>
        <div
          className="markdown"
          dangerouslySetInnerHTML={{ __html: data.html }}
        />
        <footer>
          Версия: {v.Generated || "пустая база"}. Проверяйте дату и происхождение документации перед её использованием.
        </footer>
      </article>
      <Toc html={data.toc} />
    </div>
  );
}
