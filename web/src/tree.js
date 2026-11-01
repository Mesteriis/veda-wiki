// The API's path starts with the source ID; keep that prefix in page links,
// but show only repository-relative directories in each source's tree.
export function buildTree(sourceID, pages) {
  const root = { folders: new Map(), pages: [] };
  for (const page of pages) {
    const prefix = sourceID + "/";
    if (!page.path.startsWith(prefix)) continue;
    const parts = page.path.slice(prefix.length).split("/");
    if (parts.some((part) => !part)) continue;
    let node = root;
    for (const name of parts.slice(0, -1)) {
      if (!node.folders.has(name)) node.folders.set(name, { folders: new Map(), pages: [] });
      node = node.folders.get(name);
    }
    node.pages.push(page);
  }
  return root;
}
