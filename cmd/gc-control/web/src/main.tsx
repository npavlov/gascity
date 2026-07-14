import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "@/app/App";
import { api } from "@/lib/api";
import "@/styles/global.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("Control Center root element is missing");
}

createRoot(root).render(
  <StrictMode>
    <App api={api} />
  </StrictMode>,
);
