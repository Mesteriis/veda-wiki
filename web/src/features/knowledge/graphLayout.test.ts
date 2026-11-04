import { describe, expect, it } from "vitest";
import type { GraphEdge, GraphNode } from "../../api/knowledgeTypes";
import { subgraph } from "../../test/knowledge";
import { levels, mergeGraphs, nextNode, normalizeGraph, radialLayout, type Point } from "./graphLayout";

const node = (id: string): GraphNode => ({ id, kind: "entity", type: "service", label: id });
const edge = (id: string, from: string, to: string): GraphEdge => ({ id, kind: "relation", from, to, relation: "uses", status: "extracted", evidence: [] });

describe("knowledge graph layout", () => {
  it("places the centre in the middle and neighbours on rings by distance", () => {
    const graph = normalizeGraph(subgraph());
    const depth = levels(graph, "ent-billing");
    expect(depth.get("ent-billing")).toBe(0);
    expect(depth.get("ent-db1")).toBe(1);
    expect(depth.get("doc-1")).toBe(1);
    const layout = radialLayout(graph, "ent-billing");
    expect(layout.positions.get("ent-billing")).toEqual({ x: layout.width / 2, y: layout.height / 2 });
    const centre = layout.positions.get("ent-billing") as Point;
    const distances = ["ent-db1", "ent-redis", "doc-1"].map((id) => { const p = layout.positions.get(id) as Point; return Math.round(Math.hypot(p.x - centre.x, p.y - centre.y)); });
    expect(new Set(distances).size).toBe(1);
  });

  it("puts unreachable nodes on an outer ring and keeps every node on the canvas", () => {
    const graph = { nodes: [node("a"), node("b"), node("lonely")], edges: [edge("e", "a", "b")], truncated: false };
    expect(levels(graph, "a").get("lonely")).toBe(2);
    const layout = radialLayout(graph, "a");
    for (const point of layout.positions.values()) {
      expect(point.x).toBeGreaterThanOrEqual(0);
      expect(point.x).toBeLessThanOrEqual(layout.width);
      expect(point.y).toBeGreaterThanOrEqual(0);
      expect(point.y).toBeLessThanOrEqual(layout.height);
    }
  });

  it("moves to the nearest node in the arrow direction", () => {
    const positions = new Map<string, Point>([["c", { x: 100, y: 100 }], ["r", { x: 200, y: 110 }], ["far", { x: 400, y: 100 }], ["u", { x: 100, y: 20 }], ["d", { x: 100, y: 200 }]]);
    expect(nextNode(positions, "c", "right")).toBe("r");
    expect(nextNode(positions, "c", "up")).toBe("u");
    expect(nextNode(positions, "c", "down")).toBe("d");
    expect(nextNode(positions, "c", "left")).toBeNull();
    expect(nextNode(positions, "missing", "left")).toBeNull();
  });

  it("merges expanded neighbours without duplicates and within the server limits", () => {
    const base = { nodes: [node("a"), node("b")], edges: [edge("ab", "a", "b")], truncated: false };
    const extra = { nodes: [node("b"), node("c")], edges: [edge("ab", "a", "b"), edge("bc", "b", "c"), edge("bx", "b", "x")], truncated: false };
    const merged = mergeGraphs(base, extra);
    expect(merged.nodes.map((item) => item.id)).toEqual(["a", "b", "c"]);
    expect(merged.edges.map((item) => item.id)).toEqual(["ab", "bc"]);
    expect(merged.truncated).toBe(false);
    const many = { nodes: Array.from({ length: 250 }, (_, i) => node("n" + i)), edges: [], truncated: false };
    const capped = mergeGraphs(base, many);
    expect(capped.nodes).toHaveLength(200);
    expect(capped.truncated).toBe(true);
  });
});
