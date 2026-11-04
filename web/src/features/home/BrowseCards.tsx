import { I } from "../../components/Icon";

export function BrowseCards() {
  return (
    <section className="browse-cards" aria-label="Навигация по базе знаний">
      <a className="browse-card" href="/?env=all">
        <span className="tile"><I name="notes" /></span>
        <strong>Библиотека</strong><small>Все статьи базы знаний</small>
      </a>
      <a className="browse-card" href="/?q=документация">
        <span className="tile"><I name="search" /></span>
        <strong>Найти ответ</strong><small>Поиск по документации</small>
      </a>
      <a className="browse-card" href="#knowledge-tree">
        <span className="tile"><I name="folder" /></span>
        <strong>Разделы</strong><small>Источники документации</small>
      </a>
    </section>
  );
}
