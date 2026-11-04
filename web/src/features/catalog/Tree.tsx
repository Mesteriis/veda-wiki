import { useState } from "react";
import type { Group, Page } from "../../api/types";
import { I } from "../../components/Icon";
import { Panel } from "../../components/Panel";
import { pageHref } from "../../lib/format";
import { buildMergedTree, buildTree, type TreeNode } from "../../lib/tree";

type TreePage = Page & { sourceName?: string; sourceID?: string };

export function Tree({ groups }: { groups: Group[] }) {
  const [mode, setMode] = useState<"merged" | "sources">("merged");
  return (
    <Panel
      title="Структура знания"
      className="knowledge-tree"
      actions={
        <>
          {groups.length > 1 && <div className="tabs" aria-label="Вид структуры">
            <button type="button" className={mode === "sources" ? "active" : ""} aria-pressed={mode === "sources"} onClick={() => setMode("sources")}>Источники</button>
            <button type="button" className={mode === "merged" ? "active" : ""} aria-pressed={mode === "merged"} onClick={() => setMode("merged")}>Сводно</button>
          </div>}
        </>
      }
    >
      <a className="tree-home" id="knowledge-tree" href="/">
        <I name="chevron-right" />
        <I name="home" />
        Главная
      </a>
      {mode === "merged" && groups.length > 1 ? <div className="tree-pages"><TreeNodes node={buildMergedTree(groups)} /></div> : groups.map((g) => (
        <details className="tree-group" key={g.ID}>
          <summary>
            <I name="chevron-right" />
            <I name="folder" />
            <span>{g.Name}</span>
            <small>{g.Count}</small>
          </summary>
          <div className="tree-pages">
            {g.Description && <p className="tree-source-description">{g.Description}</p>}
            {g.SourceURL && <a className="tree-source-link" href={g.SourceURL} target="_blank" rel="noopener noreferrer">Сайт источника <I name="external-link" /></a>}
            <TreeNodes node={buildTree(g.ID, g.Pages || [])} />
            <a className="tree-all" href={"/?env=" + encodeURIComponent(g.ID)}>
              Все страницы раздела <I name="arrow-right" />
            </a>
          </div>
        </details>
      ))}
    </Panel>
  );
}

function TreeNodes({ node }: { node: TreeNode<TreePage> }) {
  const folders = [...node.folders.entries()].sort(([a], [b]) => a.localeCompare(b));
  const pages = [...node.pages].sort((a, b) => a.title.localeCompare(b.title) || a.path.localeCompare(b.path));
  return (
    <>
      {folders.map(([name, child]) => (
        <details className="tree-group tree-folder" key={name}>
          <summary><I name="chevron-right" /><I name="folder" /><span title={name}>{name}</span></summary>
          <div className="tree-pages"><TreeNodes node={child} /></div>
        </details>
      ))}
      {pages.map((page) => (
        <a href={pageHref(page.path)} key={page.path} title={page.path}>
          <I name="file-text" /><span>{page.title}</span>{page.sourceID && <small title={page.sourceID}>{page.sourceName} · {page.sourceID}</small>}
        </a>
      ))}
    </>
  );
}
