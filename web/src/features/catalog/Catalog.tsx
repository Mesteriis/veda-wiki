import { useState } from "react";
import { I } from "../../components/Icon";
import { catalogPages } from "../../lib/catalog";
import { pageHref, updatedLabel } from "../../lib/format";
import { buildTree } from "../../lib/tree";
import type { View } from "../../lib/view";
import { CatalogFolders, type FolderSelection } from "./CatalogFolders";

// The full article catalog with a source and folder filter (no search query).
export function Catalog({ v }: { v: View }) {
  const [selectedFolder, setSelectedFolder] = useState<FolderSelection | null>(() => {
    const group = v.Selected && v.Selected !== "all" && (v.Groups || []).find((item) => item.ID === v.Selected);
    return group ? { sourceID: group.ID, prefix: group.ID + "/", label: group.Name } : null;
  });
  const groups = v.Groups || [];
  const selection = selectedFolder && groups.find((group) => group.ID === selectedFolder.sourceID) ? selectedFolder : null;
  const pages = catalogPages(groups, groups.flatMap((group) => group.Pages || []), selection);
  return <div className="catalog">
    <aside className="catalog-tree panel" aria-label="Источники и папки">
      <h2>Источники и папки</h2>
      <button className={!selection ? "catalog-folder active" : "catalog-folder"} onClick={() => setSelectedFolder(null)}><I name="notes" />Все статьи</button>
      {groups.map((group) => <details className="catalog-branch" key={group.ID} open>
        <summary><I name="chevron-right" /><I name="folder" /><span>{group.Name}</span></summary>
        <button className={selection?.prefix === group.ID + "/" ? "catalog-folder active" : "catalog-folder"} onClick={() => setSelectedFolder({ sourceID: group.ID, prefix: group.ID + "/", label: group.Name })}>Все статьи источника</button>
        <CatalogFolders node={buildTree(group.ID, group.Pages || [])} sourceID={group.ID} prefix={group.ID + "/"} selection={selection} onSelect={setSelectedFolder} parents={[group.Name]} />
      </details>)}
      {!groups.length && <p className="catalog-empty">Доступных разделов пока нет.</p>}
    </aside>
    <section className="catalog-content panel">
      <div className="catalog-breadcrumb"><I name="folder" />{selection ? selection.label : "База знаний"}</div>
      <h2>{selection ? selection.label.split(" / ").at(-1) : "Все статьи"}</h2>
      <div className="catalog-table-head"><span>Название статьи</span><span>Путь в базе знаний</span><span>Обновлено</span></div>
      <div className="catalog-list">{pages.map((page) => <a className="catalog-row" href={pageHref(page.path)} key={page.path}>
        <span className="catalog-title"><span className="home-page-icon"><I name="notes" /></span><strong>{page.title}</strong></span>
        <span className="catalog-path" title={page.path}>{page.path}</span>
        <time dateTime={page.updated_at || undefined}>{updatedLabel(page.updated_at)}</time>
      </a>)}</div>
      {!pages.length && <p className="catalog-empty">В этом разделе пока нет доступных статей.</p>}
    </section>
  </div>;
}
