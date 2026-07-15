import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { connectMayorEvents, useMayor } from "./useMayor";
import type { MayorStreamCallbacks, MayorStreamConnector } from "./useMayor";
import { createControlCenterAPI, MayorRequestError } from "@/lib/api";
import type { MayorAPI, MayorEvent, MayorView, TranscriptPage } from "@/lib/api";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const view: MayorView = {
  identity: "pack/named.overseer",
  state: "idle",
  lifecycle: "active",
  activity: "idle",
  session_id: "session-1",
  session_name: "runtime",
  materialized: true,
  running: true,
  attached: false,
  follow_up_supported: true,
  degraded: false,
  stale: false,
  problems: [],
};

const transcript = (text = "existing"): TranscriptPage => ({
  turns: [{ role: "assistant", text, timestamp: "2026-07-15T10:00:00Z" }],
  has_older: true,
  before: "older",
  returned: 1,
  total: 2,
  degraded: false,
  stale: false,
  problems: [],
});

function fakeMayorAPI(overrides: Partial<MayorAPI> = {}): MayorAPI {
  return {
    getMayor: vi.fn(async () => view),
    getMayorTranscript: vi.fn(async () => transcript()),
    sendMayorMessage: vi.fn(async () => ({ request_id: "request-1", status: "succeeded", intent: "default" as const, queued: false })),
    respondMayorInteraction: vi.fn(async () => ({ session_id: "session-1", status: "accepted" })),
    ...overrides,
  };
}

