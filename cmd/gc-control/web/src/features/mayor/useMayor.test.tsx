import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { connectMayorEvents, useMayor } from "./useMayor";
import type { MayorController, MayorRefreshRequester, MayorStreamCallbacks, MayorStreamConnector } from "./useMayor";
import { createControlCenterAPI, MayorRequestError } from "@/lib/api";
import type { MayorAPI, MayorEvent, MayorView, TranscriptPage } from "@/lib/api";
import { createRefreshQueue } from "@/lib/events";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
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

function controlledConnector(): { connector: MayorStreamConnector; callbacks: () => MayorStreamCallbacks; mounted: () => boolean; closed: () => boolean } {
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
    mounted: () => current !== undefined,
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
	it("keeps the live stream behind the initial transcript barrier", async () => {
		const initialTranscript = deferred<TranscriptPage>();
		const connector = vi.fn<MayorStreamConnector>(() => () => undefined);
		const api = fakeMayorAPI({
			getMayorTranscript: vi.fn(async () => initialTranscript.promise),
		});
		const hook = renderHook(() => useMayor({ api, connector }));
		await waitFor(() => expect(api.getMayorTranscript).toHaveBeenCalledOnce());

		expect(connector).not.toHaveBeenCalled();
		await act(async () => {
			initialTranscript.resolve({ ...transcript(), turns: [{ role: "assistant", text: "arrived during bootstrap" }] });
			await initialTranscript.promise;
		});
		await waitFor(() => expect(connector).toHaveBeenCalledOnce());
		expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["arrived during bootstrap"]);
	});

  it("keeps the stream closed after a failed bootstrap until a full snapshot is confirmed", async () => {
    const connector = vi.fn<MayorStreamConnector>(() => () => undefined);
    const getMayorTranscript = vi.fn<MayorAPI["getMayorTranscript"]>()
      .mockRejectedValueOnce(new Error("bootstrap transcript offline"))
      .mockResolvedValue(transcript("recovered baseline"));
    const api = fakeMayorAPI({ getMayorTranscript });
    const hook = renderHook(() => useMayor({ api, connector }));

    await waitFor(() => expect(hook.result.current.error).toBe("bootstrap transcript offline"));
    expect(connector).not.toHaveBeenCalled();

    await act(async () => { await hook.result.current.refreshSnapshots(); });
    await waitFor(() => expect(connector).toHaveBeenCalledOnce());
    expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["recovered baseline"]);
  });

	it("applies a valid Mayor view before a slow transcript lane settles", async () => {
		const pendingTranscript = deferred<TranscriptPage>();
		const api = fakeMayorAPI({
			getMayor: vi.fn(async () => ({ ...view, session_name: "fast Mayor status" })),
			getMayorTranscript: vi.fn(async () => pendingTranscript.promise),
		});
		const hook = renderHook(() => useMayor({ api, connector: () => () => undefined }));

		await waitFor(() => expect(hook.result.current.view?.session_name).toBe("fast Mayor status"));
		expect(hook.result.current.loading).toBe(false);
		expect(hook.result.current.failureState).toBeNull();
		expect(hook.result.current.turns).toEqual([]);

		await act(async () => {
			pendingTranscript.resolve(transcript("slow transcript settled"));
			await pendingTranscript.promise;
		});
		await waitFor(() => expect(hook.result.current.turns.at(-1)?.text).toBe("slow transcript settled"));
	});

	it("keeps a valid Mayor view when the transcript lane fails", async () => {
		const api = fakeMayorAPI({
			getMayor: vi.fn(async () => ({ ...view, session_name: "confirmed Mayor" })),
			getMayorTranscript: vi.fn(async () => { throw new Error("transcript offline"); }),
		});
		const hook = renderHook(() => useMayor({ api, connector: () => () => undefined }));

		await waitFor(() => expect(hook.result.current.loading).toBe(false));
		expect(hook.result.current.view?.session_name).toBe("confirmed Mayor");
		expect(hook.result.current.failureState).toBeNull();
		expect(hook.result.current.error).toBe("transcript offline");
		expect(hook.result.current.stale).toBe(true);
	});

	it("preserves repeated identical untimestamped turns", async () => {
		const repeated = { role: "assistant", text: "Still working" };
		const api = fakeMayorAPI({
			getMayorTranscript: vi.fn(async () => ({
				...transcript(),
				turns: [{ ...repeated }, { ...repeated }],
				returned: 2,
				total: 2,
			})),
		});
		const hook = renderHook(() => useMayor({ api, connector: () => () => undefined }));

		await waitFor(() => expect(hook.result.current.loading).toBe(false));
		expect(hook.result.current.turns).toHaveLength(2);
		expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["Still working", "Still working"]);
	});

	it("preserves every live turn when multiple turns share one backend cursor", async () => {
		const feed = controlledConnector();
		const api = fakeMayorAPI();
		const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
		await waitFor(() => expect(hook.result.current.loading).toBe(false));

		act(() => {
			feed.callbacks().onEvent({ kind: "turn", cursor: "shared-cursor", resources: ["transcript"], turn: { role: "assistant", text: "first turn" } });
			feed.callbacks().onEvent({ kind: "turn", cursor: "shared-cursor", resources: ["transcript"], turn: { role: "assistant", text: "second turn" } });
		});

		expect(hook.result.current.turns.slice(-2).map((turn) => turn.text)).toEqual(["first turn", "second turn"]);
	});

	it("reconciles repeated live turns against snapshot occurrences as a multiset", async () => {
		const repeated = { role: "assistant", text: "same turn" };
		const getMayorTranscript = vi.fn<MayorAPI["getMayorTranscript"]>()
			.mockResolvedValueOnce(transcript())
			.mockResolvedValue({ ...transcript(), turns: [{ ...repeated }], returned: 1, total: 1 });
		const api = fakeMayorAPI({ getMayorTranscript });
		const feed = controlledConnector();
		const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
		await waitFor(() => expect(hook.result.current.loading).toBe(false));

		act(() => {
			feed.callbacks().onEvent({ kind: "turn", cursor: "shared-cursor", resources: ["transcript"], turn: { ...repeated } });
			feed.callbacks().onEvent({ kind: "turn", cursor: "shared-cursor", resources: ["transcript"], turn: { ...repeated } });
		});
		await act(async () => { await hook.result.current.refresh(); });

		expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["same turn", "same turn"]);
	});

	it("does not consume a new live turn with an identical historical snapshot occurrence", async () => {
		const repeated = { role: "assistant", text: "same historical and live turn" };
		const unchanged = { ...transcript(), turns: [{ ...repeated }], returned: 1, total: 1 };
		const getMayorTranscript = vi.fn<MayorAPI["getMayorTranscript"]>().mockResolvedValue(unchanged);
		const api = fakeMayorAPI({ getMayorTranscript });
		const feed = controlledConnector();
		const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
		await waitFor(() => expect(hook.result.current.turns).toHaveLength(1));

		act(() => feed.callbacks().onEvent({ kind: "turn", cursor: "new-live", resources: ["transcript"], turn: { ...repeated } }));
		expect(hook.result.current.turns).toHaveLength(2);
		await act(async () => { await hook.result.current.refresh(); });

		expect(hook.result.current.turns.map((turn) => turn.text)).toEqual([
			"same historical and live turn",
			"same historical and live turn",
		]);
	});

	it("does not own focus, visibility, or interval polling", async () => {
		const windowAdd = vi.spyOn(window, "addEventListener");
		const documentAdd = vi.spyOn(document, "addEventListener");
		const interval = vi.spyOn(window, "setInterval");
		const api = fakeMayorAPI();
		const connector = () => () => undefined;
		const hook = renderHook(() => useMayor({ api, connector }));
		await waitFor(() => expect(hook.result.current.loading).toBe(false));

		expect(windowAdd.mock.calls.filter(([type]) => type === "focus")).toHaveLength(0);
		expect(documentAdd.mock.calls.filter(([type]) => type === "visibilitychange")).toHaveLength(0);
		expect(interval.mock.calls.filter(([, delay]) => delay === 10_000)).toHaveLength(0);
	});

	it("does not start a queued trailing refresh after unmount", async () => {
		const blocked = deferred<TranscriptPage>();
		const getMayorTranscript = vi.fn<MayorAPI["getMayorTranscript"]>()
			.mockResolvedValueOnce(transcript())
			.mockImplementationOnce(async () => blocked.promise)
			.mockResolvedValue(transcript("obsolete trailing"));
		const api = fakeMayorAPI({ getMayorTranscript });
		const feed = controlledConnector();
		let controller: MayorController | undefined;
		const queue = createRefreshQueue(async (resources, signal) => {
			if (!controller) return;
			if (resources.includes("mayor-snapshots")) await controller.refreshSnapshots(signal);
		});
		const requestRefresh: MayorRefreshRequester = async (resources) => {
			queue.request(resources);
			await queue.idle();
		};
		const hook = renderHook(() => {
			controller = useMayor({ api, connector: feed.connector, requestRefresh });
			return controller;
		});
		await waitFor(() => expect(hook.result.current.loading).toBe(false));

		act(() => feed.callbacks().onEvent({ kind: "invalidate", resources: ["transcript"] }));
		await waitFor(() => expect(getMayorTranscript).toHaveBeenCalledTimes(2));
		act(() => feed.callbacks().onEvent({ kind: "invalidate", resources: ["transcript"] }));
		hook.unmount();
		queue.dispose();
		blocked.resolve(transcript("released after unmount"));
		await blocked.promise;
		await new Promise((resolve) => window.setTimeout(resolve, 0));

		expect(getMayorTranscript).toHaveBeenCalledTimes(2);
	});

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
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));
    expect(hook.result.current.failureState).toBe(failureState);
  });

  it("loads snapshots, appends live turns, and never optimistically delivers a send", async () => {
    let resolveSend: ((value: { request_id: string; status: string; intent: "default"; queued: boolean }) => void) | undefined;
    const sendMayorMessage = vi.fn(() => new Promise<{ request_id: string; status: string; intent: "default"; queued: boolean }>((resolve) => { resolveSend = resolve; }));
    const api = fakeMayorAPI({ sendMayorMessage });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));

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
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
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

  it("exposes a status-only refresh action without refreshing transcript", async () => {
    const getMayor = vi.fn(async () => view);
    const getMayorTranscript = vi.fn(async () => transcript());
    const api = fakeMayorAPI({ getMayor, getMayorTranscript });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));
    const viewCalls = getMayor.mock.calls.length;
    const transcriptCalls = getMayorTranscript.mock.calls.length;

    await act(async () => { await hook.result.current.refreshStatus(); });
    expect(getMayor).toHaveBeenCalledTimes(viewCalls + 1);
    expect(getMayorTranscript).toHaveBeenCalledTimes(transcriptCalls);
  });

  it("clears transcript history when a status-only refresh changes the authoritative session", async () => {
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockResolvedValueOnce({ ...view, session_id: "session-1" })
      .mockResolvedValue({ ...view, session_id: "session-2", session_name: "replacement" });
    const getMayorTranscript = vi.fn(async (before?: string) => before
      ? { ...transcript("session one older"), has_older: false, before: undefined }
      : transcript("session one current"));
    const api = fakeMayorAPI({ getMayor, getMayorTranscript });
    const hook = renderHook(() => useMayor({
      api,
      connector: () => () => undefined,
    }));
    await waitFor(() => expect(hook.result.current.turns.at(-1)?.text).toBe("session one current"));
    await act(async () => { await hook.result.current.loadOlder(); });
    act(() => hook.result.current.setScrollOffset(44));
    const transcriptCalls = getMayorTranscript.mock.calls.length;

    await act(async () => { await hook.result.current.refreshStatus(); });

    expect(hook.result.current.view?.session_id).toBe("session-2");
    expect(hook.result.current.turns).toEqual([]);
    expect(hook.result.current.scrollOffset).toBe(0);
    expect(getMayorTranscript).toHaveBeenCalledTimes(transcriptCalls);
  });

  it("merges older transcript chronologically without duplicates", async () => {
    const older: TranscriptPage = { ...transcript("older turn"), has_older: false, before: undefined };
    const getMayorTranscript = vi.fn(async (before?: string) => before ? older : transcript());
    const feed = controlledConnector();
    const api = fakeMayorAPI({ getMayorTranscript });
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.turns).toHaveLength(1));
    await act(async () => { await hook.result.current.loadOlder(); });
    expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["older turn", "existing"]);
    await act(async () => { await hook.result.current.loadOlder(); });
    expect(hook.result.current.turns).toHaveLength(2);
    await act(async () => { await hook.result.current.refresh(); });
    expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["older turn", "existing"]);
  });

	it("merges ordered page overlap while preserving repeated untimestamped turns", async () => {
		const repeated = { role: "assistant", text: "repeat" };
		const newest: TranscriptPage = {
			...transcript(),
			turns: [{ ...repeated }, { ...repeated }, { role: "assistant", text: "newest" }],
			returned: 3,
			total: 4,
		};
		const older: TranscriptPage = {
			...transcript(),
			turns: [{ role: "user", text: "oldest" }, { ...repeated }, { ...repeated }],
			has_older: false,
			before: undefined,
			returned: 3,
			total: 4,
		};
		const getMayorTranscript = vi.fn(async (before?: string) => before ? older : newest);
		const api = fakeMayorAPI({ getMayorTranscript });
		const hook = renderHook(() => useMayor({ api, connector: () => () => undefined }));
		await waitFor(() => expect(hook.result.current.turns).toHaveLength(3));

		await act(async () => { await hook.result.current.loadOlder(); });
		expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["oldest", "repeat", "repeat", "newest"]);
	});

	it("resets transcript history and scroll when the authoritative session changes", async () => {
		const getMayor = vi.fn<MayorAPI["getMayor"]>()
			.mockResolvedValueOnce({ ...view, session_id: "session-1" })
			.mockResolvedValue({ ...view, session_id: "session-2", session_name: "rematerialized" });
		let currentPage = 0;
		const getMayorTranscript = vi.fn<MayorAPI["getMayorTranscript"]>(async (before?: string) => {
			if (before) return { ...transcript("session one older"), has_older: false, before: undefined };
			currentPage += 1;
			return currentPage === 1
				? transcript("session one current")
				: { ...transcript("session two only"), has_older: false, before: undefined };
		});
		const api = fakeMayorAPI({ getMayor, getMayorTranscript });
		const hook = renderHook(() => useMayor({ api, connector: () => () => undefined }));
		await waitFor(() => expect(hook.result.current.turns.at(-1)?.text).toBe("session one current"));
		await act(async () => { await hook.result.current.loadOlder(); });
		act(() => hook.result.current.setScrollOffset(73));

		await act(async () => { await hook.result.current.refresh(); });

		expect(hook.result.current.view?.session_id).toBe("session-2");
		expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["session two only"]);
		expect(hook.result.current.scrollOffset).toBe(0);
	});

  it("drops an older page that resolves after the authoritative session changes", async () => {
    const blockedOlder = deferred<TranscriptPage>();
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockResolvedValueOnce({ ...view, session_id: "session-1" })
      .mockResolvedValue({ ...view, session_id: "session-2", session_name: "replacement" });
    let currentPage = 0;
    const getMayorTranscript = vi.fn<MayorAPI["getMayorTranscript"]>(async (before?: string) => {
      if (before) return blockedOlder.promise;
      currentPage += 1;
      return currentPage === 1
        ? transcript("session one current")
        : { ...transcript("session two current"), has_older: false, before: undefined };
    });
    const api = fakeMayorAPI({ getMayor, getMayorTranscript });
    const hook = renderHook(() => useMayor({ api, connector: () => () => undefined }));
    await waitFor(() => expect(hook.result.current.turns.at(-1)?.text).toBe("session one current"));

    let pendingOlder!: Promise<void>;
    act(() => { pendingOlder = hook.result.current.loadOlder(); });
    await waitFor(() => expect(getMayorTranscript).toHaveBeenCalledWith("older"));
    await act(async () => { await hook.result.current.refreshSnapshots(); });

    expect(hook.result.current.view?.session_id).toBe("session-2");
    expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["session two current"]);
    expect(hook.result.current.loadingOlder).toBe(false);

    await act(async () => {
      blockedOlder.resolve({ ...transcript("late session one older"), has_older: false, before: undefined });
      await pendingOlder;
    });
    expect(hook.result.current.turns.map((turn) => turn.text)).toEqual(["session two current"]);
  });

  it("updates activity and pending hints without accepting malformed resources", async () => {
    const feed = controlledConnector();
    const api = fakeMayorAPI();
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.turns).toHaveLength(1));
    await waitFor(() => expect(feed.mounted()).toBe(true));
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
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
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
    let controller: MayorController | undefined;
    const queue = createRefreshQueue(async (resources, signal) => {
      if (!controller) return;
      if (resources.includes("mayor-snapshots")) await controller.refreshSnapshots(signal);
    });
    const requestRefresh: MayorRefreshRequester = async (resources) => {
      queue.request(resources);
      await queue.idle();
    };
    const hook = renderHook(() => {
      controller = useMayor({ api, connector: feed.connector, requestRefresh });
      return controller;
    });
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
    queue.dispose();
  });

  it("keeps last-good data visibly stale when a visible poll fails and does not poll transcript", async () => {
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockResolvedValueOnce(view)
      .mockRejectedValueOnce(new Error("Mayor status offline"));
    const getMayorTranscript = vi.fn(async () => transcript());
    const api = fakeMayorAPI({ getMayor, getMayorTranscript });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));
    const transcriptCalls = getMayorTranscript.mock.calls.length;

    await act(async () => { await hook.result.current.refreshStatus(); });
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
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));

    act(() => feed.callbacks().onEvent({ kind: "invalidate", resources: ["mayor"] }));
    await waitFor(() => expect(getMayor).toHaveBeenCalledTimes(2));
    await act(async () => { await hook.result.current.refreshStatus(); });
    await waitFor(() => expect(hook.result.current.view?.session_name).toBe("newer status poll"));

    await act(async () => {
      olderFull.resolve({ ...view, session_name: "older full refresh" });
      await olderFull.promise;
    });
    expect(hook.result.current.view?.session_name).toBe("newer status poll");
  });

  it("ignores an older transcript failure after a newer status poll succeeds", async () => {
    const olderView = deferred<MayorView>();
    const olderTranscript = deferred<TranscriptPage>();
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockResolvedValueOnce(view)
      .mockImplementationOnce(async () => olderView.promise)
      .mockResolvedValueOnce({ ...view, session_name: "newer status poll" });
    const getMayorTranscript = vi.fn<MayorAPI["getMayorTranscript"]>()
      .mockResolvedValueOnce(transcript())
      .mockImplementationOnce(async () => olderTranscript.promise);
    const api = fakeMayorAPI({ getMayor, getMayorTranscript });
    const hook = renderHook(() => useMayor({ api, connector: () => () => undefined }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));

    let olderRefresh!: Promise<boolean>;
    act(() => { olderRefresh = hook.result.current.refreshSnapshots(); });
    await waitFor(() => expect(getMayorTranscript).toHaveBeenCalledTimes(2));
    await act(async () => { await hook.result.current.refreshStatus(); });
    expect(hook.result.current.view?.session_name).toBe("newer status poll");

    await act(async () => {
      olderTranscript.reject(new Error("obsolete transcript failure"));
      olderView.resolve({ ...view, session_name: "older full refresh" });
      await olderRefresh;
    });

    expect(hook.result.current.error).toBeNull();
    expect(hook.result.current.view?.session_name).toBe("newer status poll");
  });

  it("does not let an older full refresh re-mark a newer full snapshot stale", async () => {
    const olderView = deferred<MayorView>();
    const olderTranscript = deferred<TranscriptPage>();
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockImplementationOnce(async () => olderView.promise)
      .mockResolvedValue({ ...view, session_name: "newer full snapshot" });
    const getMayorTranscript = vi.fn<MayorAPI["getMayorTranscript"]>()
      .mockImplementationOnce(async () => olderTranscript.promise)
      .mockResolvedValue(transcript("newer transcript"));
    const api = fakeMayorAPI({ getMayor, getMayorTranscript });
    const hook = renderHook(() => useMayor({ api, connector: () => () => undefined }));
    await waitFor(() => expect(getMayor).toHaveBeenCalledTimes(1));

    await act(async () => { await hook.result.current.refreshSnapshots(); });
    expect(hook.result.current.view?.session_name).toBe("newer full snapshot");
    expect(hook.result.current.stale).toBe(false);

    await act(async () => {
      olderView.resolve({ ...view, session_name: "older full snapshot" });
      olderTranscript.resolve(transcript("older transcript"));
      await Promise.all([olderView.promise, olderTranscript.promise]);
    });
    await act(async () => { await Promise.resolve(); });

    expect(hook.result.current.view?.session_name).toBe("newer full snapshot");
    expect(hook.result.current.turns.at(-1)?.text).toBe("newer transcript");
    expect(hook.result.current.stale).toBe(false);
  });

  it("ignores an older status failure after a newer full refresh succeeds", async () => {
    const olderPoll = deferred<MayorView>();
    const getMayor = vi.fn<MayorAPI["getMayor"]>()
      .mockResolvedValueOnce(view)
      .mockImplementationOnce(async () => olderPoll.promise)
      .mockResolvedValueOnce({ ...view, session_name: "newer full refresh" });
    const api = fakeMayorAPI({ getMayor });
    const feed = controlledConnector();
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));

    let pendingPoll!: Promise<boolean>;
    act(() => { pendingPoll = hook.result.current.refreshStatus(); });
    await waitFor(() => expect(getMayor).toHaveBeenCalledTimes(2));
    act(() => feed.callbacks().onEvent({ kind: "invalidate", resources: ["mayor"] }));
    await waitFor(() => expect(hook.result.current.view?.session_name).toBe("newer full refresh"));

    await act(async () => {
      olderPoll.reject(new Error("older poll failed"));
      await pendingPoll;
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
    const hook = renderHook(() => useMayor({ api, connector: feed.connector }));
    await waitFor(() => expect(hook.result.current.loading).toBe(false));

    act(() => feed.callbacks().onEvent({ kind: "invalidate", resources: ["mayor"] }));
    await waitFor(() => expect(getMayor).toHaveBeenCalledTimes(2));
    await act(async () => { await hook.result.current.refreshStatus(); });
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

	it("reconciles the bootstrap gap after the default event stream first opens", async () => {
		const sources: FakeEventSource[] = [];
		class FakeEventSource {
			onopen: ((event: Event) => void) | null = null;
			onerror: ((event: Event) => void) | null = null;
			constructor(url: string) { void url; sources.push(this); }
			addEventListener() { return undefined; }
			removeEventListener() { return undefined; }
			close() { return undefined; }
		}
		vi.stubGlobal("EventSource", FakeEventSource);
		const callbacks: MayorStreamCallbacks = {
			onEvent: vi.fn(),
			onStale: vi.fn(),
			onReconnect: vi.fn(async () => undefined),
		};
		const stop = connectMayorEvents(callbacks);
		try {
			const source = sources[0];
			expect(callbacks.onReconnect).not.toHaveBeenCalled();
			source.onopen?.(new Event("open"));
			await Promise.resolve();
			expect(callbacks.onReconnect).toHaveBeenCalledOnce();
		} finally {
			stop();
		}
	});
});
