// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";

vi.mock("mermaid", () => ({ default: { initialize: vi.fn(), render: vi.fn() } }));

import mermaid from "mermaid";
import { drawDiagrams, sizedSvg } from "./mermaid";

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
    vi.mocked(mermaid.render).mockResolvedValueOnce({ svg } as Awaited<ReturnType<typeof mermaid.render>>).mockRejectedValueOnce(new Error("parse"));
    document.body.innerHTML = '<pre class="mermaid">graph LR; A--&gt;B</pre><pre class="mermaid">bad</pre>';
    await drawDiagrams([...document.querySelectorAll<HTMLElement>("pre.mermaid")]);
    const image = document.querySelector("img.mermaid-diagram") as HTMLImageElement;
    expect(image.src.startsWith("data:image/svg+xml")).toBe(true);
    expect(image.width).toBe(121);
    expect(document.querySelector("pre.mermaid-error")?.textContent).toBe("bad");
    expect(vi.mocked(mermaid.render).mock.calls[0]?.[1]).toBe("graph LR; A-->B");
  });
});
