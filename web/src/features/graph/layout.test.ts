import { describe, expect, it } from "vitest";
import { clusterPosition, graphHeight, graphPageCount, layoutNodes } from "./layout";

// Expected values were computed by running the formulas of the former
// Graph component of the former single-file UI; the layout must not move a single node.
function group(id: string, count: number) {
  return { ID: id, Name: id.toUpperCase(), Pages: Array.from({ length: count }, (_, i) => ({ path: `${id}/p${i}`, title: `P${i}` })) };
}
const boxes = (nodes: { x: number; y: number; width: number }[]) => nodes.map((node) => [node.x, node.y, node.width]);

describe("graph layout", () => {
  it("places one page in the middle of its cluster", () => {
    expect(boxes(layoutNodes([group("a", 1)], 0))).toEqual([[160, 118, 164]]);
  });

  it("places two pages side by side", () => {
    expect(boxes(layoutNodes([group("a", 2)], 0))).toEqual([[70, 118, 164], [255, 118, 164]]);
  });

  it("places three pages in a triangle", () => {
    expect(boxes(layoutNodes([group("a", 3)], 0))).toEqual([[160, 73, 164], [50, 158, 164], [270, 158, 164]]);
  });

  it("places twelve pages in a narrow grid of three columns", () => {
    expect(boxes(layoutNodes([group("a", 12)], 0))).toEqual([
      [45, 70, 132], [190, 70, 132], [335, 70, 132],
      [45, 118, 132], [190, 118, 132], [335, 118, 132],
      [45, 166, 132], [190, 166, 132], [335, 166, 132],
      [45, 214, 132], [190, 214, 132], [335, 214, 132],
    ]);
  });

  it("keeps the group name, group index and page fields on each node", () => {
    const [node] = layoutNodes([group("a", 1), group("b", 1)], 0).slice(1);
    expect(node).toMatchObject({ path: "b/p0", title: "P0", group: "B", groupIndex: 1 });
  });

  it("centres the third of three clusters and offsets its pages", () => {
    expect([0, 1, 2].map((i) => clusterPosition(i, 3))).toEqual([{ x: 15, y: 8 }, { x: 495, y: 8 }, { x: 255, y: 293 }]);
    expect(boxes(layoutNodes([group("a", 1), group("b", 2), group("c", 3)], 0))).toEqual([
      [160, 118, 164], [550, 118, 164], [735, 118, 164], [400, 358, 164], [290, 443, 164], [510, 443, 164],
    ]);
  });

  it("uses two columns of clusters for other group counts", () => {
    expect([0, 1, 2, 3].map((i) => clusterPosition(i, 4))).toEqual([{ x: 15, y: 8 }, { x: 495, y: 8 }, { x: 15, y: 293 }, { x: 495, y: 293 }]);
  });

  it("shows the requested slice of twelve pages", () => {
    const nodes = layoutNodes([group("a", 14)], 1);
    expect(nodes.map((node) => node.path)).toEqual(["a/p12", "a/p13"]);
    expect(boxes(nodes)).toEqual([[70, 118, 164], [255, 118, 164]]);
  });

  it("skips sources without pages", () => {
    expect(layoutNodes([{ Name: "A", Pages: null }], 0)).toEqual([]);
  });

  it("sizes the canvas and counts pages of twelve", () => {
    expect([0, 1, 2, 3, 5].map(graphHeight)).toEqual([310, 310, 310, 570, 855]);
    expect(graphPageCount([])).toBe(1);
    expect(graphPageCount([group("a", 12), group("b", 13)])).toBe(2);
    expect(graphPageCount([{ Name: "A", Pages: null }])).toBe(1);
  });
});
