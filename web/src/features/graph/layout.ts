// Pure position math of the knowledge graph canvas (960 px wide before zoom).

/** Pages of one source shown at once; the rest are reached by paging. */
export const GRAPH_PAGE_SIZE = 12;

export interface Point {
  x: number;
  y: number;
}

export interface LayoutGroup<P> {
  Name: string;
  Pages: P[] | null;
}

export type GraphNode<P> = P & { group: string; groupIndex: number; width: number; x: number; y: number };

// Up to three pages sit on fixed points of the cluster with wider cards.
const spacedPoints: Record<number, readonly (readonly [number, number])[]> = {
  1: [[145, 110]],
  2: [[55, 110], [240, 110]],
  3: [[145, 65], [35, 150], [255, 150]],
};

/** Top-left corner of a source cluster; the third of exactly three is centred. */
export function clusterPosition(index: number, groupCount: number): Point {
  return { x: groupCount === 3 && index === 2 ? 255 : (index % 2) * 480 + 15, y: Math.floor(index / 2) * 285 + 8 };
}

function nodeBox(index: number, count: number, origin: Point): { x: number; y: number; width: number } {
  const point = spacedPoints[count]?.[index];
  if (point) return { width: 164, x: origin.x + point[0], y: origin.y + point[1] };
  return { width: 132, x: origin.x + 30 + (index % 3) * 145, y: origin.y + 62 + Math.floor(index / 3) * 48 };
}

/** Nodes of the visible page of every group, in group order. */
export function layoutNodes<P>(groups: LayoutGroup<P>[], currentPage: number): GraphNode<P>[] {
  return groups.flatMap((group, groupIndex) => {
    const pages = (group.Pages || []).slice(currentPage * GRAPH_PAGE_SIZE, currentPage * GRAPH_PAGE_SIZE + GRAPH_PAGE_SIZE);
    const origin = clusterPosition(groupIndex, groups.length);
    return pages.map((page, index) => ({ ...page, group: group.Name, groupIndex, ...nodeBox(index, pages.length, origin) }));
  });
}

/** Number of graph pages needed for the largest group; at least one. */
export function graphPageCount<P>(groups: LayoutGroup<P>[]): number {
  return Math.max(1, ...groups.map((group) => Math.ceil((group.Pages || []).length / GRAPH_PAGE_SIZE)));
}

/** Canvas height for the given number of clusters. */
export function graphHeight(groupCount: number): number {
  return Math.max(310, Math.ceil(groupCount / 2) * 285);
}
