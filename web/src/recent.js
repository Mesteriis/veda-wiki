export const recentKey = 'veda.recent.v1';
const limit = 50;

export function recordVisit(entries, path, visitedAt) {
  if (typeof path !== 'string' || !path || !Number.isFinite(visitedAt)) return entries;
  return [{ path, visitedAt }, ...entries.filter((item) => item.path !== path)].slice(0, limit);
}
export function readVisits(storage) {
  try {
    const data = JSON.parse(storage.getItem(recentKey) || '[]');
    return Array.isArray(data) ? data.filter((item) => item && typeof item.path === 'string' && item.path && Number.isFinite(item.visitedAt) && Math.abs(item.visitedAt) <= 8640000000000000).slice(0, limit) : [];
  } catch { return []; }
}
export function saveVisits(storage, entries) {
  try { storage.setItem(recentKey, JSON.stringify(entries)); } catch { /* Reading must remain available when storage is disabled. */ }
}
export function clearVisits(storage) {
  try { storage.removeItem(recentKey); } catch { /* Storage can be disabled. */ }
}
export function visibleVisits(entries, groups) {
  const visible = new Map(groups.flatMap((group) => (group.Pages || []).map((page) => [page.path, { ...page, sourceName: group.Name }])));
  return entries.filter((item) => visible.has(item.path)).map((item) => ({ ...visible.get(item.path), visitedAt: item.visitedAt }));
}
