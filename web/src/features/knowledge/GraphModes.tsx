import { searchEntities } from "../../api/knowledge";
import { useKnowledgeEnabled, useKnowledgeRead } from "./useKnowledge";

export type GraphMode = "links" | "knowledge";

/** The graph screen mode chosen by the address: ?graph=1&mode=knowledge. */
export function graphMode(search: string): GraphMode {
  return new URLSearchParams(search).get("mode") === "knowledge" ? "knowledge" : "links";
}

// The switch between explicit links and the knowledge graph. On the links
// screen it appears only once the server answered that the module is on, so
// with the module off that screen stays exactly as before.
export function GraphModes({ mode }: { mode: GraphMode }) {
  const enabled = useKnowledgeEnabled();
  useKnowledgeRead(mode === "links" ? "probe" : null, (signal) => searchEntities({ limit: 1 }, signal));
  if (mode === "links" && enabled !== true) return null;
  return <nav className="graph-modes" aria-label="Режим графа">
    <a href="/?graph=1" aria-current={mode === "links" ? "page" : undefined}>Ссылки</a>
    <a href="/?graph=1&mode=knowledge" aria-current={mode === "knowledge" ? "page" : undefined}>Граф знаний</a>
  </nav>;
}
