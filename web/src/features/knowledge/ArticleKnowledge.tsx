import { useState } from "react";
import { getArticleKnowledge } from "../../api/knowledge";
import type { ArticleKnowledge as Answer, Evidence, Reason, RelatedArticle } from "../../api/knowledgeTypes";
import type { Page } from "../../api/types";
import { articleHref, claimStatusLabel, entityGraphHref, entityTypeLabel, reasonLabels, relationLabel, resolutionLabel } from "./labels";
import { useKnowledgeEnabled, useKnowledgeRead } from "./useKnowledge";

const ARTICLE_LIMIT = 50;
const SHOWN_REASONS = 3;

const statusText: Record<Exclude<Answer["status"], "ready">, string> = {
  processing: "Статья анализируется. Сущности и связанные статьи появятся после обработки.",
  stale: "Индекс обновляется: прежние связи скрыты, пока текст не будет обработан заново.",
  unavailable: "Граф знаний сейчас недоступен. Статья читается как обычно.",
  too_large: "Статья слишком большая для автоматического анализа.",
  failed: "Автоматический анализ статьи не удался.",
};

/** The article's own path inside its source, or null when the page key is not source-qualified. */
export function articlePath(page: Page): string | null {
  const prefix = page.environment + "/";
  return page.environment && page.path.startsWith(prefix) ? page.path.slice(prefix.length) : null;
}

function Status({ children }: { children: string }) {
  return <section className="knowledge-article" aria-label="Граф знаний статьи"><p className="knowledge-status" role="status">{children}</p></section>;
}

/** One supporting quote: untrusted article text, rendered as text only. */
export function Quote({ evidence }: { evidence: Evidence }) {
  return <figure className="knowledge-quote">
    <blockquote>{evidence.quote}</blockquote>
    <figcaption><a href={articleHref(evidence)}>{evidence.source_id}/{evidence.path}</a></figcaption>
  </figure>;
}

function ReasonLine({ reason, names }: { reason: Reason; names: Map<string, string> }) {
  const name = reason.entity_id ? names.get(reason.entity_id) : undefined;
  const evidence = reason.evidence?.[0];
  let detail = "";
  if (reason.kind === "shared_entity" && name) detail = name;
  if (reason.kind === "candidate") detail = (name ? name + " — " : "") + "возможное совпадение, не подтверждено";
  if (reason.kind === "relation") detail = `«${relationLabel(reason.relation)}», ${claimStatusLabel("extracted")}`;
  return <li>
    <span className={"knowledge-reason knowledge-reason-" + reason.kind}>{reasonLabels[reason.kind]}</span>{detail && <>: {detail}</>}
    {evidence && reason.kind !== "shared_entity" && <Quote evidence={evidence} />}
  </li>;
}

function Related({ item, names, titleOf }: { item: RelatedArticle; names: Map<string, string>; titleOf: (path: string) => string | undefined }) {
  const qualified = item.key.source_id + "/" + item.key.path;
  return <li>
    <a href={articleHref(item.key)}>{titleOf(qualified) || qualified}</a>
    <ul className="knowledge-reasons">{item.reasons.slice(0, SHOWN_REASONS).map((reason, index) => <ReasonLine key={reason.kind + index} reason={reason} names={names} />)}</ul>
    {item.reasons.length > SHOWN_REASONS && <details><summary>Ещё причин: {item.reasons.length - SHOWN_REASONS}</summary>
      <ul className="knowledge-reasons">{item.reasons.slice(SHOWN_REASONS).map((reason, index) => <ReasonLine key={reason.kind + index} reason={reason} names={names} />)}</ul>
    </details>}
  </li>;
}

function Ready({ answer, titleOf }: { answer: Answer; titleOf: (path: string) => string | undefined }) {
  const view = answer.knowledge;
  const entities = view?.entities ?? [];
  const related = view?.related ?? [];
  if (!entities.length && !related.length) return <Status>Сущности и связанные статьи не найдены.</Status>;
  const names = new Map(entities.map((entity) => [entity.id, entity.name]));
  return <section className="knowledge-article" aria-label="Граф знаний статьи">
    <p className="knowledge-note">Найдено автоматически по тексту статьи и может быть неточным.</p>
    <h2>Сущности</h2>
    {entities.length ? <ul className="knowledge-entities">{entities.map((entity) => <li key={entity.id}>
      <a href={entityGraphHref(entity.id)}>{entity.name}</a> <span className="knowledge-type">{entityTypeLabel(entity.type)}</span>
      {entity.resolution.method === "candidate" && <span className="knowledge-candidate">{resolutionLabel("candidate")}</span>}
    </li>)}</ul> : <p className="knowledge-status">Сущности не найдены.</p>}
    <h2>Связанные статьи</h2>
    {related.length ? <ul className="knowledge-related">{related.map((item) => <Related key={item.key.source_id + "/" + item.key.path} item={item} names={names} titleOf={titleOf} />)}</ul> : <p className="knowledge-status">Связанные статьи не найдены.</p>}
    {view?.truncated && <p className="knowledge-note">Показана часть результатов: сервер ограничил ответ.</p>}
  </section>;
}

// Entities and related articles of the displayed article. Nothing here blocks
// reading: a disabled module shows nothing, other states one short line.
export function ArticleKnowledge({ page, titleOf }: { page: Page; titleOf: (path: string) => string | undefined }) {
  const path = articlePath(page);
  const [attempt, setAttempt] = useState(0);
  const enabled = useKnowledgeEnabled();
  const key = path === null ? null : [page.environment, path, page.sha256, attempt].join("\0");
  const state = useKnowledgeRead(key, (signal) => getArticleKnowledge(page.environment, path ?? "", ARTICLE_LIMIT, signal));
  const retry = () => setAttempt((value) => value + 1);
  if (state.status === "idle") return null;
  if (state.status === "loading") return enabled ? <Status>Загружаем сущности и связанные статьи…</Status> : null;
  if (state.status === "error") {
    if (state.failure === "disabled" || state.failure === "not_found" || state.failure === "invalid") return null;
    return <Status>{statusText.unavailable}</Status>;
  }
  const answer = state.data;
  // Knowledge of another text than the one displayed is never shown as current.
  if (answer.key.content_sha256 !== page.sha256) return <Status>Текст статьи изменился: связи для показанной версии ещё не готовы.</Status>;
  if (answer.status === "ready") return <Ready answer={answer} titleOf={titleOf} />;
  const text = statusText[answer.status] + (answer.status === "failed" && answer.error ? ` Код: ${answer.error}.` : "");
  return <section className="knowledge-article" aria-label="Граф знаний статьи">
    <p className="knowledge-status" role="status">{text}</p>
    {(answer.status === "processing" || answer.status === "stale") && <button type="button" onClick={retry}>Проверить снова</button>}
  </section>;
}
