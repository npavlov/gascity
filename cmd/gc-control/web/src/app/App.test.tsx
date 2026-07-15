import { StrictMode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";
import type {
  ControlCenterAPI,
  ConvoyDetail,
  ConvoyList,
  ConvoySummary,
  ConvoysAPI,
  Health,
  Order,
  OrderList,
  OrderRunList,
  OrderRunOutput,
} from "@/lib/api";
import type { EventSourceFactory, EventSourceLike } from "@/lib/events";

afterEach(cleanup);

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

const health: Health = { schema_version: 1, status: "ok", city: "taxdome", supervisor_reachable: true };

function convoy(id: string, title: string, total = 1): ConvoySummary {
  return {
    id,
    title,
    status: "open",
    progress: { closed: 0, total },
    stage: { active: ["dev"], waiting: [], complete: false },
    signals: [
      { key: "fail_gate", icon: "alert", label: "Fail gate", tone: "danger", detail: "lint" },
      { key: "running", icon: "play", label: "Worker running", tone: "info" },
    ],
    problems: [],
    updated_at: "2026-07-15T10:00:00Z",
  };
}

function convoyList(items: ConvoyList["items"], overrides: Partial<ConvoyList> = {}): ConvoyList {
  return { items, degraded: false, stale: false, problems: [], ...overrides };
}

function detail(summary: ConvoySummary): ConvoyDetail {
  return {
    convoy: summary,
    beads: [{ id: `${summary.id}-dev`, title: "Dev", type: "task", status: "in_progress", metadata: {}, needs: [], blocked: false, updated_at: "2026-07-15T10:00:00Z", step_ref: "dev" }],
    sessions: [{ id: "session-1", session_name: "worker", template: "dynamic/worker", state: "running", running: true, attached: false, needs_input: false, active_bead: `${summary.id}-dev` }],
  };
}

function order(scopedName: string, name: string): Order {
  return { name, scoped_name: scopedName, type: "cooldown", enabled: true, problems: [] };
}

function orderList(items: OrderList["items"], overrides: Partial<OrderList> = {}): OrderList {
  return { items, degraded: false, stale: false, problems: [], ...overrides };
}

function fullAPI(overrides: Partial<ControlCenterAPI> = {}): ControlCenterAPI {
  const summary = convoy("convoy-1", "Feature one");
  const run = { bead_id: "run-1", store_ref: "city", status: "completed" as const, created_at: "2026-07-15T10:00:00Z", has_output: true, problems: [] };
  return {
    health: vi.fn(async () => health),
    listConvoys: vi.fn(async () => convoyList([summary])),
    getConvoy: vi.fn(async () => detail(summary)),
    listBeads: vi.fn(async () => ({ items: detail(summary).beads, degraded: false, stale: false, problems: [] })),
    listOrders: vi.fn(async (): Promise<OrderList> => ({
      items: [{ name: "review", scoped_name: "city/review", type: "cooldown", enabled: true, last_run: run, problems: [] }],
      degraded: false,
      stale: false,
      problems: [],
    })),
    listOrderHistory: vi.fn(async (): Promise<OrderRunList> => ({ items: [run], degraded: false, stale: false, problems: [] })),
    getOrderRunOutput: vi.fn(async (): Promise<OrderRunOutput> => ({ bead_id: "run-1", store_ref: "city", created_at: run.created_at, labels: [], output: "review output" })),
    ...overrides,
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

  it("renders one root tool frame with controlled Convoys and Orders workspaces", async () => {
    const api = fullAPI();
    const { container } = render(<App api={api} />);

    expect(await screen.findByRole("heading", { name: "taxdome", level: 1 })).toBeVisible();
    expect(container.querySelectorAll('[data-layout="tool-frame"]')).toHaveLength(1);
    expect(container.querySelectorAll("main")).toHaveLength(1);
    expect(screen.getByRole("tab", { name: "Convoys" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "Orders" })).toBeVisible();
    expect(await screen.findByRole("heading", { name: "Feature one", level: 2 })).toBeVisible();
  });

  it("shows simultaneous convoy signals and hides percentage progress when total is zero", async () => {
    const summary = convoy("convoy-zero", "Zero denominator", 0);
    render(<App api={fullAPI({ listConvoys: vi.fn(async () => convoyList([summary])), getConvoy: vi.fn(async () => detail(summary)) })} />);

    expect(await screen.findByRole("heading", { name: "Zero denominator", level: 2 })).toBeVisible();
    expect(screen.getAllByText("Fail gate").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Worker running").length).toBeGreaterThan(0);
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(screen.getByText("Progress unavailable")).toBeVisible();
  });

  it("preserves selection across tabs and falls back to the previous item after refresh", async () => {
    const user = userEvent.setup();
    const first = convoy("a", "Feature A");
    const second = convoy("b", "Feature B");
    const listConvoys = vi
      .fn<ConvoysAPI["listConvoys"]>()
      .mockResolvedValueOnce(convoyList([first, second]))
      .mockResolvedValueOnce(convoyList([first, second]))
      .mockResolvedValue(convoyList([first]));
    const api = fullAPI({ listConvoys, getConvoy: vi.fn(async (id) => detail(id === "b" ? second : first)) });
    render(<App api={api} />);

    await user.click(await screen.findByRole("button", { name: "Select convoy Feature B" }));
    expect(await screen.findByRole("heading", { name: "Feature B", level: 2 })).toBeVisible();
    await user.click(screen.getByRole("tab", { name: "Orders" }));
    await user.click(screen.getByRole("tab", { name: "Convoys" }));
    expect(screen.getByRole("button", { name: "Select convoy Feature B" })).toHaveAttribute("aria-pressed", "true");

    act(() => window.dispatchEvent(new Event("focus")));
    expect(await screen.findByRole("heading", { name: "Feature A", level: 2 })).toBeVisible();
    expect(screen.getByRole("button", { name: "Select convoy Feature A" })).toHaveAttribute("aria-pressed", "true");
  });

  it("falls forward to the next convoy when the selected item disappears", async () => {
    const user = userEvent.setup();
    const first = convoy("a", "Feature A");
    const second = convoy("b", "Feature B");
    const third = convoy("c", "Feature C");
    const listConvoys = vi
      .fn<ConvoysAPI["listConvoys"]>()
      .mockResolvedValueOnce(convoyList([first, second, third]))
      .mockResolvedValue(convoyList([first, third]));
    render(<App api={fullAPI({ listConvoys, getConvoy: vi.fn(async (id) => detail(id === "c" ? third : id === "b" ? second : first)) })} />);

    await user.click(await screen.findByRole("button", { name: "Select convoy Feature B" }));
    act(() => window.dispatchEvent(new Event("focus")));
    expect(await screen.findByRole("heading", { name: "Feature C", level: 2 })).toBeVisible();
    expect(screen.getByRole("button", { name: "Select convoy Feature C" })).toHaveAttribute("aria-pressed", "true");
  });

  it("preserves order selection across tabs and falls forward after removal", async () => {
    const user = userEvent.setup();
    const first = order("city/a", "Order A");
    const second = order("city/b", "Order B");
    const third = order("city/c", "Order C");
    const listOrders = vi
      .fn(async (): Promise<OrderList> => orderList([first, second, third]))
      .mockResolvedValueOnce(orderList([first, second, third]))
      .mockResolvedValueOnce(orderList([first, second, third]))
      .mockResolvedValue(orderList([first, third]));
    const api = fullAPI({ listOrders, listOrderHistory: vi.fn(async () => ({ items: [], degraded: false, stale: false, problems: [] })) });
    render(<App api={api} />);

    await user.click(await screen.findByRole("tab", { name: "Orders" }));
    await user.click(await screen.findByRole("button", { name: "Select order Order B" }));
    expect(await screen.findByRole("heading", { name: "Order B", level: 2 })).toBeVisible();
    await user.click(screen.getByRole("tab", { name: "Convoys" }));
    await user.click(screen.getByRole("tab", { name: "Orders" }));
    expect(screen.getByRole("button", { name: "Select order Order B" })).toHaveAttribute("aria-pressed", "true");

    act(() => window.dispatchEvent(new Event("focus")));
    expect(await screen.findByRole("heading", { name: "Order C", level: 2 })).toBeVisible();
    expect(screen.getByRole("button", { name: "Select order Order C" })).toHaveAttribute("aria-pressed", "true");
  });

  it("keeps partial or stale lists usable and distinguishes empty and hard-error states", async () => {
    const partial = fullAPI({
      listConvoys: vi.fn(async () => convoyList([convoy("a", "Cached feature")], {
        degraded: true,
        stale: true,
        problems: [{ code: "upstream_partial", source: "convoys", detail: "one store unavailable", retryable: true }],
      })),
    });
    const partialRender = render(<App api={partial} />);
    expect(await screen.findByText("Showing last confirmed convoy data")).toBeVisible();
    expect(screen.getByText("one store unavailable")).toBeVisible();
    partialRender.unmount();

    const emptyRender = render(<App api={fullAPI({ listConvoys: vi.fn(async () => convoyList([])) })} />);
    expect(await screen.findByRole("heading", { name: "No convoys" })).toBeVisible();
    emptyRender.unmount();

    render(<App api={fullAPI({ listConvoys: vi.fn(async () => { throw new Error("offline"); }) })} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load convoys");
  });

  it("keeps inactive-tab last-confirmed data stale until remount refreshes it", async () => {
    const user = userEvent.setup();
    class CacheEventSource implements EventSourceLike {
      listener: ((event: MessageEvent<string>) => void) | undefined;
      onerror: ((event: Event) => void) | null = null;
      addEventListener(_type: string, listener: (event: MessageEvent<string>) => void) { this.listener = listener; }
      removeEventListener() { this.listener = undefined; }
      close() { return undefined; }
      emit() { this.listener?.(new MessageEvent("invalidate", { data: JSON.stringify({ resources: ["convoys"], cursor: "20" }) })); }
    }
    const source = new CacheEventSource();
    const summary = convoy("convoy-1", "Cached feature");
    const listConvoys = vi.fn<ConvoysAPI["listConvoys"]>()
      .mockResolvedValueOnce(convoyList([summary]))
      .mockRejectedValue(new Error("offline"));
    render(<App api={fullAPI({ listConvoys, getConvoy: vi.fn(async () => detail(summary)) })} eventSourceFactory={() => source} />);
    await screen.findByRole("heading", { name: "Cached feature", level: 2 });

    await user.click(screen.getByRole("tab", { name: "Orders" }));
    act(() => source.emit());
    await user.click(screen.getByRole("tab", { name: "Convoys" }));
    expect(await screen.findByRole("heading", { name: "Cached feature", level: 2 })).toBeVisible();
    expect(await screen.findByText("Showing last confirmed convoy data")).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Unable to load convoys" })).not.toBeInTheDocument();
  });

  it("loads order output only after an explicit click and forwards both identities", async () => {
    const user = userEvent.setup();
    const getOrderRunOutput = vi.fn(async (): Promise<OrderRunOutput> => ({ bead_id: "run-1", store_ref: "city", created_at: "now", labels: [], output: "captured output" }));
    const api = fullAPI({ getOrderRunOutput });
    render(<App api={api} />);

    await user.click(await screen.findByRole("tab", { name: "Orders" }));
    expect(await screen.findByRole("heading", { name: "review", level: 2 })).toBeVisible();
    expect(getOrderRunOutput).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "View output for run-1" }));
    expect(await screen.findByText("captured output")).toBeVisible();
    expect(getOrderRunOutput).toHaveBeenCalledWith("run-1", "city", expect.any(AbortSignal));
  });

  it("aborts and hides output from the previously selected exact order run", async () => {
    const user = userEvent.setup();
    const first = order("city/a", "Order A");
    const second = order("rig/b", "Order B");
    const run = { bead_id: "same-id", store_ref: "city", status: "completed" as const, created_at: "now", has_output: true, problems: [] };
    let outputSignal: AbortSignal | undefined;
    let resolveOutput: ((value: OrderRunOutput) => void) | undefined;
    const getOrderRunOutput = vi.fn((_beadID: string, _storeRef: string, signal?: AbortSignal) => {
      outputSignal = signal;
      return new Promise<OrderRunOutput>((resolve) => { resolveOutput = resolve; });
    });
    const api = fullAPI({
      listOrders: vi.fn(async () => orderList([first, second])),
      listOrderHistory: vi.fn(async (scopedName) => ({ items: scopedName === "city/a" ? [run] : [], degraded: false, stale: false, problems: [] })),
      getOrderRunOutput,
    });
    render(<App api={api} />);

    await user.click(await screen.findByRole("tab", { name: "Orders" }));
    await user.click(await screen.findByRole("button", { name: "View output for same-id" }));
    await user.click(screen.getByRole("button", { name: "Select order Order B" }));
    expect(outputSignal?.aborted).toBe(true);
    await act(async () => {
      resolveOutput?.({ bead_id: "same-id", store_ref: "city", created_at: "now", labels: [], output: "old order output" });
      await Promise.resolve();
    });
    expect(screen.queryByText("old order output")).not.toBeInTheDocument();
    expect(screen.queryByText("same-id")).not.toBeInTheDocument();
  });

  it("cleans up StrictMode event streams and refreshes on invalidation", async () => {
    class AppEventSource implements EventSourceLike {
      listeners = new Set<(event: MessageEvent<string>) => void>();
      onerror: ((event: Event) => void) | null = null;
      closed = false;
      addEventListener(_type: string, listener: (event: MessageEvent<string>) => void) { this.listeners.add(listener); }
      removeEventListener(_type: string, listener: (event: MessageEvent<string>) => void) { this.listeners.delete(listener); }
      close() { this.closed = true; }
      emit(resources: string[]) { this.listeners.forEach((listener) => listener(new MessageEvent("invalidate", { data: JSON.stringify({ resources, cursor: "42" }) }))); }
    }
    const sources: AppEventSource[] = [];
    const factory: EventSourceFactory = () => {
      const source = new AppEventSource();
      sources.push(source);
      return source;
    };
    const api = fullAPI();
    const { rerender, unmount } = render(<StrictMode><App api={api} eventSourceFactory={factory} /></StrictMode>);
    await screen.findByRole("heading", { name: "Feature one", level: 2 });
    expect(sources.length).toBeGreaterThanOrEqual(1);
    const firstSource = sources[0];
    rerender(<StrictMode><App api={api} eventSourceFactory={(url) => factory(url)} /></StrictMode>);
    await waitFor(() => expect(sources.length).toBeGreaterThan(1));
    expect(firstSource.closed).toBe(true);
    const callsBefore = vi.mocked(api.listConvoys!).mock.calls.length;
    act(() => sources.at(-1)?.emit(["convoys"]));
    await waitFor(() => expect(vi.mocked(api.listConvoys!).mock.calls.length).toBeGreaterThan(callsBefore));
    unmount();
    expect(sources.at(-1)?.closed).toBe(true);
  });

  it("keeps data stale until the selected detail also refreshes successfully", async () => {
    class StaleEventSource implements EventSourceLike {
      listener: ((event: MessageEvent<string>) => void) | undefined;
      onerror: ((event: Event) => void) | null = null;
      addEventListener(_type: string, listener: (event: MessageEvent<string>) => void) { this.listener = listener; }
      removeEventListener() { this.listener = undefined; }
      close() { return undefined; }
      emit() { this.listener?.(new MessageEvent("invalidate", { data: JSON.stringify({ resources: ["convoys"], cursor: "12" }) })); }
    }
    const source = new StaleEventSource();
    const base = convoy("convoy-1", "Feature one");
    const getConvoy = vi.fn(async () => detail(base));
    const api = fullAPI({ getConvoy });
    render(<App api={api} eventSourceFactory={() => source} />);
    await screen.findByRole("heading", { name: "Feature one", level: 2 });
    getConvoy.mockRejectedValueOnce(new Error("detail unavailable"));
    act(() => source.emit());
    expect(await screen.findByText("Showing last confirmed convoy data")).toBeVisible();
    expect(screen.getByRole("heading", { name: "Feature one", level: 2 })).toBeVisible();
    expect(screen.getByLabelText("Tracked beads")).toBeVisible();
    expect(screen.getByText("Showing last confirmed convoy detail")).toBeVisible();
  });

  it("polls only while visible, refreshes on visibility/focus, and removes timers on cleanup", async () => {
    const visibility = Object.getOwnPropertyDescriptor(document, "visibilityState");
    const factory: EventSourceFactory = () => ({
      onerror: null,
      addEventListener() { return undefined; },
      removeEventListener() { return undefined; },
      close() { return undefined; },
    });
    try {
      Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
      const listConvoys = vi.fn(async () => convoyList([convoy("convoy-1", "Feature one")]));
      const api = fullAPI({ listConvoys });
      const view = render(<App api={api} eventSourceFactory={factory} pollInterval={10_000} />);
      await screen.findByRole("heading", { name: "Feature one", level: 2 });

      vi.useFakeTimers();
      view.rerender(<App api={api} eventSourceFactory={factory} pollInterval={20} />);
      const initialCalls = listConvoys.mock.calls.length;
      await act(async () => { await vi.advanceTimersByTimeAsync(20); });
      expect(listConvoys.mock.calls.length).toBeGreaterThan(initialCalls);

      Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
      const hiddenCalls = listConvoys.mock.calls.length;
      await act(async () => { await vi.advanceTimersByTimeAsync(60); });
      expect(listConvoys).toHaveBeenCalledTimes(hiddenCalls);

      Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
      fireEvent(document, new Event("visibilitychange"));
      await act(async () => { await Promise.resolve(); });
      expect(listConvoys.mock.calls.length).toBeGreaterThan(hiddenCalls);
      const restoredCalls = listConvoys.mock.calls.length;
      fireEvent.focus(window);
      await act(async () => { await Promise.resolve(); });
      expect(listConvoys.mock.calls.length).toBeGreaterThan(restoredCalls);

      view.unmount();
      const callsAfterUnmount = listConvoys.mock.calls.length;
      await act(async () => { await vi.advanceTimersByTimeAsync(60); });
      expect(listConvoys).toHaveBeenCalledTimes(callsAfterUnmount);
    } finally {
      vi.useRealTimers();
      if (visibility) Object.defineProperty(document, "visibilityState", visibility);
    }
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
