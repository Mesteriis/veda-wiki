/** A diagram the reading page asks the diagram frame to draw. */
export type DiagramRequest = { id: number; text: string };
/** A drawn diagram: standalone SVG markup sized from its viewBox. */
export type Drawing = { markup: string; width: number; height: number };
/** The frame's answer; drawing is absent when the diagram failed to draw. */
export type DiagramReply = { id: number; drawing?: Drawing };
/** Turns Mermaid source into a drawing or rejects. */
export type Render = (text: string) => Promise<Drawing>;

const FRAME = "/diagram-frame.html";
const START_TIMEOUT = 15_000;
const DRAW_TIMEOUT = 30_000;

let frame: Promise<Window> | null = null;
let counter = 0;
const pending = new Map<number, (drawing: Drawing | undefined) => void>();

/**
 * Opens the hidden diagram frame once. Mermaid needs inline styles to lay a
 * diagram out, and only that frame's Content-Security-Policy allows them.
 */
function openFrame(): Promise<Window> {
  frame ??= new Promise<Window>((resolve, reject) => {
    const element = document.createElement("iframe");
    element.className = "diagram-frame";
    element.tabIndex = -1;
    element.setAttribute("aria-hidden", "true");
    const timer = setTimeout(() => {
      frame = null;
      element.remove();
      reject(new Error("diagram frame did not start"));
    }, START_TIMEOUT);
    window.addEventListener("message", (event: MessageEvent<unknown>) => {
      if (event.origin !== location.origin || !element.contentWindow || event.source !== element.contentWindow) return;
      const data = event.data as ({ ready?: boolean } & Partial<DiagramReply>) | null;
      if (data?.ready) {
        clearTimeout(timer);
        resolve(element.contentWindow);
      } else if (typeof data?.id === "number") {
        pending.get(data.id)?.(validDrawing(data.drawing));
        pending.delete(data.id);
      }
    });
    element.src = FRAME;
    document.body.append(element);
  });
  return frame;
}

/** Draws a diagram in the hidden frame. */
export const frameRender: Render = async (text) => {
  const target = await openFrame();
  const id = ++counter;
  const drawing = await new Promise<Drawing | undefined>((resolve) => {
    pending.set(id, resolve);
    setTimeout(() => {
      if (pending.delete(id)) resolve(undefined);
    }, DRAW_TIMEOUT);
    const request: DiagramRequest = { id, text };
    target.postMessage(request, location.origin);
  });
  if (!drawing) throw new Error("diagram failed");
  return drawing;
};

function validDrawing(value: unknown): Drawing | undefined {
  const drawing = value as Partial<Drawing> | null | undefined;
  if (typeof drawing?.markup !== "string" || typeof drawing.width !== "number" || typeof drawing.height !== "number") return undefined;
  return { markup: drawing.markup, width: drawing.width, height: drawing.height };
}

/**
 * Replaces each <pre class="mermaid"> with the drawn diagram. The SVG
 * carries its own <style> and style attributes, which the page's policy
 * forbids even in parsed markup, so the page never parses it: it is shown
 * as an isolated image. A diagram that fails to draw keeps its source text.
 */
export async function drawDiagrams(blocks: HTMLElement[], render: Render = frameRender): Promise<void> {
  for (const block of blocks) {
    if (!block.isConnected) continue;
    try {
      const sized = await render(block.textContent ?? "");
      if (!block.isConnected) continue;
      const image = new Image(sized.width, sized.height);
      image.className = "mermaid-diagram";
      image.alt = "Диаграмма";
      image.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(sized.markup);
      block.replaceWith(image);
    } catch {
      block.classList.add("mermaid-error");
      block.title = "Диаграмму не удалось построить, показан её исходный текст";
    }
  }
}
