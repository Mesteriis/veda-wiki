import { useState } from "react";
import { I } from "../../components/Icon";
import { pageHref } from "../../lib/format";
import type { View } from "../../lib/view";
import { clusterPosition, graphHeight, graphPageCount, layoutNodes } from "./layout";

export function Graph({ v }: { v: View }) {
  const [selected, setSelected] = useState<string | null>(null);
  const [scale, setScale] = useState(1);
  const [sourceID, setSourceID] = useState("");
  const [pageIndex, setPageIndex] = useState(0);
  const available = v.Groups || [];
  const groups = sourceID ? available.filter((group) => group.ID === sourceID) : available.slice(0, 6);
  const pageCount = graphPageCount(groups);
  const currentPage = Math.min(pageIndex, pageCount - 1);
  const nodes = layoutNodes(groups, currentPage);
  const byPath = new Map(nodes.map((node) => [node.path, node]));
  const edges = (v.GraphEdges || []).filter((edge) => byPath.has(edge.from) && byPath.has(edge.to));
  const chosen = selected === null ? undefined : byPath.get(selected);
  const allPages = new Map(available.flatMap((group) => group.Pages || []).map((page) => [page.path, page]));
  const related = chosen ? (v.GraphEdges || []).filter((edge) => edge.from === chosen.path || edge.to === chosen.path).map((edge) => ({ path: edge.from === chosen.path ? edge.to : edge.from, incoming: edge.to === chosen.path })).filter((item) => allPages.has(item.path)) : [];
  const height = graphHeight(groups.length);
  return <section className="graph-section" aria-label="Граф связей статей">
    <div className="graph-filter"><label>Источник <select value={sourceID} onChange={(event) => { setSourceID(event.target.value); setPageIndex(0); setSelected(null); }}><option value="">Все источники</option>{available.map((group) => <option key={group.ID} value={group.ID}>{group.Name}</option>)}</select></label>{pageCount > 1 && <div><button disabled={currentPage === 0} onClick={() => { setPageIndex(currentPage - 1); setSelected(null); }}>Назад</button><span>Страница {currentPage + 1} из {pageCount}</span><button disabled={currentPage + 1 === pageCount} onClick={() => { setPageIndex(currentPage + 1); setSelected(null); }}>Вперёд</button></div>}</div>
    {(v.Count > nodes.length) && <p className="graph-note">Показано {nodes.length} из {v.Count} доступных статей. Выберите источник и страницу, чтобы увидеть остальные; линии показаны только между видимыми узлами.</p>}
    {!nodes.length ? <p className="graph-note">Нет доступных статей для отображения.</p> : <>
      <div className={'graph-canvas-shell' + (groups.length === 3 && scale === 1 ? ' graph-three-groups' : '')}><div className="graph-viewport" tabIndex={0} aria-label="Область графа">
        <div className="graph-stage" style={{ width: 960 * scale, height: height * scale }}>
          <div className="graph-inner" style={{ width: 960, height, transform: `scale(${scale})` }}>
            {groups.map((group, i) => { const origin = clusterPosition(i, groups.length); return <div className={'graph-cluster graph-cluster-' + (i % 3)} key={group.ID} style={{ left: origin.x, top: origin.y }}><strong>{group.Name}</strong></div>; })}
            <svg className="graph-lines" width="960" height={height} aria-hidden="true">{edges.map((edge) => { const from = byPath.get(edge.from), to = byPath.get(edge.to); if (!from || !to) return null; return <line key={edge.from + '\0' + edge.to} x1={from.x + from.width / 2} y1={from.y + 22} x2={to.x + to.width / 2} y2={to.y + 22} />; })}</svg>
            {nodes.map((node) => <button key={node.path} title={node.title} className={'graph-node graph-node-' + (node.groupIndex % 3) + (selected === node.path ? ' selected' : '')} style={{ left: node.x, top: node.y, width: node.width }} onClick={() => setSelected(node.path)}><I name="file-text" /><span>{node.title}</span></button>)}
          </div>
        </div>
      </div>
      <div className="graph-tools"><div className="graph-legend"><strong>Легенда</strong><span><I name="file-text" />Статья</span><span><I name="folder" />Источник</span><span><I name="affiliate" />Связь между статьями</span></div><div className="graph-zoom"><button aria-label="Уменьшить" onClick={() => setScale((s) => Math.max(.6, +(s - .2).toFixed(1)))}>−</button><span>{Math.round(scale * 100)}%</span><button aria-label="Увеличить" onClick={() => setScale((s) => Math.min(1.6, +(s + .2).toFixed(1)))}>+</button></div></div></div>
      <div className="graph-detail">{chosen ? <><I name="file-text" /><span><strong>{chosen.title}</strong><small>{chosen.group} / {chosen.path}</small></span><a href={pageHref(chosen.path)}>Открыть статью <I name="arrow-right" /></a></> : <p>Выберите статью на графе, чтобы открыть её.</p>}</div>
      {chosen && <section className="graph-related" aria-label="Связанные статьи"><h2>Связанные статьи</h2>{related.length ? <ul>{related.map((item) => <li key={(item.incoming ? 'in:' : 'out:') + item.path}><span>{item.incoming ? 'Ссылается на эту статью' : 'Ссылка из этой статьи'}:</span> <a href={pageHref(item.path)}>{allPages.get(item.path)?.title}</a></li>)}</ul> : <p>Связей с доступными статьями не найдено.</p>}</section>}
    </>}
  </section>;
}
