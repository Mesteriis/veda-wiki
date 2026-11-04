import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles.css";
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
