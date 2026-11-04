import { useRef, type KeyboardEvent } from "react";
import type { GraphEdge, GraphNode } from "../../api/knowledgeTypes";
import { nextNode, type Direction, type KnowledgeGraphData, type Layout, type Point } from "./graphLayout";
import { entityTypeLabel, relationLabel } from "./labels";

export type Selection = { kind: "node" | "edge"; id: string } | null;

const NODE_RADIUS = 20;
const keys: Record<string, Direction> = { ArrowLeft: "left", ArrowRight: "right", ArrowUp: "up", ArrowDown: "down" };

export function nodeName(node: GraphNode): string {
  return (node.kind === "document" ? "Документ" : entityTypeLabel(node.type)) + " " + node.label;
}

// The edge stops at the border of its target so the arrow head stays visible.
function endpoints(from: Point, to: Point): [Point, Point] {
  const dx = to.x - from.x, dy = to.y - from.y;
  const length = Math.hypot(dx, dy) || 1;
  const cut = (NODE_RADIUS + 4) / length;
  return [{ x: from.x + dx * cut, y: from.y + dy * cut }, { x: to.x - dx * cut, y: to.y - dy * cut }];
}

function short(label: string): string {
  return label.length > 22 ? label.slice(0, 21) + "…" : label;
}

function Edge({ edge, from, to, selected, onSelect }: { edge: GraphEdge; from: Point; to: Point; selected: boolean; onSelect: () => void }) {
  const [a, b] = endpoints(from, to);
  const relation = edge.kind === "relation";
  const className = "kg-edge kg-edge-" + edge.kind + (edge.status === "uncertain" ? " kg-edge-uncertain" : "") + (selected ? " selected" : "");
  return <g className={className} onClick={onSelect}>
    <line className="kg-edge-hit" x1={a.x} y1={a.y} x2={b.x} y2={b.y} />
    <line className="kg-edge-line" x1={a.x} y1={a.y} x2={b.x} y2={b.y} markerEnd={relation ? "url(#kg-arrow)" : undefined} />
    {relation && <text className="kg-edge-label" x={(a.x + b.x) / 2} y={(a.y + b.y) / 2 - 6} textAnchor="middle">{relationLabel(edge.relation)}</text>}
  </g>;
}

interface CanvasProps {
  graph: KnowledgeGraphData;
  layout: Layout;
  center: string;
  scale: number;
  selection: Selection;
  onSelect: (selection: Selection) => void;
}

// SVG drawing of the graph. Documents are squares, entities are circles;
// relation edges carry an arrow from head to tail and their type. Nodes are
// one tab stop: arrow keys move between them, Enter or Space selects one.
export function KnowledgeCanvas({ graph, layout, center, scale, selection, onSelect }: CanvasProps) {
  const refs = useRef(new Map<string, SVGGElement>());
  const selectedNode = selection?.kind === "node" ? selection.id : null;
  const current = selectedNode && layout.positions.has(selectedNode) ? selectedNode : center;
  function keyDown(event: KeyboardEvent<SVGGElement>, id: string) {
    const direction = keys[event.key];
    if (direction) {
      event.preventDefault();
      const target = nextNode(layout.positions, id, direction);
      if (target) refs.current.get(target)?.focus();
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onSelect({ kind: "node", id });
    }
  }
  return <svg className="kg-canvas" width={layout.width * scale} height={layout.height * scale} viewBox={`0 0 ${layout.width} ${layout.height}`} role="group" aria-label="Граф знаний: стрелки — перемещение, Enter — выбор">
    <defs>
      <marker id="kg-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
        <path d="M0 0L10 5L0 10z" />
      </marker>
    </defs>
    <g aria-hidden="true">{graph.edges.map((edge) => {
      const from = layout.positions.get(edge.from), to = layout.positions.get(edge.to);
      if (!from || !to) return null;
      return <Edge key={edge.id} edge={edge} from={from} to={to} selected={selection?.kind === "edge" && selection.id === edge.id} onSelect={() => onSelect({ kind: "edge", id: edge.id })} />;
    })}</g>
    {graph.nodes.map((node) => {
      const point = layout.positions.get(node.id);
      if (!point) return null;
      const document = node.kind === "document";
      const className = "kg-node kg-node-" + node.kind + (node.id === center ? " kg-center" : "") + (node.id === selectedNode ? " selected" : "");
      return <g key={node.id} ref={(element) => { if (element) refs.current.set(node.id, element); else refs.current.delete(node.id); }}
        className={className} transform={`translate(${point.x} ${point.y})`} role="button" tabIndex={node.id === current ? 0 : -1}
        aria-label={nodeName(node) + (node.id === center ? ", центр" : "")} aria-pressed={node.id === selectedNode}
        onClick={() => onSelect({ kind: "node", id: node.id })} onKeyDown={(event) => keyDown(event, node.id)}>
        <title>{nodeName(node)}</title>
        {document ? <rect x={-NODE_RADIUS + 2} y={-NODE_RADIUS + 2} width={2 * NODE_RADIUS - 4} height={2 * NODE_RADIUS - 4} rx="4" /> : <circle r={NODE_RADIUS} />}
        <text className="kg-node-glyph" y="5" textAnchor="middle">{document ? "Д" : entityTypeLabel(node.type).slice(0, 1)}</text>
        <text className="kg-node-label" y={NODE_RADIUS + 16} textAnchor="middle">{short(node.label)}</text>
      </g>;
    })}
  </svg>;
}
