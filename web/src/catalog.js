// Source-qualified paths keep identical folders in different repositories distinct.
export function catalogPages(groups, pages, selection) {
  if (!selection) return pages || [];
  const source = groups.find((group) => group.ID === selection.sourceID);
  return (source?.Pages || []).filter((page) => page.path.startsWith(selection.prefix));
}
