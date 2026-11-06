// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import { loadDiagrams, STALE_NOTE } from "./diagramLoad";

describe("loadDiagrams", () => {
  it("draws the blocks once the diagram code loads", async () => {
    document.body.innerHTML = '<pre class="mermaid">graph LR; A--&gt;B</pre>';
    const blocks = [...document.querySelectorAll<HTMLElement>("pre.mermaid")];
    const drawDiagrams = vi.fn().mockResolvedValue(undefined);
    await loadDiagrams(blocks, async () => ({ drawDiagrams }));
    expect(drawDiagrams).toHaveBeenCalledWith(blocks);
    expect(document.querySelector(".mermaid-note")).toBeNull();
  });

  it("keeps the source and asks to reload when the code is gone", async () => {
    document.body.innerHTML = '<pre class="mermaid">graph LR; A--&gt;B</pre><pre class="mermaid">sequenceDiagram</pre>';
    const blocks = [...document.querySelectorAll<HTMLElement>("pre.mermaid")];
    await loadDiagrams(blocks, () => Promise.reject(new TypeError("Failed to fetch dynamically imported module")));
    expect(document.querySelectorAll("pre.mermaid-error")).toHaveLength(2);
    const notes = [...document.querySelectorAll(".mermaid-note")];
    expect(notes.map((note) => note.textContent)).toEqual([STALE_NOTE, STALE_NOTE]);
    expect(notes[0]?.nextElementSibling).toBe(blocks[0]);
    expect(blocks[0]?.textContent).toBe("graph LR; A-->B");
  });
});
