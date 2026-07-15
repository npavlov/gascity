import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { MailAPI, MailCount, MailMessage, MailPage, MailThread } from "@/lib/api";

import { useMail } from "./useMail";

function message(id: string, options: Partial<MailMessage> = {}): MailMessage {
  return {
    schema_version: 1,
    id,
    from: "mayor",
    to: "crew",
    subject: `Subject ${id}`,
    body: `Body ${id}`,
    created_at: "2026-07-15T12:00:00Z",
    read: false,
    cc: [],
    ...options,
  };
}

const count: MailCount = { schema_version: 1, total: 2, unread: 1, partial: false, partial_errors: [] };

function page(items: MailMessage[], next?: string, partial = false): MailPage {
  return { schema_version: 1, items, total: 2, next_cursor: next, partial, partial_errors: partial ? ["rig partial"] : [] };
}

function thread(items: MailMessage[]): MailThread {
  return { schema_version: 1, items, total: items.length, partial: false, partial_errors: [], truncated: false };
}

function fakeMailAPI(overrides: Partial<MailAPI> = {}): MailAPI {
  const first = message("a", { thread_id: "thread-a" });
  return {
    getMailCount: vi.fn(async () => count),
    listMail: vi.fn(async () => page([first, message("b", { rig: "rig-two", thread_id: "thread-b" })])),
    getMail: vi.fn(async (id) => message(id, { thread_id: `thread-${id}` })),
    getMailThread: vi.fn(async (id) => thread([message(`${id}-1`)])),
    ...overrides,
  };
}

