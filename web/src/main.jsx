import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App.jsx";
import "./styles.css";
import { loginFromLink } from "./api/session";

loginFromLink(window.location, window.history, window.fetch.bind(window)).then((linkLoginError) => {
  createRoot(document.getElementById("root")).render(
    <React.StrictMode>
      <App linkLoginError={linkLoginError} />
    </React.StrictMode>,
  );
});
