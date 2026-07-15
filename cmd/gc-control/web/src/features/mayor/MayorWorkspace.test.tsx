import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { MayorWorkspace } from "./MayorWorkspace";
import mayorWorkspaceStyles from "./MayorWorkspace.css?raw";
import type { MayorController } from "./useMayor";
import type { MayorState, MayorView } from "@/lib/api";

afterEach(cleanup);

function controller(state: MayorState = "idle", overrides: Partial<MayorController> = {}): MayorController {
  const view: MayorView = {
    identity: "pack/named.overseer", state, lifecycle: state, materialized: state !== "available_dormant",
    running: state === "idle" || state === "in_turn", attached: false, follow_up_supported: state === "in_turn",
    degraded: false, stale: false, problems: [],
  };
  return {
    view, turns: [{ role: "assistant", text: "Mayor ready" }], loading: false, stale: false, error: null,
    failureState: null, liveTurn: null, composerDraft: "", setComposerDraft: vi.fn(), pendingDraft: "",
    setPendingDraft: vi.fn(), receipt: null, mutation: null, mutationError: null, scrollOffset: 0,
    setScrollOffset: vi.fn(), refresh: vi.fn(async () => undefined), loadOlder: vi.fn(async () => undefined),
    refreshStatus: vi.fn(async () => true), refreshSnapshots: vi.fn(async () => true),
    send: vi.fn(async () => undefined), respond: vi.fn(async () => undefined), canSend: true,
    hasOlder: false, loadingOlder: false, ...overrides,
  };
}

describe("MayorWorkspace", () => {
  it.each([
    ["available_dormant", "Ready on first message"], ["idle", "Idle"], ["in_turn", "In turn"],
    ["sleeping", "Sleeping"], ["stopped", "Stopped"], ["unsupported", "Unsupported"],
  ] as const)("renders %s distinctly", (state, label) => {
    render(<MayorWorkspace controller={controller(state)} />);
    expect(screen.getByText(label)).toBeVisible();
  });

  it("uses the Mayor icon allowlist and semantic transcript sizing", () => {
    const { container } = render(<MayorWorkspace controller={controller("idle")} />);
    expect(container.querySelector("svg.lucide-check")).toBeNull();
    expect(container.querySelector("svg.lucide-message-circle")).not.toBeNull();
    expect(mayorWorkspaceStyles).not.toContain("max-height: 60vh");
    expect(mayorWorkspaceStyles).toContain("--cc-size-mayor-transcript-max-block:");
    expect(mayorWorkspaceStyles).toContain("max-block-size: var(--cc-size-mayor-transcript-max-block)");
  });

  it.each([
    ["missing", "Mayor is not configured"], ["ambiguous", "Mayor identity is ambiguous"],
    ["disconnected", "Mayor is disconnected"],
  ] as const)("renders unresolved %s", (failureState, title) => {
    render(<MayorWorkspace controller={controller("idle", { view: null, failureState, error: title, canSend: false })} />);
    expect(screen.getByRole("heading", { name: title })).toBeVisible();
  });

  it("renders transcript without a broad live region and announces only appended live turns", () => {
    render(<MayorWorkspace controller={controller("idle", { liveTurn: { role: "assistant", text: "New live reply" } })} />);
    expect(screen.getByText("Mayor ready").closest("[aria-live]")).toBeNull();
    expect(screen.getByText("New live reply").closest('[aria-live="polite"]')).toBeTruthy();
  });

  it("submits controlled composer and pending response without terminal controls", async () => {
    const user = userEvent.setup();
    const send = vi.fn(async () => undefined);
    const respond = vi.fn(async () => undefined);
    const setComposerDraft = vi.fn();
    const pending = { request_id: "pending-1", kind: "question", prompt: "Continue?", options: ["allow"], metadata: {} };
    render(<MayorWorkspace controller={controller("idle", {
      view: { ...controller().view!, pending }, composerDraft: "hello", setComposerDraft, send, respond,
    })} />);
    await user.click(screen.getByRole("button", { name: "Send to Mayor" }));
    expect(send).toHaveBeenCalledOnce();
    await user.click(screen.getByRole("button", { name: "Answer allow" }));
    expect(respond).toHaveBeenCalledWith("allow");
    expect(screen.queryByText(/terminal|tmux/i)).not.toBeInTheDocument();
  });

  it("disables unsafe follow-up and preserves explicit busy/error states", () => {
    render(<MayorWorkspace controller={controller("in_turn", { canSend: false, mutation: "send", mutationError: "send failed" })} />);
    expect(screen.getByRole("button", { name: "Send to Mayor" })).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent("send failed");
  });

  it("disables pending controls during every serialized Mayor mutation", () => {
    const pending = { request_id: "pending-1", kind: "question", prompt: "Continue?", options: ["allow"], metadata: {} };
    render(<MayorWorkspace controller={controller("idle", {
      view: { ...controller().view!, pending },
      mutation: "send",
    })} />);
    expect(screen.getByRole("textbox", { name: "Mayor response details" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Answer allow" })).toBeDisabled();
  });

  it("keeps a failed refresh visible alongside last-good Mayor data", () => {
    render(<MayorWorkspace controller={controller("idle", {
      stale: true,
      error: "Mayor status offline",
    })} />);
    expect(screen.getByText("Last confirmed")).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("Mayor status offline");
    expect(screen.getByText("Mayor ready")).toBeVisible();
  });
});
