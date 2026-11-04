import { useState } from "react";
import { I } from "../../components/Icon";
import { pageHref } from "../../lib/format";
import { clearVisits, readVisits, visibleVisits, type Visit } from "../../lib/recent";
import type { View } from "../../lib/view";

export function Recent({ v }: { v: View }) {
  const [entries, setEntries] = useState<Visit[]>(() => { try { return readVisits(window.sessionStorage); } catch { return []; } });
  const pages = visibleVisits(entries, v.Groups || []);
  function clear() { try { clearVisits(window.sessionStorage); } catch { /* Storage may be unavailable. */ } setEntries([]); }
  return <section className="recent-history panel" aria-labelledby="recent-history-title">
    <div className="recent-history-heading"><div><h2 id="recent-history-title">История просмотров</h2><p>История хранится в этой вкладке и очищается при выходе.</p></div>{pages.length > 0 && <button onClick={clear}>Очистить историю</button>}</div>
    {pages.length ? <div className="recent-history-list">{pages.map((page) => <a className="recent-history-row" key={page.path} href={pageHref(page.path)}><span className="home-page-icon"><I name="file-text" /></span><span className="recent-history-title"><strong>{page.title}</strong><small>{page.sourceName} / {page.path}</small></span><time dateTime={new Date(page.visitedAt).toISOString()}>{new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'short' }).format(page.visitedAt)}</time><I name="arrow-right" /></a>)}</div> : <div className="recent-history-empty"><I name="clock" /><h3>Здесь пока пусто</h3><p>Откройте статью, и она появится в истории этой вкладки.</p><a href="/?env=all">Перейти к статьям <I name="arrow-right" /></a></div>}
  </section>;
}
