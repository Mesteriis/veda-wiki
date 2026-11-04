import type { Card } from "../../api/types";
import { I } from "../../components/Icon";
import { Panel } from "../../components/Panel";
import { pageHref, updatedLabel } from "../../lib/format";

export function PageCards({ cards }: { cards: Card[] }) {
  return (
    <Panel
      title="Недавно обновлённые"
      className="recent-pages"
      actions={
        <a href="/?env=all">
          Смотреть все <I name="arrow-right" />
        </a>
      }
    >
      <div className="recent-grid">
        {cards.map((c) => (
          <a
            className="page-card"
            key={c.Page.path}
            href={pageHref(c.Page.path)}
          >
            <img className="page-cover" src={c.ImageURL || "/assets/" + c.Image} alt="" />
            <div className="page-card-copy">
              <strong title={c.Page.title}>{c.Page.title}</strong>
              <small className="card-path">/{c.Page.path}</small>
              <p>{c.Description}</p>
              <div className="tags">
                <span>#{c.Tag}</span>
                <span>#wiki</span>
              </div>
              <div className="card-meta">
                <I name="clock" />
                <time dateTime={c.Page.updated_at || undefined}>{updatedLabel(c.Page.updated_at)}</time>
              </div>
            </div>
          </a>
        ))}
      </div>
    </Panel>
  );
}
