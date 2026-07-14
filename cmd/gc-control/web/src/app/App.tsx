import { useEffect, useState } from "react";

import type { ControlCenterAPI, Health } from "@/lib/api";

type AppState =
  | { kind: "loading" }
  | { kind: "ready"; health: Health }
  | { kind: "error" };

export interface AppProps {
  api: ControlCenterAPI;
}

export function App({ api }: AppProps) {
  const [state, setState] = useState<AppState>({ kind: "loading" });

  useEffect(() => {
    let active = true;
    api
      .health()
      .then((health) => {
        if (active) setState({ kind: "ready", health });
      })
      .catch(() => {
        if (active) setState({ kind: "error" });
      });
    return () => {
      active = false;
    };
  }, [api]);

  if (state.kind === "loading") {
    return (
      <main className="app-state" role="status" aria-label="Loading Control Center">
        <p>Loading Control Center…</p>
      </main>
    );
  }
  if (state.kind === "error") {
    return (
      <main className="app-state app-state--error" role="alert">
        <h1>Unable to connect to Control Center</h1>
        <p>The local server did not return its typed health response.</p>
      </main>
    );
  }

  const connected = state.health.supervisor_reachable;
  return (
    <main className="app-shell">
      <header className="app-header">
        <p className="app-eyebrow">GasCity Control Center</p>
        <h1>{state.health.city}</h1>
        <p className={connected ? "health health--ok" : "health health--degraded"}>
          <span aria-hidden="true">{connected ? "●" : "▲"}</span>{" "}
          {connected ? "Supervisor connected" : "Supervisor unavailable"}
        </p>
      </header>
      <section aria-label="Control Center workspace" className="app-workspace">
        <p>The operator workspace is ready.</p>
      </section>
    </main>
  );
}
