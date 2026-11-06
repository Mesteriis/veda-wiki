import mermaid from "mermaid";
import type { DiagramReply, DiagramRequest, Drawing } from "./diagrams";

// This module runs inside the hidden diagram frame. Mermaid sets inline
// styles while it lays a diagram out; the frame's own Content-Security-Policy
// allows them, so the reading page keeps its strict policy and a clean console.
mermaid.initialize({
  startOnLoad: false,
  securityLevel: "strict",
  // A parse error keeps the source text instead of drawing an error picture.
  suppressErrorRendering: true,
  theme: "dark",
  // Plain SVG text: an <img> does not render HTML labels inside foreignObject.
  htmlLabels: false,
  flowchart: { htmlLabels: false },
});

/**
 * Sizes the SVG from its viewBox so the image keeps the diagram's proportions.
 * It parses markup with style attributes, so it runs here, not on the page.
 */
export function sizedSvg(svg: string): Drawing | null {
  const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
  const root = doc.documentElement;
  if (root.nodeName !== "svg") return null;
  const box = (root.getAttribute("viewBox") ?? "").trim().split(/[\s,]+/).map(Number);
  const [, , boxWidth = 0, boxHeight = 0] = box;
  if (box.length !== 4 || !box.every(Number.isFinite) || boxWidth <= 0 || boxHeight <= 0) return null;
  const [width, height] = [Math.ceil(boxWidth), Math.ceil(boxHeight)];
  root.setAttribute("width", String(width));
  root.setAttribute("height", String(height));
  root.removeAttribute("style");
  return { markup: new XMLSerializer().serializeToString(root), width, height };
}

type Render = (id: string, text: string) => Promise<{ svg: string }>;

/** Draws one requested diagram; a reply without a drawing means it failed. */
export async function answer(request: DiagramRequest, render: Render = (id, text) => mermaid.render(id, text)): Promise<DiagramReply> {
  const id = `veda-mermaid-${request.id}`;
  try {
    const { svg } = await render(id, request.text);
    const drawing = sizedSvg(svg);
    return drawing ? { id: request.id, drawing } : { id: request.id };
  } catch {
    // Mermaid can leave its temporary drawing behind when parsing fails.
    for (const leftover of [document.getElementById(id), document.getElementById("d" + id)]) leftover?.remove();
    return { id: request.id };
  }
}

/** Accepts a request only from the reading page that framed this one. */
export function request(event: MessageEvent<unknown>): DiagramRequest | null {
  if (event.source !== window.parent || event.origin !== location.origin) return null;
  const data = event.data as Partial<DiagramRequest> | null;
  if (typeof data?.id !== "number" || typeof data.text !== "string") return null;
  return { id: data.id, text: data.text };
}

if (window.parent !== window) {
  window.addEventListener("message", (event: MessageEvent<unknown>) => {
    const asked = request(event);
    if (asked) void answer(asked).then((reply) => window.parent.postMessage(reply, location.origin));
  });
  window.parent.postMessage({ ready: true }, location.origin);
}
