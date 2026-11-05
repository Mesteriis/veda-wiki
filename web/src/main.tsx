import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles/fonts.css";
import "./styles/tokens.css";
import "./styles/base.css";
import "./components/shell.css";
import "./components/sidebar.css";
import "./components/sidebar-rail.css";
import "./components/header.css";
import "./features/reading/reading.css";
import "./features/catalog/catalog.css";
import "./features/home/home.css";
import "./features/session/session.css";
import "./features/graph/graph.css";
import "./features/knowledge/knowledge.css";
import "./features/recent/recent.css";
import "./features/github/github.css";
import "./features/access/access.css";
import "./features/mcp/mcp.css";
import { loginFromLink } from "./api/session";

loginFromLink(window.location, window.history, window.fetch.bind(window)).then((linkLoginError) => {
  const root = document.getElementById("root");
  if (!root) throw new Error("VEDA: #root element is missing");
  createRoot(root).render(
    <StrictMode>
      <App linkLoginError={linkLoginError} />
    </StrictMode>,
  );
});
