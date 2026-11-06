// Draws Mermaid diagrams; the page loads this only when it has any. Without
// JavaScript, or if a diagram is invalid, its source text stays visible.
const blocks = [...document.querySelectorAll("pre.mermaid")];
if (blocks.length) {
  try {
    const { default: mermaid } = await import("https://cdn.jsdelivr.net/npm/mermaid@12.2.0/dist/mermaid.esm.min.mjs");
    const dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    mermaid.initialize({ startOnLoad: false, securityLevel: "strict", suppressErrorRendering: true, theme: dark ? "dark" : "default" });
    await mermaid.run({ nodes: blocks, suppressErrors: true });
  } catch {
    // The diagram source remains readable.
  }
}
