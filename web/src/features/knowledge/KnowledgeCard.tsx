import { getEntity } from "../../api/knowledge";
import type { Claim, Evidence, GraphEdge, GraphNode } from "../../api/knowledgeTypes";
import { Quote } from "./ArticleKnowledge";
import { articleHref, claimStatusLabel, entityTypeLabel, relationLabel, resolutionLabel } from "./labels";
import { useKnowledgeRead } from "./useKnowledge";

const CARD_LIMIT = 10;
const QUOTES = 5;

function Quotes({ evidence }: { evidence: Evidence[] | null }) {
  const list = (evidence ?? []).slice(0, QUOTES);
  if (!list.length) return null;
  return <div className="kg-quotes"><h4>Подтверждающие цитаты</h4>{list.map((item, index) => <Quote key={item.block_id + index} evidence={item} />)}</div>;
}

function ClaimLine({ claim, names }: { claim: Claim; names: Map<string, string> }) {
  return <li>
    <span>{names.get(claim.head_id) ?? "сущность"} → {relationLabel(claim.relation)} → {names.get(claim.tail_id) ?? "сущность"}</span>
    <small className="kg-honest">{claimStatusLabel(claim.status, claim.markers)}</small>
    <Quote evidence={claim.evidence} />
  </li>;
}

interface EntityCardProps {
  node: GraphNode;
  names: Map<string, string>;
  isCenter: boolean;
  onCenter: () => void;
  onExpand: () => void;
  expanding: boolean;
}

function EntityDetails({ node, names, isCenter, onCenter, onExpand, expanding }: EntityCardProps) {
  const state = useKnowledgeRead(node.id, (signal) => getEntity(node.id, CARD_LIMIT, signal));
  const card = state.status === "done" ? state.data : null;
  const allNames = new Map(names);
  if (card) allNames.set(card.id, card.name);
  return <>
    <h3>{node.label} <small>{entityTypeLabel(node.type)}</small></h3>
    <div className="kg-actions">
      {!isCenter && <button type="button" onClick={onCenter}>Сделать центром</button>}
      <button type="button" onClick={onExpand} disabled={expanding}>{expanding ? "Раскрываем…" : "Показать соседей"}</button>
    </div>
    {state.status === "loading" && <p className="knowledge-status" role="status">Загружаем карточку…</p>}
    {state.status === "error" && <p className="knowledge-status" role="status">Карточка сущности недоступна.</p>}
    {card && <>
      <p>Упоминаний: {card.mentions}, документов: {card.documents}. Объединение упоминаний: {resolutionLabel(card.resolution.method)}.</p>
      {!!card.candidates?.length && <p className="kg-honest">Возможные совпадения (не подтверждены): {card.candidates.map((candidate) => candidate.name).join(", ")}</p>}
      {!!card.document_list?.length && <><h4>Документы</h4><ul>{card.document_list.map((key) => <li key={key.source_id + "/" + key.path}><a href={articleHref(key)}>{key.source_id}/{key.path}</a></li>)}</ul></>}
      {!!card.claims?.length && <><h4>Отношения</h4><ul className="kg-claims">{card.claims.map((claim) => <ClaimLine key={claim.id} claim={claim} names={allNames} />)}</ul></>}
      {card.truncated && <p className="knowledge-note">Показана часть данных: сервер ограничил ответ.</p>}
    </>}
  </>;
}

function EdgeDetails({ edge, names }: { edge: GraphEdge; names: Map<string, string> }) {
  const from = names.get(edge.from) ?? "узел", to = names.get(edge.to) ?? "узел";
  if (edge.kind === "mention") return <>
    <h3>Упоминание</h3>
    <p>Документ «{from}» упоминает «{to}»{edge.count ? ` (${edge.count})` : ""}. Упоминание найдено автоматически.</p>
    <Quotes evidence={edge.evidence} />
  </>;
  return <>
    <h3>{from} → {relationLabel(edge.relation)} → {to}</h3>
    <p>Отношение направлено от «{from}» к «{to}». Оно извлечено из текста статьи и не проверено человеком.</p>
    <p className="kg-honest">{claimStatusLabel(edge.status, edge.markers)}</p>
    <Quotes evidence={edge.evidence} />
  </>;
}

interface CardProps {
  node: GraphNode | undefined;
  edge: GraphEdge | undefined;
  names: Map<string, string>;
  center: string;
  onCenter: (id: string) => void;
  onExpand: (id: string) => void;
  expanding: boolean;
}

/** The card of the selected node or edge; read-only, with links to the articles. */
export function KnowledgeCard({ node, edge, names, center, onCenter, onExpand, expanding }: CardProps) {
  let body;
  if (node?.kind === "document") {
    body = <>
      <h3>{node.label} <small>Документ</small></h3>
      {node.key && <p><a href={articleHref(node.key)}>Открыть статью {node.key.source_id}/{node.key.path}</a></p>}
    </>;
  } else if (node) {
    body = <EntityDetails node={node} names={names} isCenter={node.id === center} onCenter={() => onCenter(node.id)} onExpand={() => onExpand(node.id)} expanding={expanding} />;
  } else if (edge) {
    body = <EdgeDetails edge={edge} names={names} />;
  } else {
    body = <p>Выберите узел или связь на графе либо в списке связей.</p>;
  }
  return <section className="kg-card" aria-label="Карточка выбранного элемента" aria-live="polite">{body}</section>;
}