function deferred<T>() {
  let resolve: ((value: T) => void) | undefined;
  const promise = new Promise<T>((next) => { resolve = next; });
  return { promise, resolve: (value: T) => resolve?.(value) };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useMail", () => {
  it("performs no autonomous reads or timers and delegates manual refresh to the shared coordinator", async () => {
    const intervalSpy = vi.spyOn(window, "setInterval");
    const requestRefresh = vi.fn();
    const api = fakeMailAPI();
    const { result } = renderHook(() => useMail({ api, requestRefresh }));

    await act(async () => { await Promise.resolve(); });
    expect(api.getMailCount).not.toHaveBeenCalled();
    expect(api.listMail).not.toHaveBeenCalled();
    expect(intervalSpy).not.toHaveBeenCalled();

    await act(async () => { expect(await result.current.refreshCount()).toBe(true); });
    await act(async () => { expect(await result.current.refreshSnapshots()).toBe(true); });
    expect(api.getMailCount).toHaveBeenCalledOnce();
    expect(api.listMail).toHaveBeenCalledWith("unread", undefined, 50, expect.any(AbortSignal));
    expect(api.getMail).toHaveBeenCalledWith("a", undefined, expect.any(AbortSignal));
    expect(api.getMailThread).toHaveBeenCalledWith("thread-a", undefined, expect.any(AbortSignal));

    act(() => result.current.refresh());
    expect(requestRefresh).toHaveBeenCalledWith(["count", "snapshots"]);
  });

  it("selects with detail and thread GETs only and forwards the exact rig identity", async () => {
    const api = fakeMailAPI();
    const { result } = renderHook(() => useMail({ api, requestRefresh: vi.fn() }));
    await act(async () => { await result.current.refreshSnapshots(); });
    vi.mocked(api.getMail).mockClear();
    vi.mocked(api.getMailThread).mockClear();

    act(() => result.current.select({ id: "b", rig: "rig-two" }));
    await waitFor(() => expect(api.getMail).toHaveBeenCalledWith("b", "rig-two", expect.any(AbortSignal)));
    expect(api.getMailThread).toHaveBeenCalledWith("thread-b", "rig-two", expect.any(AbortSignal));
    expect(Object.keys(api).sort()).toEqual(["getMail", "getMailCount", "getMailThread", "listMail"]);
  });

  it("retains last-confirmed rows and snapshot freshness when snapshot refresh fails", async () => {
    const listMail = vi.fn<MailAPI["listMail"]>()
      .mockResolvedValueOnce(page([message("a")]))
      .mockRejectedValueOnce(new Error("offline"));
    const api = fakeMailAPI({ listMail });
    const { result } = renderHook(() => useMail({ api, requestRefresh: vi.fn() }));
    await act(async () => { await result.current.refreshSnapshots(); });

    await act(async () => { expect(await result.current.refreshSnapshots()).toBe(false); });
    expect(result.current.collection.items.map((item) => item.id)).toEqual(["a"]);
    expect(result.current.snapshotsStale).toBe(true);
    expect(result.current.stale).toBe(true);
  });

  it("does not let a count-only success clear failed snapshot freshness before revalidation", async () => {
    const reopened = deferred<MailPage>();
    const revalidatedDetail = deferred<MailMessage>();
    const revalidatedThread = deferred<MailThread>();
    const listMail = vi.fn<MailAPI["listMail"]>()
      .mockResolvedValueOnce(page([message("cached")]))
      .mockRejectedValueOnce(new Error("snapshots offline"))
      .mockImplementationOnce(() => reopened.promise);
    const getMail = vi.fn<MailAPI["getMail"]>()
      .mockResolvedValueOnce(message("cached"))
      .mockImplementationOnce(() => revalidatedDetail.promise);
    const getMailThread = vi.fn<MailAPI["getMailThread"]>()
      .mockResolvedValueOnce(thread([message("cached-thread")]))
      .mockImplementationOnce(() => revalidatedThread.promise);
    const api = fakeMailAPI({ listMail, getMail, getMailThread });
    const { result } = renderHook(() => useMail({ api, requestRefresh: vi.fn() }));
    await act(async () => {
      await Promise.all([result.current.refreshCount(), result.current.refreshSnapshots()]);
    });
    expect(result.current.collection.items.map((item) => item.id)).toEqual(["cached"]);

    await act(async () => { expect(await result.current.refreshSnapshots()).toBe(false); });
    expect(result.current.snapshotsStale).toBe(true);
    await act(async () => { expect(await result.current.refreshCount()).toBe(true); });
    expect(result.current.countStale).toBe(false);
    expect(result.current.snapshotsStale).toBe(true);
    expect(result.current.stale).toBe(true);

    let reopenRequest: Promise<boolean> | undefined;
    act(() => { reopenRequest = result.current.refreshSnapshots(); });
    expect(result.current.collection.items.map((item) => item.id)).toEqual(["cached"]);
    expect(result.current.snapshotsStale).toBe(true);
    act(() => reopened.resolve(page([message("revalidated")])));
    await waitFor(() => expect(getMail).toHaveBeenCalledTimes(2));
    expect(result.current.collection.items.map((item) => item.id)).toEqual(["revalidated"]);
    expect(result.current.snapshotsStale).toBe(true);

    await act(async () => {
      revalidatedDetail.resolve(message("revalidated"));
      revalidatedThread.resolve(thread([message("revalidated-thread")]));
      await reopenRequest;
    });
    expect(result.current.snapshotsStale).toBe(false);
    expect(result.current.stale).toBe(false);
  });

  it("invalidates pending count and snapshot reads before accepting post-event refreshes", async () => {
    const oldCount = deferred<MailCount>();
    const oldList = deferred<MailPage>();
    let oldCountSignal: AbortSignal | undefined;
    let oldListSignal: AbortSignal | undefined;
    const getMailCount = vi.fn<MailAPI["getMailCount"]>()
      .mockImplementationOnce((signal) => {
        oldCountSignal = signal;
        return oldCount.promise;
      })
      .mockResolvedValue(count);
    const listMail = vi.fn<MailAPI["listMail"]>()
      .mockImplementationOnce((_status, _cursor, _limit, signal) => {
        oldListSignal = signal;
        return oldList.promise;
      })
      .mockResolvedValue(page([message("fresh")]));
    const api = fakeMailAPI({ getMailCount, listMail });
    const { result } = renderHook(() => useMail({ api, requestRefresh: vi.fn() }));

    let oldCountRequest: Promise<boolean> | undefined;
    let oldSnapshotRequest: Promise<boolean> | undefined;
    act(() => {
      oldCountRequest = result.current.refreshCount();
      oldSnapshotRequest = result.current.refreshSnapshots();
    });
    await waitFor(() => expect(listMail).toHaveBeenCalledOnce());
    act(() => result.current.onInvalidation());
    expect(oldCountSignal?.aborted).toBe(true);
    expect(oldListSignal?.aborted).toBe(true);

    await act(async () => {
      oldCount.resolve({ ...count, unread: 99 });
      oldList.resolve(page([message("stale")]));
      await Promise.all([oldCountRequest, oldSnapshotRequest]);
    });
    expect(result.current.count).toBeNull();
    expect(result.current.collection.items).toEqual([]);
    expect(result.current.countStale).toBe(true);
    expect(result.current.snapshotsStale).toBe(true);

    await act(async () => {
      await Promise.all([result.current.refreshCount(), result.current.refreshSnapshots()]);
    });
    expect(result.current.count?.unread).toBe(count.unread);
    expect(result.current.collection.items.map((item) => item.id)).toEqual(["fresh"]);
    expect(result.current.stale).toBe(false);
  });

  it("aborts and generation-guards an old filter refresh while the coordinator schedules the new page", async () => {
    const oldUnread = deferred<MailPage>();
    const nextAll = deferred<MailPage>();
    let oldSignal: AbortSignal | undefined;
    const listMail = vi.fn<MailAPI["listMail"]>()
      .mockResolvedValueOnce(page([message("a"), message("b")]))
      .mockImplementationOnce((_status, _cursor, _limit, signal) => {
        oldSignal = signal;
        return oldUnread.promise;
      })
      .mockImplementationOnce((status) => {
        expect(status).toBe("all");
        return nextAll.promise;
      });
    const requestRefresh = vi.fn();
    const api = fakeMailAPI({ listMail });
    const { result } = renderHook(() => useMail({ api, requestRefresh }));
    await act(async () => { await result.current.refreshSnapshots(); });

    let oldRequest: Promise<boolean> | undefined;
    act(() => { oldRequest = result.current.refreshSnapshots(); });
    await waitFor(() => expect(listMail).toHaveBeenCalledTimes(2));
    act(() => result.current.setFilter("all"));
    expect(requestRefresh).toHaveBeenCalledWith(["snapshots"]);
    expect(oldSignal?.aborted).toBe(true);
    expect(result.current.collection.filter).toBe("all");
    expect(result.current.collection.items).toEqual([]);

    await act(async () => {
      oldUnread.resolve(page([message("stale-unread")]));
      await oldRequest;
    });
    expect(result.current.collection.items).toEqual([]);
    let allRequest: Promise<boolean> | undefined;
    act(() => { allRequest = result.current.refreshSnapshots(); });
    await act(async () => {
      nextAll.resolve(page([message("all-a"), message("all-b")]));
      await allRequest;
    });
    expect(result.current.collection.items.map((item) => item.id)).toEqual(["all-a", "all-b"]);
    expect(result.current.collection.filter).toBe("all");
  });

  it("reconciles an in-flight page refresh against the newest exact rig selection", async () => {
    const refreshed = deferred<MailPage>();
    const first = message("same", { rig: "one", thread_id: "thread-one" });
    const second = message("same", { rig: "two", thread_id: "thread-two" });
    const listMail = vi.fn<MailAPI["listMail"]>()
      .mockResolvedValueOnce(page([first, second]))
      .mockImplementationOnce(() => refreshed.promise);
    const api = fakeMailAPI({ listMail });
    const { result } = renderHook(() => useMail({ api, requestRefresh: vi.fn() }));
    await act(async () => { await result.current.refreshSnapshots(); });

    let request: Promise<boolean> | undefined;
    act(() => { request = result.current.refreshSnapshots(); });
    await waitFor(() => expect(listMail).toHaveBeenCalledTimes(2));
    act(() => result.current.select({ id: "same", rig: "two" }));
    expect(result.current.collection.selected).toEqual({ id: "same", rig: "two" });

    await act(async () => {
      refreshed.resolve(page([first, second]));
      await request;
    });
    expect(result.current.collection.selected).toEqual({ id: "same", rig: "two" });
    await waitFor(() => expect(result.current.detail?.identity).toEqual({ id: "same", rig: "two" }));
  });

  it("aborts superseded selection reads and cleanup aborts outstanding work", async () => {
    const signals: AbortSignal[] = [];
    const getMail = vi.fn<MailAPI["getMail"]>(async (id, _rig, signal) => {
      if (id === "a") return message("a", { thread_id: "thread-a" });
      if (signal) signals.push(signal);
      return await new Promise<MailMessage>((_resolve, reject) => {
        signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
      });
    });
    const api = fakeMailAPI({ getMail });
    const { result, unmount } = renderHook(() => useMail({ api, requestRefresh: vi.fn() }));
    await act(async () => { await result.current.refreshSnapshots(); });

    act(() => result.current.select({ id: "b", rig: "rig-two" }));
    await waitFor(() => expect(signals).toHaveLength(1));
    act(() => result.current.select({ id: "a" }));
    expect(signals[0].aborted).toBe(true);

    act(() => result.current.select({ id: "b", rig: "rig-two" }));
    await waitFor(() => expect(signals).toHaveLength(2));
    unmount();
    expect(signals[1].aborted).toBe(true);
  });

  it("keeps reconnect stale until count, list, selected detail, and thread all revalidate", async () => {
    const api = fakeMailAPI();
    const { result } = renderHook(() => useMail({ api, requestRefresh: vi.fn() }));
    await act(async () => {
      await Promise.all([result.current.refreshCount(), result.current.refreshSnapshots()]);
    });

    act(() => result.current.onDisconnect());
    act(() => result.current.onReconnect());
    expect(result.current.disconnected).toBe(true);
    expect(result.current.countStale).toBe(true);
    expect(result.current.snapshotsStale).toBe(true);

    await act(async () => { await result.current.refreshCount(); });
    expect(result.current.disconnected).toBe(true);
    expect(result.current.countStale).toBe(false);
    expect(result.current.snapshotsStale).toBe(true);

    await act(async () => { await result.current.refreshSnapshots(); });
    expect(result.current.disconnected).toBe(false);
    expect(result.current.snapshotsStale).toBe(false);
    expect(result.current.stale).toBe(false);
  });

  it("keeps reconnect stale when selection completes before the refreshed list", async () => {
    const refreshedList = deferred<MailPage>();
    const refreshedDetail = deferred<MailMessage>();
    const refreshedThread = deferred<MailThread>();
    const first = message("first", { thread_id: "thread-first" });
    const second = message("second", { thread_id: "thread-second" });
    const listMail = vi.fn<MailAPI["listMail"]>()
      .mockResolvedValueOnce(page([first, second]))
      .mockImplementationOnce(() => refreshedList.promise);
    const getMail = vi.fn<MailAPI["getMail"]>()
      .mockResolvedValueOnce(first)
      .mockResolvedValueOnce(second)
      .mockImplementationOnce(() => refreshedDetail.promise);
    const getMailThread = vi.fn<MailAPI["getMailThread"]>()
      .mockResolvedValueOnce(thread([first]))
      .mockResolvedValueOnce(thread([second]))
      .mockImplementationOnce(() => refreshedThread.promise);
    const api = fakeMailAPI({ listMail, getMail, getMailThread });
    const { result } = renderHook(() => useMail({ api, requestRefresh: vi.fn() }));
    await act(async () => {
      await Promise.all([result.current.refreshCount(), result.current.refreshSnapshots()]);
    });

    act(() => result.current.onDisconnect());
    act(() => result.current.onReconnect());
    await act(async () => { await result.current.refreshCount(); });
    let snapshotRequest: Promise<boolean> | undefined;
    act(() => { snapshotRequest = result.current.refreshSnapshots(); });
    await waitFor(() => expect(listMail).toHaveBeenCalledTimes(2));

    act(() => result.current.select({ id: second.id }));
    await waitFor(() => expect(result.current.detail?.identity.id).toBe(second.id));
    expect(result.current.disconnected).toBe(true);
    expect(result.current.snapshotsStale).toBe(true);

    act(() => refreshedList.resolve(page([first, { ...second, thread_id: "thread-second-refreshed" }])));
    await waitFor(() => expect(getMail).toHaveBeenCalledTimes(3));
    expect(result.current.disconnected).toBe(true);
    expect(result.current.snapshotsStale).toBe(true);

    await act(async () => {
      refreshedDetail.resolve(second);
      refreshedThread.resolve(thread([second]));
      await snapshotRequest;
    });
    expect(result.current.disconnected).toBe(false);
    expect(result.current.snapshotsStale).toBe(false);
  });

  it("rejects pending pre-reconnect reads until a full post-reconnect refresh completes", async () => {
    const oldCount = deferred<MailCount>();
    const oldList = deferred<MailPage>();
    let oldCountSignal: AbortSignal | undefined;
    let oldListSignal: AbortSignal | undefined;
    const getMailCount = vi.fn<MailAPI["getMailCount"]>()
      .mockResolvedValueOnce(count)
      .mockImplementationOnce((signal) => {
        oldCountSignal = signal;
        return oldCount.promise;
      })
      .mockResolvedValue({ ...count, unread: 0 });
    const listMail = vi.fn<MailAPI["listMail"]>()
      .mockResolvedValueOnce(page([message("cached")]))
      .mockImplementationOnce((_status, _cursor, _limit, signal) => {
        oldListSignal = signal;
        return oldList.promise;
      })
      .mockResolvedValue(page([message("reconnected")]));
    const api = fakeMailAPI({ getMailCount, listMail });
    const { result } = renderHook(() => useMail({ api, requestRefresh: vi.fn() }));
    await act(async () => {
      await Promise.all([result.current.refreshCount(), result.current.refreshSnapshots()]);
    });

    let oldCountRequest: Promise<boolean> | undefined;
    let oldSnapshotRequest: Promise<boolean> | undefined;
    act(() => {
      oldCountRequest = result.current.refreshCount();
      oldSnapshotRequest = result.current.refreshSnapshots();
    });
    await waitFor(() => expect(listMail).toHaveBeenCalledTimes(2));
    act(() => result.current.onDisconnect());
    act(() => result.current.onReconnect());
    expect(oldCountSignal?.aborted).toBe(true);
    expect(oldListSignal?.aborted).toBe(true);

    await act(async () => {
      oldCount.resolve({ ...count, unread: 99 });
      oldList.resolve(page([message("stale")]));
      await Promise.all([oldCountRequest, oldSnapshotRequest]);
    });
    expect(result.current.count?.unread).toBe(count.unread);
    expect(result.current.collection.items.map((item) => item.id)).toEqual(["cached"]);
    expect(result.current.disconnected).toBe(true);
    expect(result.current.countStale).toBe(true);
    expect(result.current.snapshotsStale).toBe(true);

    await act(async () => {
      await Promise.all([result.current.refreshCount(), result.current.refreshSnapshots()]);
    });
    expect(result.current.count?.unread).toBe(0);
    expect(result.current.collection.items.map((item) => item.id)).toEqual(["reconnected"]);
    expect(result.current.disconnected).toBe(false);
    expect(result.current.stale).toBe(false);
  });
});
