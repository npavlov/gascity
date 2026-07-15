import { createRef } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { AlertIcon } from "../icons";
import { Button } from "../primitives/Button";
import { Badge } from "./Badge";
import { Dialog } from "./Dialog";
import { EmptyState } from "./EmptyState";
import { Panel } from "./Panel";
import { Progress } from "./Progress";
import { Skeleton } from "./Skeleton";
import { Spinner } from "./Spinner";
import { StatusSignal } from "./StatusSignal";
import { Tabs } from "./Tabs";
import { Tooltip } from "./Tooltip";

describe("UI components", () => {
  it("Badge and StatusSignal expose semantic tone with authoritative visible text", () => {
    const ref = createRef<HTMLSpanElement>();
    render(<><Badge tone="info">New</Badge><StatusSignal ref={ref} tone="danger" icon={AlertIcon} label="Gate failed" /></>);
    expect(screen.getByText("New")).toHaveAttribute("data-tone", "info");
    expect(ref.current).toHaveAttribute("data-tone", "danger");
    expect(ref.current).toHaveTextContent("Gate failed");
    expect(ref.current?.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  });

  it("Progress is labelled and clamps its value", () => {
    render(<Progress label="Convoy progress" value={120} min={10} max={90} />);
    const progress = screen.getByRole("progressbar", { name: "Convoy progress" });
    expect(progress).toHaveAttribute("aria-valuemin", "10");
    expect(progress).toHaveAttribute("aria-valuemax", "90");
    expect(progress).toHaveAttribute("aria-valuenow", "90");
  });

  it("Tabs supports selection and roving keyboard focus", async () => {
    const user = userEvent.setup();
    render(<Tabs aria-label="Work views" items={[
      { id: "summary", label: "Summary", content: "Summary content" },
      { id: "diff", label: "Diff", content: "Diff content" },
      { id: "logs", label: "Logs", content: "Logs content" },
    ]} />);
    const summary = screen.getByRole("tab", { name: "Summary" });
    const diff = screen.getByRole("tab", { name: "Diff" });
    const logs = screen.getByRole("tab", { name: "Logs" });
    summary.focus();
    await user.keyboard("{ArrowRight}");
    expect(diff).toHaveFocus();
    await user.keyboard("{End}");
    expect(logs).toHaveFocus();
    await user.keyboard("{Home}");
    expect(summary).toHaveFocus();
    await user.keyboard("{ArrowLeft}");
    expect(logs).toHaveFocus();
    await user.click(diff);
    expect(diff).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel", { name: "Diff" })).toHaveTextContent("Diff content");
  });

  it("Tooltip supplements rather than replaces the trigger name", async () => {
    const user = userEvent.setup();
    render(<Tooltip content="Refresh current data"><Button>Refresh</Button></Tooltip>);
    const trigger = screen.getByRole("button", { name: "Refresh" });
    await user.hover(trigger);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Refresh current data");
    expect(trigger).toHaveAccessibleName("Refresh");
  });

  it("Dialog is labelled, closes on Escape, and restores trigger focus", async () => {
    const user = userEvent.setup();
    render(<Dialog title="Convoy details" trigger={<Button>Open details</Button>}>Body</Dialog>);
    const trigger = screen.getByRole("button", { name: "Open details" });
    await user.click(trigger);
    expect(screen.getByRole("dialog", { name: "Convoy details" })).toBeVisible();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("Panel and EmptyState associate headings and compose actions", () => {
    const ref = createRef<HTMLElement>();
    render(<><Panel ref={ref} title="Workspace" actions={<Button>Run</Button>}>Ready</Panel><EmptyState title="No orders" description="Dispatch one." actions={<Button>Create</Button>} /></>);
    expect(ref.current).toHaveAccessibleName("Workspace");
    expect(screen.getByRole("heading", { name: "No orders" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Create" })).toBeVisible();
  });

  it("Skeleton is decorative and Spinner announces its label", () => {
    render(<><Skeleton data-testid="skeleton" /><Spinner label="Loading mail" /></>);
    expect(screen.getByTestId("skeleton")).toHaveAttribute("aria-hidden", "true");
    expect(screen.getByRole("status", { name: "Loading mail" })).toBeVisible();
  });
});
