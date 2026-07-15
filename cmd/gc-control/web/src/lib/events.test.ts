import { afterEach, describe, expect, it, vi } from "vitest";

import { createInvalidationFeed, createRefreshQueue } from "./events";
import type { EventSourceFactory, EventSourceLike } from "./events";

class FakeEventSource implements EventSourceLike {
  readonly listeners = new Map<string, Set<(event: MessageEvent<string>) => void>>();
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  closed = false;

  addEventListener(type: string, listener: (event: MessageEvent<string>) => void) {
    const listeners = this.listeners.get(type) ?? new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: (event: MessageEvent<string>) => void) {
    this.listeners.get(type)?.delete(listener);
  }

  close() {
    this.closed = true;
  }

  emit(type: string, data: unknown) {
    const event = new MessageEvent(type, { data: JSON.stringify(data) });
    this.listeners.get(type)?.forEach((listener) => listener(event));
  }

  emitRaw(type: string, data: string) {
    const event = new MessageEvent(type, { data });
    this.listeners.get(type)?.forEach((listener) => listener(event));
  }

  fail() {
    this.onerror?.(new Event("error"));
  }

  open() {
    this.onopen?.(new Event("open"));
  }
}

afterEach(() => {
  vi.useRealTimers();
});

describe("createInvalidationFeed", () => {
  it("decodes typed invalidations, repeats the 10s cap, and resets after a valid event", () => {
    vi.useFakeTimers();
    const sources: FakeEventSource[] = [];
    const factory: EventSourceFactory = (url) => {
      expect(url).toBe("/api/v1/events");
      const source = new FakeEventSource();
      sources.push(source);
      return source;
    };
    const invalidations = vi.fn();
    const stale = vi.fn();
    const stop = createInvalidationFeed({ factory, onInvalidate: invalidations, onStale: stale });

    sources[0].emit("invalidate", { resources: ["orders", "convoys"], cursor: "42" });
    expect(invalidations).toHaveBeenCalledWith(["convoys", "orders"]);
    sources[0].fail();
    expect(stale).toHaveBeenCalledOnce();
    expect(sources[0].closed).toBe(true);
    vi.advanceTimersByTime(999);
    expect(sources).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(sources).toHaveLength(2);

    sources[1].fail();
    vi.advanceTimersByTime(2_000);
    sources[2].fail();
    vi.advanceTimersByTime(4_000);
    sources[3].fail();
    vi.advanceTimersByTime(8_000);
    sources[4].fail();
    vi.advanceTimersByTime(10_000);
    expect(sources).toHaveLength(6);
    sources[5].fail();
    vi.advanceTimersByTime(9_999);
    expect(sources).toHaveLength(6);
    vi.advanceTimersByTime(1);
    expect(sources).toHaveLength(7);

    sources[6].emit("invalidate", { resources: ["orders"], cursor: "43" });
    sources[6].fail();
    vi.advanceTimersByTime(999);
    expect(sources).toHaveLength(7);
    vi.advanceTimersByTime(1);
    expect(sources).toHaveLength(8);

    stop();
    expect(sources[7].closed).toBe(true);
    sources[7].fail();
    vi.advanceTimersByTime(20_000);
    expect(sources).toHaveLength(8);
  });

  it("requests reconciliation only after a failed feed reconnects", () => {
    vi.useFakeTimers();
    const sources: FakeEventSource[] = [];
    const reconnected = vi.fn();
    const stop = createInvalidationFeed({
      factory: () => {
        const source = new FakeEventSource();
        sources.push(source);
        return source;
      },
      onInvalidate: vi.fn(),
      onReconnect: reconnected,
      onStale: vi.fn(),
    });

    sources[0].open();
    expect(reconnected).not.toHaveBeenCalled();
    sources[0].fail();
    vi.advanceTimersByTime(1_000);
    sources[1].open();
    sources[1].open();
    expect(reconnected).toHaveBeenCalledOnce();
    stop();
  });

  it("decodes Mail and ignores malformed or unrelated invalidations", () => {
    const source = new FakeEventSource();
    const invalidations = vi.fn();
    const stop = createInvalidationFeed({ factory: () => source, onInvalidate: invalidations, onStale: vi.fn() });

    source.emit("invalidate", { resources: ["mail"], cursor: "9" });
    source.emit("invalidate", { resources: ["convoys"], cursor: "" });
    source.emit("invalidate", { resources: null, cursor: "10" });
    source.emitRaw("invalidate", "{");
    expect(invalidations).toHaveBeenCalledOnce();
    expect(invalidations).toHaveBeenCalledWith(["mail"]);
    stop();
  });

  it("detaches listeners so late StrictMode-style messages cannot invalidate after cleanup", () => {
    const source = new FakeEventSource();
    const invalidations = vi.fn();
    const stop = createInvalidationFeed({ factory: () => source, onInvalidate: invalidations, onStale: vi.fn() });
    const lateListener = [...(source.listeners.get("invalidate") ?? [])][0];

    stop();
    lateListener?.(new MessageEvent("invalidate", { data: JSON.stringify({ resources: ["convoys"], cursor: "11" }) }));
    source.fail();
    expect(invalidations).not.toHaveBeenCalled();
    expect(source.onerror).toBeNull();
  });
});

describe("createRefreshQueue", () => {
  it("coalesces one in-flight refresh into one trailing union rerun", async () => {
    let release: (() => void) | undefined;
    const calls: string[][] = [];
    const refresh = vi.fn(async (resources: string[]) => {
      calls.push(resources);
      if (calls.length === 1) await new Promise<void>((resolve) => { release = resolve; });
    });
    const queue = createRefreshQueue(refresh);

    queue.request(["convoys"]);
    queue.request(["orders"]);
    queue.request(["convoys"]);
    expect(calls).toEqual([["convoys"]]);
    release?.();
    await queue.idle();
    expect(calls).toEqual([["convoys"], ["convoys", "orders"]]);

    queue.dispose();
    queue.request(["orders"]);
    await queue.idle();
    expect(calls).toHaveLength(2);
  });

  it("records refresh rejection, still drains the trailing batch, and aborts on dispose", async () => {
    let attempt = 0;
    let aborted = false;
    const queue = createRefreshQueue(async (_resources, signal) => {
      attempt += 1;
      if (attempt === 1) throw new Error("refresh failed");
      await new Promise<void>((resolve) => signal.addEventListener("abort", () => { aborted = true; resolve(); }, { once: true }));
    });
    queue.request(["convoys"]);
    queue.request(["orders"]);
    await Promise.resolve();
    expect(queue.lastError()).toEqual(new Error("refresh failed"));
    queue.dispose();
    await queue.idle();
    expect(attempt).toBe(2);
    expect(aborted).toBe(true);
  });
});
