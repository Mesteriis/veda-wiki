import { useEffect, useMemo, useRef, useState } from "react";
import { I } from "../../components/Icon";
import { isAbort } from "../../api/http";
import { getGraph, knowledgeFailure, searchEntities, type GraphSearch, type KnowledgeFailure } from "../../api/knowledge";
import { RELATION_TYPES, type EntitySummary } from "../../api/knowledgeTypes";
import type { View } from "../../lib/view";
import { mergeGraphs, normalizeGraph, radialLayout, type KnowledgeGraphData } from "./graphLayout";
import { KnowledgeCanvas, type Selection } from "./KnowledgeCanvas";
import { KnowledgeCard } from "./KnowledgeCard";
import { KnowledgeToolbar } from "./KnowledgeToolbar";
import { entityTypeLabel } from "./labels";
import { RelationList } from "./RelationList";
import { useKnowledgeGeneration, useKnowledgeRead, type ReadState } from "./useKnowledge";

const SEARCH_LIMIT = 20;
// One request stays well under the server caps (200 nodes, 400 edges);
// expanding neighbours adds more up to those caps.
const STEP = { maxNodes: 40, maxEdges: 80 } as const;

/** What the last "show neighbours" of one node did: a failure is never shown as "no neighbours". */
type ExpansionOutcome = { id: string; added: number } | { id: string; failure: KnowledgeFailure };

interface Expansion {
  base: string;
  generation: number;
  graphs: KnowledgeGraphData[];
  pending: string | null;
  outcome: ExpansionOutcome | null;
}

const expansionFailures: Record<KnowledgeFailure, string> = {
  timeout: "Сервер не успел ответить: соседи не загружены. Попробуйте ещё раз.",
  network: "Нет связи с сервером: соседи не загружены. Попробуйте ещё раз.",
  unavailable: "Граф знаний сейчас недоступен: соседи не загружены. Попробуйте позже.",
  disabled: "Граф знаний отключён на этом сервере: соседи не загружены.",
  not_found: "Сущность больше недоступна: соседи не загружены.",
  invalid: "Сервер отклонил запрос соседей.",
};

/** The card line about the last expansion of the selected node, or null. */
export function expansionText(outcome: ExpansionOutcome | null, id: string | undefined): string | null {
  if (!outcome || outcome.id !== id) return null;
  if ("failure" in outcome) return expansionFailures[outcome.failure];
  return outcome.added ? `Добавлено узлов: ${outcome.added}.` : "Новых соседей нет: все найденные связи уже показаны.";
}

/** Keeps the chosen centre in the address so the view can be linked and reloaded. */
function rememberCenter(id: string): void {
  const params = new URLSearchParams(window.location.search);
  params.set("entity", id);
  window.history.replaceState(window.history.state, "", window.location.pathname + "?" + params.toString());
}

function Message({ children }: { children: string }) {
  return <p className="graph-note" role="status">{children}</p>;
}

function SearchResults({ state, center, onPick }: { state: ReadState<{ entities: EntitySummary[] | null; total: number }>; center: string | null; onPick: (id: string) => void }) {
  if (state.status === "idle") return null;
  if (state.status === "loading") return <Message>Ищем сущности…</Message>;
  if (state.status === "error") return null;
  const entities = state.data.entities ?? [];
  if (!entities.length) return <Message>Сущности не найдены. Статьи могут ещё обрабатываться.</Message>;
  return <div className="kg-results" aria-label="Найденные сущности">
    <span>Найдено: {state.data.total}. Выберите центр графа:</span>
    <ul>{entities.map((entity) => <li key={entity.id}><button type="button" aria-pressed={entity.id === center} onClick={() => onPick(entity.id)}>
      {entity.name} <small>{entityTypeLabel(entity.type)} · документов: {entity.documents}</small>
    </button></li>)}</ul>
  </div>;
}

function failureText(state: ReadState<unknown>): string | null {
  if (state.status !== "error") return null;
  if (state.failure === "disabled") return "Граф знаний отключён на этом сервере. Связи по ссылкам доступны в режиме «Ссылки».";
  if (state.failure === "not_found") return "Сущность не найдена или недоступна.";
  return "Граф знаний сейчас недоступен. Попробуйте позже; статьи читаются как обычно.";
}

