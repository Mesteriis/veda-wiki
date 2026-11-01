import test from "node:test";
import assert from "node:assert/strict";
import { buildTree, buildMergedTree } from "./tree.js";

test("keeps source identities and nested directory names separate", () => {
  const pages = [
    { path: "alpha/README.md", title: "Root" },
    { path: "alpha/infra/servers/a.md", title: "A" },
    { path: "alpha/infra/b.md", title: "B" },
    { path: "beta/infra/servers/a.md", title: "Other" },
  ];
  const a = buildTree("alpha", pages);
  assert.equal(a.pages[0].title, "Root");
  assert.equal(a.folders.get("infra").pages[0].title, "B");
  assert.equal(a.folders.get("infra").folders.get("servers").pages[0].path, "alpha/infra/servers/a.md");
  assert.equal(buildTree("beta", pages).folders.get("infra").folders.get("servers").pages[0].title, "Other");
  assert.equal(buildTree("missing", pages).folders.size, 0);
});

test("merged tree keeps colliding paths as distinct source-qualified articles", () => {
  const groups = [
    { ID: "alpha", Name: "First", Pages: [{ path: "alpha/docs/setup.md", title: "Setup" }] },
    { ID: "beta", Name: "Second", Pages: [{ path: "beta/docs/setup.md", title: "Setup" }, { path: "alpha/hidden.md", title: "Wrong source" }] },
  ];
  const tree = buildMergedTree(groups);
  const pages = tree.folders.get("docs").pages;
  assert.deepEqual(pages.map((page) => [page.path, page.sourceID, page.sourceName]), [
    ["alpha/docs/setup.md", "alpha", "First"], ["beta/docs/setup.md", "beta", "Second"],
  ]);
  assert.equal(tree.pages.length, 0);
});
