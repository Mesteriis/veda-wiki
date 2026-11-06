type DrawDiagrams = (blocks: HTMLElement[]) => Promise<void>;

export const STALE_NOTE = "Диаграмма не загрузилась: VEDA обновилась после открытия страницы. Обновите страницу.";

/**
 * Loads the diagram code and draws the blocks. Mermaid is large, so it loads
 * only for pages that have diagrams. When the load fails, usually because the
 * page was opened before VEDA was updated and its old files are gone, each
 * block keeps its source and says to reload the page.
 */
export async function loadDiagrams(
  blocks: HTMLElement[],
  load: () => Promise<{ drawDiagrams: DrawDiagrams }> = () => import("./diagrams"),
): Promise<void> {
  let draw: DrawDiagrams;
  try {
    ({ drawDiagrams: draw } = await load());
  } catch {
    for (const block of blocks) {
      if (!block.isConnected) continue;
      block.classList.add("mermaid-error");
      const note = document.createElement("p");
      note.className = "mermaid-note";
      note.setAttribute("role", "status");
      note.textContent = STALE_NOTE;
      block.before(note);
    }
    return;
  }
  await draw(blocks);
}