function controlledConnector(): { connector: MayorStreamConnector; callbacks: () => MayorStreamCallbacks; closed: () => boolean } {
  let current: MayorStreamCallbacks | undefined;
  let isClosed = false;
  return {
    connector(callbacks) {
      current = callbacks;
      return () => { isClosed = true; };
    },
    callbacks() {
      if (!current) throw new Error("connector not mounted");
      return current;
    },
    closed: () => isClosed,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((next, fail) => { resolve = next; reject = fail; });
  return { promise, resolve, reject };
}

describe("useMayor", () => {
  it("preserves the typed Mayor problem detail for visible identity failures", async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({
      type: "urn:gascity:control-center:mayor:mayor_identity_ambiguous",
      title: "Conflict",
      status: 409,
      detail: "configured Mayor identity appears more than once in Supervisor status",
    }), { status: 409, headers: { "content-type": "application/problem+json" } })) as unknown as typeof fetch;
    const client = createControlCenterAPI({ baseUrl: "http://control.test", fetch: fetcher });

    await expect(client.getMayor()).rejects.toMatchObject({
      code: "mayor_identity_ambiguous",
      status: 409,
      message: "configured Mayor identity appears more than once in Supervisor status",
    });
  });

  it.each([
    ["mayor_not_configured", "missing"],
    ["mayor_identity_ambiguous", "ambiguous"],
  ] as const)("maps structured %s failures to %s", async (code, failureState) => {
    const api = fakeMayorAPI({
      getMayor: vi.fn(async () => { throw new MayorRequestError(code, 409, code); }),
    });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));
    expect(hook.result.current.failureState).toBe(failureState);
  });

  it("loads snapshots, appends live turns, and never optimistically delivers a send", async () => {
    let resolveSend: ((value: { request_id: string; status: string; intent: "default"; queued: boolean }) => void) | undefined;
    const sendMayorMessage = vi.fn(() => new Promise<{ request_id: string; status: string; intent: "default"; queued: boolean }>((resolve) => { resolveSend = resolve; }));
    const api = fakeMayorAPI({ sendMayorMessage });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));

    await waitFor(() => expect(hook.result.current.turns).toHaveLength(1));
    act(() => hook.result.current.setComposerDraft("new message"));
    let pending!: Promise<void>;
    act(() => { pending = hook.result.current.send(); });
    expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["existing"]);

    await act(async () => {
      resolveSend?.({ request_id: "request-1", status: "succeeded", intent: "default", queued: false });
      await pending;
    });
    expect(sendMayorMessage).toHaveBeenCalledWith("new message");
    expect(hook.result.current.composerDraft).toBe("");
    expect(hook.result.current.receipt?.request_id).toBe("request-1");

    act(() => feed.callbacks().onEvent({ kind: "turn", cursor: "9", resources: ["transcript"], turn: { role: "assistant", text: "live" } }));
    expect(hook.result.current.turns.at(-1)?.text).toBe("live");
    expect(hook.result.current.liveTurn?.text).toBe("live");
    hook.unmount();
    expect(feed.closed()).toBe(true);
  });

  it("marks last-good state stale and refreshes every snapshot before reconnect clears it", async () => {
    const getMayor = vi.fn(async () => view);
    const getMayorTranscript = vi.fn(async () => transcript());
    const api = fakeMayorAPI({ getMayor, getMayorTranscript });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));
    const viewCalls = getMayor.mock.calls.length;
    const transcriptCalls = getMayorTranscript.mock.calls.length;

    act(() => feed.callbacks().onStale());
    expect(hook.result.current.stale).toBe(true);
    await act(async () => { await feed.callbacks().onReconnect(); });
    expect(getMayor.mock.calls.length).toBeGreaterThan(viewCalls);
    expect(getMayorTranscript.mock.calls.length).toBeGreaterThan(transcriptCalls);
    expect(hook.result.current.stale).toBe(false);
  });

  it("pauses polling while hidden and refreshes on focus restore", async () => {
    vi.useFakeTimers();
    const getMayor = vi.fn(async () => view);
    const api = fakeMayorAPI({ getMayor });
    const feed = controlledConnector();
    const hook = renderHook(({ visible }) => useMayor({ api, visible, connector: feed.connector, pollInterval: 20 }), { initialProps: { visible: false } });
    await act(async () => { await Promise.resolve(); });
    const hiddenCalls = getMayor.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(60); });
    expect(getMayor).toHaveBeenCalledTimes(hiddenCalls);

    hook.rerender({ visible: true });
    await act(async () => { window.dispatchEvent(new Event("focus")); await Promise.resolve(); });
    expect(getMayor.mock.calls.length).toBeGreaterThan(hiddenCalls);
  });

  it("merges older transcript chronologically without duplicates", async () => {
    const older: TranscriptPage = { ...transcript("older turn"), has_older: false, before: undefined };
    const getMayorTranscript = vi.fn(async (before?: string) => before ? older : transcript());
    const feed = controlledConnector();
    const api = fakeMayorAPI({ getMayorTranscript });
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.turns).toHaveLength(1));
    await act(async () => { await hook.result.current.loadOlder(); });
    expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["older turn", "existing"]);
    await act(async () => { await hook.result.current.loadOlder(); });
    expect(hook.result.current.turns).toHaveLength(2);
  });

  it("updates activity and pending hints without accepting malformed resources", async () => {
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api: fakeMayorAPI(), visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.view).not.toBeNull());
    const events: MayorEvent[] = [
      { kind: "activity", activity: "in-turn", resources: ["mayor"] },
      { kind: "pending", resources: ["pending"], pending: { request_id: "p1", kind: "question", prompt: "Continue?", options: [], metadata: {} } },
    ];
    act(() => events.forEach(feed.callbacks().onEvent));
    expect(hook.result.current.view?.activity).toBe("in-turn");
    expect(hook.result.current.view?.pending?.request_id).toBe("p1");
  });

  it("never carries a pending draft into a replacement request", async () => {
    const respondMayorInteraction = vi.fn<MayorAPI["respondMayorInteraction"]>(async () => ({
      session_id: "session-1",
      status: "accepted",
    }));
    const feed = controlledConnector();
    const api = fakeMayorAPI({
      getMayor: vi.fn(async () => ({
        ...view,
        pending: { request_id: "prompt-a", kind: "question", prompt: "First?", options: ["allow"], metadata: {} },
      })),
      respondMayorInteraction,
    });
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.view?.pending?.request_id).toBe("prompt-a"));
    act(() => hook.result.current.setPendingDraft("answer meant only for A"));

    act(() => feed.callbacks().onEvent({
      kind: "pending",
      resources: ["pending"],
      pending: { request_id: "prompt-b", kind: "question", prompt: "Second?", options: ["allow"], metadata: {} },
    }));

    await waitFor(() => expect(hook.result.current.pendingDraft).toBe(""));
    await act(async () => { await hook.result.current.respond("allow"); });
    expect(respondMayorInteraction).toHaveBeenCalledWith("prompt-b", "allow", undefined, {});
  });

  it("merges a live turn across one in-flight refresh and one coalesced trailing refresh", async () => {
    const blocked = deferred<TranscriptPage>();
    const getMayorTranscript = vi.fn<MayorAPI["getMayorTranscript"]>()
      .mockResolvedValueOnce(transcript())
      .mockImplementationOnce(async () => blocked.promise)
      .mockResolvedValue(transcript("reconciled"));
    const api = fakeMayorAPI({ getMayorTranscript });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.turns).toHaveLength(1));

    act(() => feed.callbacks().onEvent({ kind: "invalidate", resources: ["transcript"] }));
    await waitFor(() => expect(getMayorTranscript).toHaveBeenCalledTimes(2));
    act(() => {
      for (let index = 0; index < 5; index += 1) {
        feed.callbacks().onEvent({ kind: "invalidate", resources: ["transcript"] });
      }
      feed.callbacks().onEvent({ kind: "turn", cursor: "live-1", resources: ["transcript"], turn: { role: "assistant", text: "live during refresh" } });
    });
    await act(async () => { blocked.resolve(transcript("snapshot without live")); await blocked.promise; });

    await waitFor(() => expect(getMayorTranscript).toHaveBeenCalledTimes(3));
    expect(hook.result.current.turns.map((turn) => turn.text)).toContain("live during refresh");
    await new Promise((resolve) => window.setTimeout(resolve, 0));
    expect(getMayorTranscript).toHaveBeenCalledTimes(3);
  });

  it("keeps last-good data visibly stale when a visible poll fails and does not poll transcript", async () => {
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockResolvedValueOnce(view)
      .mockRejectedValueOnce(new Error("Mayor status offline"));
    const getMayorTranscript = vi.fn(async () => transcript());
    const api = fakeMayorAPI({ getMayor, getMayorTranscript });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));
    const transcriptCalls = getMayorTranscript.mock.calls.length;

    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(hook.result.current.error).toBe("Mayor status offline"));
    expect(hook.result.current.stale).toBe(true);
    expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["existing"]);
    expect(getMayorTranscript).toHaveBeenCalledTimes(transcriptCalls);
  });

  it("does not let an older full refresh overwrite a newer successful status poll", async () => {
    const olderFull = deferred<MayorView>();
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockResolvedValueOnce(view)
      .mockImplementationOnce(async () => olderFull.promise)
      .mockResolvedValueOnce({ ...view, session_name: "newer status poll" });
    const feed = controlledConnector();
    const api = fakeMayorAPI({ getMayor });
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));

    act(() => feed.callbacks().onEvent({ kind: "invalidate", resources: ["mayor"] }));
    await waitFor(() => expect(getMayor).toHaveBeenCalledTimes(2));
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(hook.result.current.view?.session_name).toBe("newer status poll"));

    await act(async () => {
      olderFull.resolve({ ...view, session_name: "older full refresh" });
      await olderFull.promise;
    });
    expect(hook.result.current.view?.session_name).toBe("newer status poll");
  });

  it("ignores an older status failure after a newer full refresh succeeds", async () => {
    const olderPoll = deferred<MayorView>();
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockResolvedValueOnce(view)
      .mockImplementationOnce(async () => olderPoll.promise)
      .mockResolvedValueOnce({ ...view, session_name: "newer full refresh" });
    const api = fakeMayorAPI({ getMayor });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));

    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(getMayor).toHaveBeenCalledTimes(2));
    act(() => feed.callbacks().onEvent({ kind: "invalidate", resources: ["mayor"] }));
    await waitFor(() => expect(hook.result.current.view?.session_name).toBe("newer full refresh"));

    await act(async () => {
      olderPoll.reject(new Error("older poll failed"));
      await Promise.resolve();
    });
    expect(hook.result.current.error).toBeNull();
    expect(hook.result.current.stale).toBe(false);
  });

  it("keeps a newer status failure after an older full refresh succeeds", async () => {
    const olderFull = deferred<MayorView>();
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockResolvedValueOnce(view)
      .mockImplementationOnce(async () => olderFull.promise)
      .mockRejectedValueOnce(new Error("newer poll failed"));
    const api = fakeMayorAPI({ getMayor });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, visible: true, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));

    act(() => feed.callbacks().onEvent({ kind: "invalidate", resources: ["mayor"] }));
    await waitFor(() => expect(getMayor).toHaveBeenCalledTimes(2));
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(hook.result.current.error).toBe("newer poll failed"));

    await act(async () => {
      olderFull.resolve({ ...view, session_name: "older full refresh" });
      await olderFull.promise;
    });
    expect(hook.result.current.error).toBe("newer poll failed");
    expect(hook.result.current.stale).toBe(true);
    expect(hook.result.current.view?.session_name).not.toBe("older full refresh");
  });

  it("does not report reconnect before the default event stream actually opens", async () => {
    vi.useFakeTimers();
    class FakeEventSource {
      readonly url: string;
      onopen: ((event: Event) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      listener: ((event: MessageEvent<string>) => void) | null = null;
      constructor(url: string) { this.url = url; sources.push(this); }
      addEventListener(_type: string, listener: (event: MessageEvent<string>) => void) { this.listener = listener; }
      removeEventListener() { this.listener = null; }
      close() { return undefined; }
    }
    const sources: FakeEventSource[] = [];
    vi.stubGlobal("EventSource", FakeEventSource);
    const callbacks: MayorStreamCallbacks = {
      onEvent: vi.fn(),
      onStale: vi.fn(),
      onReconnect: vi.fn(async () => undefined),
    };
    const stop = connectMayorEvents(callbacks);
    try {
      expect(sources).toHaveLength(1);
      expect(sources[0].url).toBe("/api/v1/mayor/events");
      sources[0].onerror?.(new Event("error"));
      await vi.advanceTimersByTimeAsync(1_000);
      expect(sources).toHaveLength(2);
      expect(callbacks.onReconnect).not.toHaveBeenCalled();
      sources[1].onerror?.(new Event("error"));
      await vi.advanceTimersByTimeAsync(2_000);
      expect(callbacks.onReconnect).not.toHaveBeenCalled();
      sources[2].onopen?.(new Event("open"));
      await Promise.resolve();
      expect(callbacks.onReconnect).toHaveBeenCalledOnce();
      sources[2].onerror?.(new Event("error"));
      await vi.advanceTimersByTimeAsync(999);
      expect(sources).toHaveLength(3);
      await vi.advanceTimersByTimeAsync(1);
      expect(sources).toHaveLength(4);
    } finally {
      stop();
    }
  });
});
