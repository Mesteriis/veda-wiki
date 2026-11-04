// Source-qualified paths keep identical folders in different repositories distinct.
export interface CatalogGroup<P extends { path: string }> {
  ID: string;
  Pages: P[] | null;
}

export interface CatalogSelection {
  sourceID: string;
  prefix: string;
}

export function catalogPages<P extends { path: string }>(groups: CatalogGroup<P>[], pages: P[] | null, selection: CatalogSelection | null): P[] {
  if (!selection) return pages || [];
  const source = groups.find((group) => group.ID === selection.sourceID);
  return (source?.Pages || []).filter((page) => page.path.startsWith(selection.prefix));
}
