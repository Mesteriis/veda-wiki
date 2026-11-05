import type { StateView } from "../api/types";

/** The server view plus the section chosen by the address parameters. */
export interface View extends StateView {
  SettingsView: boolean;
  GitHubView: boolean;
  GraphView: boolean;
  RecentView: boolean;
  ApiView: boolean;
}

// A search or an article always wins over a section parameter; owner sections
// are never chosen on the public surface.
export function readerView(view: StateView, search: string): View {
  const params = new URLSearchParams(search);
  const plain = !view.Query && !view.Page;
  return {
    ...view,
    SettingsView: !view.Public && params.get("settings") === "1" && plain,
    GitHubView: !view.Public && params.get("github") === "1" && plain,
    GraphView: params.get("graph") === "1" && plain,
    RecentView: params.get("recent") === "1" && plain,
    ApiView: !view.Public && params.get("api") === "1" && plain,
  };
}

// The sources a state shows identify what the session may read. When it
// changes between two polls (the session expired, another tab signed out or
// in, access was changed), data fetched for the previous scope must go.
export function readingScope(view: StateView): string {
  const sources = (view.Groups ?? []).map((group) => group.ID).sort();
  return JSON.stringify([view.Public, sources]);
}
