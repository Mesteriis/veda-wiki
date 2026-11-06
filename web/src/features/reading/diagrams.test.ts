// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";

vi.mock("mermaid", () => ({ default: { initialize: vi.fn(), render: vi.fn() } }));

import { drawDiagrams } from "./diagrams";
import { answer, request, sizedSvg } from "./diagramFrame";

const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="100%" style="max-width: 120px" viewBox="0 0 120.4 80"><style>.a{fill:red}</style><rect class="a"/></svg>';

describe("sizedSvg", () => {
  it("sizes the SVG from its viewBox and keeps its own styles", () => {
    const sized = sizedSvg(svg);
    expect(sized).toMatchObject({ width: 121, height: 80 });
    expect(sized?.markup).toContain('width="121"');
    expect(sized?.markup).toContain("<style>");
    expect(sized?.markup).not.toContain("max-width");
  });

  it("refuses markup without a usable viewBox", () => {
    expect(sizedSvg('<svg xmlns="http://www.w3.org/2000/svg"/>')).toBeNull();
    expect(sizedSvg("<div/>")).toBeNull();
  });
});

describe("drawDiagrams", () => {
  it("replaces a block with an image and keeps the source on failure", async () => {
    const render = vi.fn().mockResolvedValueOnce(sizedSvg(svg)).mockRejectedValueOnce(new Error("parse"));
    document.body.innerHTML = '<pre class="mermaid">graph LR; A--&gt;B</pre><pre class="mermaid">bad</pre>';
    await drawDiagrams([...document.querySelectorAll<HTMLElement>("pre.mermaid")], render);
    const image = document.querySelector("img.mermaid-diagram") as HTMLImageElement;
    expect(image.src.startsWith("data:image/svg+xml")).toBe(true);
    expect(image.width).toBe(121);
    expect(document.querySelector("pre.mermaid-error")?.textContent).toBe("bad");
    expect(render.mock.calls[0]?.[0]).toBe("graph LR; A-->B");
  });
});

describe("diagram frame", () => {
  it("answers with the drawing, or without svg and leftovers on failure", async () => {
    expect(await answer({ id: 3, text: "graph LR; A-->B" }, async () => ({ svg }))).toEqual({ id: 3, drawing: sizedSvg(svg) });
    expect(await answer({ id: 5, text: "graph" }, async () => ({ svg: "<svg/>" }))).toEqual({ id: 5 });
    document.body.innerHTML = '<div id="dveda-mermaid-4"></div>';
    expect(await answer({ id: 4, text: "bad" }, async () => Promise.reject(new Error("parse")))).toEqual({ id: 4 });
    expect(document.getElementById("dveda-mermaid-4")).toBeNull();
  });

  it("accepts requests only from its own page", () => {
    const event = (data: unknown, origin = location.origin, source: unknown = window.parent) =>
      ({ data, origin, source }) as MessageEvent<unknown>;
    expect(request(event({ id: 1, text: "graph" }))).toEqual({ id: 1, text: "graph" });
    expect(request(event({ id: 1, text: "graph" }, "https://evil.example"))).toBeNull();
    expect(request(event({ id: 1, text: "graph" }, location.origin, {}))).toBeNull();
    expect(request(event({ id: "1", text: "graph" }))).toBeNull();
    expect(request(event(null))).toBeNull();
  });
});
