import type { Card, Page } from "../../api/types";
import { I } from "../../components/Icon";
import { Panel } from "../../components/Panel";
import { pageHref } from "../../lib/format";

export function MorePages({ cards, pages }: { cards: Card[]; pages: Page[] }) {
  const displayed = new Set(cards.map((card) => card.Page.path));
  const more = pages.filter((page) => !displayed.has(page.path)).slice(0, 5);
  return (
    <Panel
      title="Другие страницы"
      className="popular"
      actions={<a href="/?env=all">Все страницы <I name="arrow-right" /></a>}
    >
      {more.length ? <div className="popular-list">{more.map((page) => (
        <a href={pageHref(page.path)} key={page.path}>
          <span className="tiny-tile"><I name="file-description" /></span>
          <span className="popular-label"><strong>{page.title}</strong><small>/{page.path}</small></span>
        </a>
      ))}</div> : <p className="data-note">Других страниц пока нет.</p>}
    </Panel>
  );
}
