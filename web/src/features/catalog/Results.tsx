import { I } from "../../components/Icon";
import { Panel } from "../../components/Panel";
import { pageHref } from "../../lib/format";
import type { View } from "../../lib/view";
import { Catalog } from "./Catalog";

// Search results for a query; without one, the catalog of all articles.
export function Results({ v }: { v: View }) {
  if (!v.Query) return <Catalog v={v} />;
  return (
    <Panel
      className="search-results"
      title={v.Query ? "Результаты поиска: " + v.Query : "Все страницы"}
      actions={<span>{(v.Pages || []).length}</span>}
    >
      <div className="document-list">
        {(v.Pages || []).map((p) => (
          <a href={pageHref(p.path)} key={p.path}>
            <I name="file-text" />
            <span>
              <strong>{p.title}</strong>
              <small>{p.path}</small>
            </span>
            <I name="arrow-up-right" />
          </a>
        ))}
        {!v.Pages?.length && <p>Совпадений нет. Попробуйте другой запрос.</p>}
      </div>
    </Panel>
  );
}
