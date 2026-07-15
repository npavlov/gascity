import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "./App";
import type { ControlCenterAPI, Health } from "@/lib/api";

function fakeAPI(result: Health | Error | Promise<Health>): ControlCenterAPI {
  return {
    health: async () => {
      const value = await result;
      if (value instanceof Error) {
        throw value;
      }
      return value;
    },
  };
}

describe("App", () => {
  it("shows an accessible loading state while health is pending", () => {
    render(<App api={fakeAPI(new Promise<Health>(() => undefined))} />);

    expect(screen.getByRole("status", { name: "Loading Control Center" })).toHaveClass("cc-spinner");
  });

  it("renders the configured city and connected Supervisor state", async () => {
    render(
      <App
        api={fakeAPI({
          schema_version: 1,
          status: "ok",
          city: "taxdome",
          supervisor_reachable: true,
        })}
      />,
    );

    expect(await screen.findByRole("heading", { name: "taxdome" })).toBeVisible();
    expect(screen.getByText("Supervisor connected").closest(".cc-status-signal")).toHaveAttribute(
      "data-tone",
      "success",
    );
    expect(screen.getByRole("region", { name: "Control Center workspace" })).toHaveClass("cc-panel");
    expect(screen.getByRole("heading", { name: "Operator workspace is ready" })).toBeVisible();
  });

  it("keeps the city visible when the Supervisor is degraded", async () => {
    render(
      <App
        api={fakeAPI({
          schema_version: 1,
          status: "degraded",
          city: "taxdome",
          supervisor_reachable: false,
        })}
      />,
    );

    expect(await screen.findByRole("heading", { name: "taxdome" })).toBeVisible();
    expect(screen.getByText("Supervisor unavailable").closest(".cc-status-signal")).toHaveAttribute(
      "data-tone",
      "warning",
    );
  });

  it("shows an explicit connection error when health cannot be fetched", async () => {
    render(<App api={fakeAPI(new Error("request failed"))} />);

    expect(await screen.findByRole("alert")).toHaveClass("cc-panel");
    expect(screen.getByRole("alert")).toHaveTextContent("Unable to connect to Control Center");
  });
});
