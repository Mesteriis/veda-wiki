import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles/fonts.css";
import "./styles/tokens.css";
import "./styles/base.css";
import "./components/shell.css";
import "./components/sidebar.css";
import "./components/header.css";
import "./styles/dashboard.css";
import "./styles/reading.css";
import "./styles/responsive-wide.css";
import "./styles/responsive-narrow.css";
import "./styles/alignment.css";
import "./styles/sections.css";
import "./features/recent/recent.css";
import "./features/github/github.css";
import "./features/access/access.css";
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
