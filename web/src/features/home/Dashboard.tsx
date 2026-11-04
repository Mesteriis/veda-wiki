import { I } from "../../components/Icon";
import { pageHref, updatedLabel } from "../../lib/format";
import type { View } from "../../lib/view";
import { Tree } from "../catalog/Tree";

const covers = ["collection-dunes.jpg", "collection-leaf.jpg", "collection-mountains.jpg"];
const pageCountLabel = (count: number) => `${count} ${new Intl.PluralRules("ru").select(count) === "one" ? "страница" : new Intl.PluralRules("ru").select(count) === "few" ? "страницы" : "страниц"}`;

export function Dashboard({ v }: { v: View }) {
  const groups = v.Groups || [];
  return (
    <div className="dashboard home-dashboard">
      <div className="home-content">
        {groups.length > 0 && <section className="collections" aria-labelledby="collections-title">
          <h2 id="collections-title">Коллекции знаний</h2>
          <p>Быстрый доступ к разделам базы знаний</p>
          <div className="collection-grid">{groups.slice(0, 3).map((group, index) => <a className="collection" href={'/?env=' + encodeURIComponent(group.ID)} key={group.ID}>
            <img src={'/assets/' + covers[index]} alt="" />
            <span className="collection-copy"><I name="folder" /><span><strong>{group.Name}</strong><small>{group.Description || pageCountLabel(group.Count)}</small></span><I name="arrow-right" /></span>
          </a>)}</div>
        </section>}
        <section className="home-recent" aria-labelledby="home-recent-title">
          <div className="home-section-heading"><div><h2 id="home-recent-title">Недавние страницы</h2><p>Недавно обновлённые материалы</p></div>{v.Count > 0 && <a href="/?env=all">Все страницы <I name="arrow-right" /></a>}</div>
          {v.Count === 0 ? <p className="home-empty">Здесь появятся доступные статьи базы знаний.</p> : <div className="home-page-list">{(v.Cards || []).map((card) => <a className="home-page-row" href={pageHref(card.Page.path)} key={card.Page.path}>
            <span className="home-page-icon"><I name="notes" /></span><strong title={card.Page.title}>{card.Page.title}</strong><span className="home-page-path">{card.Page.path}</span><time dateTime={card.Page.updated_at || undefined}>{updatedLabel(card.Page.updated_at)}</time>
          </a>)}</div>}
        </section>
      </div>
      <aside className="home-tree">
        <Tree groups={groups} />
      </aside>
    </div>
  );
}
