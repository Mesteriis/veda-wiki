export const recentKey = 'veda.recent.v1';
const limit = 50;

export interface Visit {
  path: string;
  visitedAt: number;
}

/** The part of Web Storage that history uses. */
export interface VisitStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export interface VisibleGroup<P extends { path: string }> {
  Name?: string;
  Pages: P[] | null;
}

function isVisit(item: unknown): item is Visit {
  if (typeof item !== 'object' || item === null) return false;
  const { path, visitedAt } = item as Partial<Visit>;
  return typeof path === 'string' && path !== '' && typeof visitedAt === 'number' && Number.isFinite(visitedAt) && Math.abs(visitedAt) <= 8640000000000000;
}

export function recordVisit(entries: Visit[], path: unknown, visitedAt: number): Visit[] {
  if (typeof path !== 'string' || !path || !Number.isFinite(visitedAt)) return entries;
  return [{ path, visitedAt }, ...entries.filter((item) => item.path !== path)].slice(0, limit);
}
export function readVisits(storage: Pick<VisitStorage, 'getItem'>): Visit[] {
  try {
    const data: unknown = JSON.parse(storage.getItem(recentKey) || '[]');
    return Array.isArray(data) ? data.filter(isVisit).slice(0, limit) : [];
  } catch { return []; }
}
export function saveVisits(storage: Pick<VisitStorage, 'setItem'>, entries: Visit[]): void {
  try { storage.setItem(recentKey, JSON.stringify(entries)); } catch { /* Reading must remain available when storage is disabled. */ }
}
export function clearVisits(storage: Pick<VisitStorage, 'removeItem'>): void {
  try { storage.removeItem(recentKey); } catch { /* Storage can be disabled. */ }
}
export function visibleVisits<P extends { path: string }>(entries: Visit[], groups: VisibleGroup<P>[]): (P & { sourceName: string | undefined; visitedAt: number })[] {
  const visible = new Map(groups.flatMap((group) => (group.Pages || []).map((page): [string, P & { sourceName: string | undefined }] => [page.path, { ...page, sourceName: group.Name }])));
  return entries.flatMap((item) => {
    const page = visible.get(item.path);
    return page ? [{ ...page, visitedAt: item.visitedAt }] : [];
  });
}
