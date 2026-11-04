import type { StateView } from "../api/types";

/** The server view plus the section chosen by the address parameters. */
export interface View extends StateView {
  SettingsView: boolean;
  GitHubView: boolean;
  GraphView: boolean;
  RecentView: boolean;
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
  };
}
