export interface TreeNode<P> {
  folders: Map<string, TreeNode<P>>;
  pages: P[];
}

export type MergedPage<P> = P & { sourceName: string; sourceID: string };

export interface TreeGroup<P> {
  ID: string;
  Name?: string;
  Pages: P[] | null;
}

function emptyNode<P>(): TreeNode<P> {
  return { folders: new Map(), pages: [] };
}

function folderNode<P>(node: TreeNode<P>, name: string): TreeNode<P> {
  let child = node.folders.get(name);
  if (!child) {
    child = emptyNode<P>();
    node.folders.set(name, child);
  }
  return child;
}

// The API's path starts with the source ID; keep that prefix in page links,
// but show only repository-relative directories in each source's tree.
export function buildTree<P extends { path: string }>(sourceID: string, pages: P[]): TreeNode<P> {
  const root = emptyNode<P>();
  for (const page of pages) {
    const prefix = sourceID + "/";
    if (!page.path.startsWith(prefix)) continue;
    const parts = page.path.slice(prefix.length).split("/");
    if (parts.some((part) => !part)) continue;
    let node = root;
    for (const name of parts.slice(0, -1)) {
      node = folderNode(node, name);
    }
    node.pages.push(page);
  }
  return root;
}

// Merge directory paths for browsing while keeping every article's original
// source-qualified link. Equal paths remain separate, never overwritten.
export function buildMergedTree<P extends { path: string }>(groups: TreeGroup<P>[]): TreeNode<MergedPage<P>> {
  const root = emptyNode<MergedPage<P>>();
  for (const group of groups) {
    const prefix = group.ID + "/";
    for (const page of group.Pages || []) {
      if (!page.path.startsWith(prefix)) continue;
      const parts = page.path.slice(prefix.length).split("/");
      if (parts.some((part) => !part)) continue;
      let node = root;
      for (const name of parts.slice(0, -1)) {
        node = folderNode(node, name);
      }
      node.pages.push({ ...page, sourceName: group.Name || group.ID, sourceID: group.ID });
    }
  }
  return root;
}
