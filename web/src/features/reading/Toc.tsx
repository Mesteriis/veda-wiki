// Table of contents rendered by the server from the article headings.
export function Toc({ html }: { html: string }) {
  return (
    <aside className="toc panel">
      <h2>На этой странице</h2>
      <div dangerouslySetInnerHTML={{ __html: html }} />
    </aside>
  );
}
