// Pure layout and navigation math of the knowledge graph canvas.
import type { GraphEdge, GraphNode, Subgraph } from "../../api/knowledgeTypes";
import { KNOWLEDGE_LIMITS } from "../../api/knowledgeTypes";

export interface Point {
  x: number;
  y: number;
}

export interface Layout {
  positions: Map<string, Point>;
  width: number;
  height: number;
}

/** A graph with null lists replaced by empty ones. */
export interface KnowledgeGraphData {
  nodes: GraphNode[];
  edges: GraphEdge[];
  truncated: boolean;
}

const RING = 170;
const GAP = 120;
const MARGIN = 120;
const MIN_WIDTH = 720;
const MIN_HEIGHT = 440;

export function normalizeGraph(graph: Subgraph): KnowledgeGraphData {
  return { nodes: graph.nodes ?? [], edges: graph.edges ?? [], truncated: graph.truncated };
}

/** Breadth-first distance of every node from the centre over edges in either direction; unreachable nodes go one ring further out. */
export function levels(graph: KnowledgeGraphData, center: string): Map<string, number> {
  const adjacent = new Map<string, string[]>();
  for (const edge of graph.edges) {
    adjacent.set(edge.from, [...(adjacent.get(edge.from) ?? []), edge.to]);
    adjacent.set(edge.to, [...(adjacent.get(edge.to) ?? []), edge.from]);
  }
  const known = new Set(graph.nodes.map((node) => node.id));
  const level = new Map<string, number>();
  if (known.has(center)) {
    level.set(center, 0);
    const queue = [center];
    for (let next = queue.shift(); next !== undefined; next = queue.shift()) {
      const depth = level.get(next) ?? 0;
      for (const neighbour of adjacent.get(next) ?? []) {
        if (known.has(neighbour) && !level.has(neighbour)) {
          level.set(neighbour, depth + 1);
          queue.push(neighbour);
        }
      }
    }
  }
  const outer = Math.max(0, ...level.values()) + 1;
  for (const node of graph.nodes) if (!level.has(node.id)) level.set(node.id, outer);
  return level;
}

// The centre sits in the middle; each ring is wide enough for its nodes, and
// nodes of a ring are ordered by the angle of their parent so branches stay together.
export function radialLayout(graph: KnowledgeGraphData, center: string): Layout {
  const level = levels(graph, center);
  const rings = new Map<number, string[]>();
  for (const node of graph.nodes) {
    const depth = level.get(node.id) ?? 0;
    rings.set(depth, [...(rings.get(depth) ?? []), node.id]);
  }
  const angle = new Map<string, number>();
  const radius = new Map<number, number>();
  const order = [...rings.keys()].sort((a, b) => a - b);
  let previous = 0;
  for (const depth of order) {
    const ids = rings.get(depth) ?? [];
    if (depth === 0 && ids.length === 1) {
      radius.set(0, 0);
      angle.set(ids[0] ?? "", 0);
      continue;
    }
    const parentAngle = (id: string) => {
      const parents = graph.edges.flatMap((edge) => edge.from === id ? [edge.to] : edge.to === id ? [edge.from] : [])
        .filter((other) => (level.get(other) ?? 0) < depth && angle.has(other));
      return parents.length ? (angle.get(parents[0] ?? "") ?? 0) : 0;
    };
    const sorted = [...ids].sort((a, b) => parentAngle(a) - parentAngle(b) || a.localeCompare(b));
    const r = Math.max(previous + RING, (sorted.length * GAP) / (2 * Math.PI));
    radius.set(depth, r);
    previous = r;
    sorted.forEach((id, index) => angle.set(id, -Math.PI / 2 + (2 * Math.PI * index) / sorted.length));
  }
  const outer = Math.max(0, ...radius.values());
  const width = Math.max(MIN_WIDTH, Math.ceil(2 * (outer + MARGIN)));
  const height = Math.max(MIN_HEIGHT, Math.ceil(2 * (outer + MARGIN)));
  const positions = new Map<string, Point>();
  for (const node of graph.nodes) {
    const r = radius.get(level.get(node.id) ?? 0) ?? 0;
    const a = angle.get(node.id) ?? 0;
    positions.set(node.id, { x: Math.round(width / 2 + r * Math.cos(a)), y: Math.round(height / 2 + r * Math.sin(a)) });
  }
  return { positions, width, height };
}

export type Direction = "left" | "right" | "up" | "down";

const unit: Record<Direction, Point> = { left: { x: -1, y: 0 }, right: { x: 1, y: 0 }, up: { x: 0, y: -1 }, down: { x: 0, y: 1 } };

/** The node an arrow key moves to: the nearest one in that direction, preferring nodes straight ahead. */
export function nextNode(positions: Map<string, Point>, from: string, direction: Direction): string | null {
  const start = positions.get(from);
  if (!start) return null;
  const d = unit[direction];
  let best: string | null = null;
  let bestCost = Infinity;
  for (const [id, point] of positions) {
    if (id === from) continue;
    const dx = point.x - start.x, dy = point.y - start.y;
    const ahead = dx * d.x + dy * d.y;
    if (ahead <= 0) continue;
    const aside = Math.abs(dx * d.y - dy * d.x);
    const cost = ahead + 2 * aside;
    if (cost < bestCost || (cost === bestCost && best !== null && id < best)) {
      best = id;
      bestCost = cost;
    }
  }
  return best;
}

/** Adds the neighbours of an expanded node, within the server's node and edge limits. */
export function mergeGraphs(base: KnowledgeGraphData, extra: KnowledgeGraphData): KnowledgeGraphData {
  const nodes = [...base.nodes];
  const ids = new Set(nodes.map((node) => node.id));
  let truncated = base.truncated || extra.truncated;
  for (const node of extra.nodes) {
    if (ids.has(node.id)) continue;
    if (nodes.length >= KNOWLEDGE_LIMITS.nodesMax) { truncated = true; continue; }
    nodes.push(node);
    ids.add(node.id);
  }
  const edges = [...base.edges];
  const edgeIDs = new Set(edges.map((edge) => edge.id));
  for (const edge of extra.edges) {
    if (edgeIDs.has(edge.id) || !ids.has(edge.from) || !ids.has(edge.to)) continue;
    if (edges.length >= KNOWLEDGE_LIMITS.edgesMax) { truncated = true; continue; }
    edges.push(edge);
    edgeIDs.add(edge.id);
  }
  return { nodes, edges, truncated };
}
