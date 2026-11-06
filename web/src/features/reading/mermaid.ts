import mermaid from "mermaid";

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

let counter = 0;

/** Sizes the SVG from its viewBox so the image keeps the diagram's proportions. */
export function sizedSvg(svg: string): { markup: string; width: number; height: number } | null {
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

/**
 * Replaces each <pre class="mermaid"> with the drawn diagram. The page's
 * Content-Security-Policy forbids inline styles, so the SVG, which carries
 * its own <style>, is shown as an isolated image. A diagram that fails to
 * parse keeps its source text.
 */
export async function drawDiagrams(blocks: HTMLElement[]): Promise<void> {
  for (const block of blocks) {
    if (!block.isConnected) continue;
    const id = `veda-mermaid-${++counter}`;
    try {
      const { svg } = await mermaid.render(id, block.textContent ?? "");
      const sized = sizedSvg(svg);
      if (!sized || !block.isConnected) throw new Error("diagram has no size");
      const image = new Image(sized.width, sized.height);
      image.className = "mermaid-diagram";
      image.alt = "Диаграмма";
      image.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(sized.markup);
      block.replaceWith(image);
    } catch {
      // Mermaid can leave its temporary drawing behind when parsing fails.
      for (const leftover of [document.getElementById(id), document.getElementById("d" + id)]) leftover?.remove();
      block.classList.add("mermaid-error");
      block.title = "Диаграмму не удалось построить, показан её исходный текст";
    }
  }
}