// The knowledge graph mode of the graph screen: everything here only reads.
export function KnowledgeGraph({ v }: { v: View }) {
  const generation = useKnowledgeGeneration();
  const [query, setQuery] = useState("");
  const [source, setSource] = useState("");
  const [relations, setRelations] = useState<string[]>([...RELATION_TYPES]);
  const [center, setCenter] = useState<string | null>(() => new URLSearchParams(window.location.search).get("entity") || null);
  const [selection, setSelection] = useState<Selection>(center ? { kind: "node", id: center } : null);
  const [scale, setScale] = useState(1);
  const [expansion, setExpansion] = useState<Expansion | null>(null);
  const expandRequest = useRef<AbortController | null>(null);

  const entities = useKnowledgeRead(JSON.stringify(["entities", query, source]), (signal) => searchEntities({ q: query, sourceID: source, limit: SEARCH_LIMIT }, signal));
  const relationFilter = relations.length === RELATION_TYPES.length ? undefined : relations;
  const filters: Omit<GraphSearch, "entityID"> = { sources: source ? [source] : undefined, relations: relationFilter, ...STEP };
  const graphKey = center === null ? null : JSON.stringify(["graph", center, source, relationFilter ?? []]);
  const base = useKnowledgeRead(graphKey, (signal) => getGraph({ entityID: center ?? "", ...filters }, signal));
  useEffect(() => () => expandRequest.current?.abort(), [graphKey, generation]);

  const own = expansion && expansion.base === graphKey && expansion.generation === generation ? expansion : null;
  const graph = useMemo(() => {
    if (base.status !== "done") return null;
    return (own?.graphs ?? []).reduce(mergeGraphs, normalizeGraph(base.data));
  }, [base, own]);
  const layout = useMemo(() => (graph && center ? radialLayout(graph, center) : null), [graph, center]);
  const viewport = useRef<HTMLDivElement | null>(null);
  // Narrow screens see the middle of the canvas, where the centre is, first.
  useEffect(() => {
    const element = viewport.current;
    if (!element || !layout) return;
    element.scrollLeft = Math.max(0, (element.scrollWidth - element.clientWidth) / 2);
    element.scrollTop = Math.max(0, (element.scrollHeight - element.clientHeight) / 2);
  }, [layout]);
  const names = useMemo(() => new Map((graph?.nodes ?? []).map((node) => [node.id, node.label])), [graph]);

  function choose(id: string) {
    setCenter(id);
    setSelection({ kind: "node", id });
    rememberCenter(id);
  }
  async function expand(id: string) {
    if (!graphKey) return;
    expandRequest.current?.abort();
    const controller = new AbortController();
    expandRequest.current = controller;
    const previous = own?.graphs ?? [];
    const shown = new Set((graph?.nodes ?? []).map((node) => node.id));
    setExpansion({ base: graphKey, generation, graphs: previous, pending: id, outcome: null });
    try {
      const more = normalizeGraph(await getGraph({ entityID: id, ...filters }, controller.signal));
      const added = more.nodes.filter((node) => !shown.has(node.id)).length;
      if (!controller.signal.aborted) setExpansion({ base: graphKey, generation, graphs: [...previous, more], pending: null, outcome: { id, added } });
    } catch (error) {
      if (!controller.signal.aborted && !isAbort(error)) setExpansion({ base: graphKey, generation, graphs: previous, pending: null, outcome: { id, failure: knowledgeFailure(error) } });
    }
  }

  const unavailable = failureText(entities) ?? (entities.status === "error" ? null : failureText(base));
  const selectedNode = selection?.kind === "node" ? graph?.nodes.find((node) => node.id === selection.id) : undefined;
  const selectedEdge = selection?.kind === "edge" ? graph?.edges.find((edge) => edge.id === selection.id) : undefined;
  return <div className="kg">
    <KnowledgeToolbar groups={v.Groups ?? []} source={source} relations={relations} onSearch={setQuery}
      onSource={(next) => { setSource(next); setSelection(center ? { kind: "node", id: center } : null); }} onRelations={setRelations} />
    {entities.status === "error" && entities.failure === "disabled" ? <Message>{unavailable ?? ""}</Message> : <>
      <SearchResults state={entities} center={center} onPick={choose} />
      {unavailable && <Message>{unavailable}</Message>}
      {center === null && entities.status === "done" && !!entities.data.entities?.length && <Message>Выберите сущность, чтобы построить граф вокруг неё.</Message>}
      {base.status === "loading" && <Message>Строим граф…</Message>}
      {graph && layout && center && (graph.nodes.length ? <>
        {graph.truncated && <p className="graph-note">Показана часть графа: сервер ограничивает ответ. Раскрывайте соседей выбранной сущности постепенно (не более 200 узлов и 400 связей).</p>}
        <div className="kg-main">
          <div className="kg-stage">
            <div ref={viewport} className="graph-viewport kg-viewport" aria-label="Область графа знаний">
              <KnowledgeCanvas graph={graph} layout={layout} center={center} scale={scale} selection={selection} onSelect={setSelection} />
            </div>
            <div className="graph-tools">
              <div className="graph-legend kg-legend"><strong>Легенда</strong>
                <span><svg width="18" height="18" aria-hidden="true"><circle cx="9" cy="9" r="7" className="kg-legend-entity" /></svg>Сущность</span>
                <span><svg width="18" height="18" aria-hidden="true"><rect x="2" y="2" width="14" height="14" rx="3" className="kg-legend-document" /></svg>Документ</span>
                <span><I name="arrow-right" />Отношение: от субъекта к объекту</span>
                <span><svg width="18" height="18" aria-hidden="true"><line x1="1" y1="9" x2="17" y2="9" className="kg-legend-mention" /></svg>Упоминание в документе</span>
              </div>
              <div className="graph-zoom"><button aria-label="Уменьшить" onClick={() => setScale((s) => Math.max(.4, +(s - .2).toFixed(1)))}>−</button><span>{Math.round(scale * 100)}%</span><button aria-label="Увеличить" onClick={() => setScale((s) => Math.min(1.6, +(s + .2).toFixed(1)))}>+</button></div>
            </div>
          </div>
          <KnowledgeCard node={selectedNode} edge={selectedEdge} names={names} center={center}
            onCenter={choose} onExpand={(id) => { void expand(id); }} expanding={!!own?.pending && own.pending === selectedNode?.id}
            expansionNote={expansionText(own?.outcome ?? null, selectedNode?.id)} />
        </div>
        <RelationList edges={graph.edges} names={names} selection={selection} onSelect={setSelection} />
      </> : <Message>У этой сущности нет связей с учётом выбранных фильтров.</Message>)}
    </>}
  </div>;
}
