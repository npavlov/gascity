import { useEffect, useState } from "react";

import type { ControlCenterAPI, Health } from "@/lib/api";
import {
  AlertIcon,
  CheckIcon,
  DetailHeader,
  EmptyState,
  Panel,
  Spinner,
  StatusSignal,
  Text,
  ToolFrame,
} from "@/ui";

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
      <main className="app-state">
        <Spinner label="Loading Control Center" />
        <Text>Loading Control Center…</Text>
      </main>
    );
  }
  if (state.kind === "error") {
    return (
      <main className="app-state">
        <Panel className="app-state__panel" role="alert" title="Unable to connect to Control Center">
          <Text>The local server did not return its typed health response.</Text>
        </Panel>
      </main>
    );
  }

  const connected = state.health.supervisor_reachable;
  return (
    <ToolFrame
      header={
        <DetailHeader
          title={state.health.city}
          subtitle="GasCity Control Center"
          status={
            <StatusSignal
              label={connected ? "Supervisor connected" : "Supervisor unavailable"}
              tone={connected ? "success" : "warning"}
              icon={connected ? CheckIcon : AlertIcon}
            />
          }
        />
      }
    >
      <Panel className="app-workspace" title="Control Center workspace">
        <EmptyState
          title="Operator workspace is ready"
          description="Convoys, orders, Mayor, and mail will appear here as their projections connect."
        />
      </Panel>
    </ToolFrame>
  );
}
