import type { GraphEdge } from "../../api/knowledgeTypes";
import type { Selection } from "./KnowledgeCanvas";
import { claimStatusLabel, relationLabel } from "./labels";

function describe(edge: GraphEdge, names: Map<string, string>): string {
  const from = names.get(edge.from) ?? "узел", to = names.get(edge.to) ?? "узел";
  return edge.kind === "mention" ? `${from} упоминает ${to}` : `${from} → ${relationLabel(edge.relation)} → ${to}`;
}

/** The same edges as the canvas, as a list a screen reader or a keyboard can walk. */
export function RelationList({ edges, names, selection, onSelect }: { edges: GraphEdge[]; names: Map<string, string>; selection: Selection; onSelect: (selection: Selection) => void }) {
  const relations = edges.filter((edge) => edge.kind === "relation");
  const mentions = edges.filter((edge) => edge.kind === "mention");
  const item = (edge: GraphEdge) => <li key={edge.id}>
    <button type="button" aria-pressed={selection?.kind === "edge" && selection.id === edge.id} onClick={() => onSelect({ kind: "edge", id: edge.id })}>{describe(edge, names)}</button>
    {edge.kind === "relation" && <small className="kg-honest">{claimStatusLabel(edge.status, edge.markers)}{edge.evidence?.[0] ? ` · ${edge.evidence[0].source_id}/${edge.evidence[0].path}` : ""}</small>}
  </li>;
  return <section className="kg-list" aria-label="Список связей">
    <h2>Связи</h2>
    {!edges.length && <p>Связей нет.</p>}
    {!!relations.length && <><h3>Отношения</h3><ul>{relations.map(item)}</ul></>}
    {!!mentions.length && <><h3>Упоминания в документах</h3><ul>{mentions.map(item)}</ul></>}
  </section>;
}
