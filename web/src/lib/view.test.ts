import { describe, expect, it } from "vitest";
import type { StateView } from "../api/types";
import { readerView } from "./view";

const base: StateView = {
  GraphEdges: null, Cards: null, SourceWarnings: null, Public: false, Groups: null, Pages: null, Page: null,
  Query: "", Selected: "", Generated: "", Withheld: 0, Count: 0, GitHubCount: 0, TOC: "",
};
const page = { path: "a/b.md", title: "B", environment: "a", sha256: "x" };

describe("readerView", () => {
  it("chooses the section from the address", () => {
    expect(readerView(base, "?settings=1")).toMatchObject({ SettingsView: true, GitHubView: false, GraphView: false, RecentView: false });
    expect(readerView(base, "?github=1").GitHubView).toBe(true);
    expect(readerView(base, "?graph=1").GraphView).toBe(true);
    expect(readerView(base, "?recent=1").RecentView).toBe(true);
    expect(readerView(base, "?graph=true").GraphView).toBe(false);
  });

  it("never opens owner sections on the public surface", () => {
    const view = readerView({ ...base, Public: true }, "?settings=1&github=1&graph=1");
    expect(view).toMatchObject({ SettingsView: false, GitHubView: false, GraphView: true });
  });

  it("lets a search or an article win over a section", () => {
    expect(readerView({ ...base, Query: "x" }, "?graph=1&recent=1&settings=1")).toMatchObject({ GraphView: false, RecentView: false, SettingsView: false });
    expect(readerView({ ...base, Page: page }, "?github=1&graph=1")).toMatchObject({ GitHubView: false, GraphView: false });
  });
});
