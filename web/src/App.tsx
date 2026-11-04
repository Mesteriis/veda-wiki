import { useState } from "react";
import { AccessSettings } from "./features/access/AccessSettings";
import { GitHubConnect } from "./features/github/GitHubConnect";
import { FeatureContext, type FeatureName } from "./components/FeatureContext";
import { FeatureDialog } from "./components/FeatureDialog";
import { Header } from "./components/Header";
import { Sidebar } from "./components/Sidebar";
import { SourceWarnings } from "./components/SourceWarnings";
import { Results } from "./features/catalog/Results";
import { Graph } from "./features/graph/Graph";
import { Dashboard } from "./features/home/Dashboard";
import { Reading } from "./features/reading/Reading";
import { Recent } from "./features/recent/Recent";
import { useShortcut } from "./hooks/useShortcut";
import { useStatePoll } from "./hooks/useStatePoll";
import type { StatePayload } from "./api/types";
import { readerView, type View } from "./lib/view";

const linkLoginMessage = "Не удалось войти по ссылке. Проверьте ключ и адрес подключения.";

function Section({ v, data }: { v: View; data: StatePayload }) {
  if (v.SettingsView) return <AccessSettings />;
  if (v.GitHubView) return <GitHubConnect />;
  if (v.RecentView) return <Recent v={v} />;
  if (v.GraphView) return <Graph v={v} />;
  if (v.Page) return <Reading data={data} page={v.Page} />;
  if (v.Query || v.Selected) return <Results v={v} />;
  return <Dashboard v={v} />;
}

export function App({ linkLoginError = false }: { linkLoginError?: boolean }) {
  const [feature, setFeature] = useState<FeatureName | null>(null);
  // Polling restarts whenever a dialog opens or closes.
  const { data, error } = useStatePoll(feature);
  useShortcut("k", "search");
  if (!data)
    return (
      <div className="loading-screen">
        <img src="/assets/veda-logo.png" alt="" />
        <h1>VEDA</h1>
        <p>{error || "Загружаем базу знаний…"}</p>
        {linkLoginError && <p role="alert">{linkLoginMessage}</p>}
        {error && <a href="/">Вернуться на главную</a>}
      </div>
    );
  const v = readerView(data.view, window.location.search);
  return (
    <FeatureContext.Provider value={setFeature}>
      <Sidebar v={v} />
      <main>
        <Header v={v} />
        {linkLoginError && <p role="alert">{linkLoginMessage}</p>}
        <SourceWarnings view={v} />
        <Section v={v} data={data} />
      </main>
      <FeatureDialog feature={feature} isPublic={v.Public} onClose={() => setFeature(null)} />
    </FeatureContext.Provider>
  );
}
