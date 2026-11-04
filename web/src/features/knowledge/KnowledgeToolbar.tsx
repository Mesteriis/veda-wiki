import { useState, type FormEvent } from "react";
import { RELATION_TYPES, KNOWLEDGE_LIMITS } from "../../api/knowledgeTypes";
import type { Group } from "../../api/types";
import { relationLabel } from "./labels";

interface ToolbarProps {
  groups: Group[];
  source: string;
  relations: string[];
  onSearch: (query: string) => void;
  onSource: (source: string) => void;
  onRelations: (relations: string[]) => void;
}

/** Entity search, source filter and relation-type filter of the knowledge graph. */
export function KnowledgeToolbar({ groups, source, relations, onSearch, onSource, onRelations }: ToolbarProps) {
  const [query, setQuery] = useState("");
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSearch(query.trim());
  }
  function toggle(relation: string, on: boolean) {
    const next = on ? [...relations, relation] : relations.filter((item) => item !== relation);
    // The server cannot filter out every type; at least one stays selected.
    if (next.length) onRelations(RELATION_TYPES.filter((item) => next.includes(item)));
  }
  return <div className="kg-toolbar graph-filter">
    <form role="search" onSubmit={submit}>
      <label>Сущность <input type="search" value={query} maxLength={KNOWLEDGE_LIMITS.queryRunes} placeholder="Например, billing" onChange={(event) => setQuery(event.target.value)} /></label>
      <button type="submit">Найти</button>
    </form>
    <label>Источник <select value={source} onChange={(event) => onSource(event.target.value)}><option value="">Все источники</option>{groups.map((group) => <option key={group.ID} value={group.ID}>{group.Name}</option>)}</select></label>
    <fieldset className="kg-relations">
      <legend>Типы отношений</legend>
      {RELATION_TYPES.map((relation) => <label key={relation}><input type="checkbox" checked={relations.includes(relation)} disabled={relations.length === 1 && relations.includes(relation)} onChange={(event) => toggle(relation, event.target.checked)} />{relationLabel(relation)}</label>)}
    </fieldset>
  </div>;
}
