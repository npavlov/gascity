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

async function settle() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

afterEach(() => {
  vi.useRealTimers();
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
});

describe("useMail", () => {
  it("loads count globally and snapshots only while Mail is active", async () => {
    vi.useFakeTimers();
    const api = fakeMailAPI();
    const { result, rerender } = renderHook(({ active }) => useMail({ api, active, pollInterval: 10_000 }), {
      initialProps: { active: false },
    });

    await settle();
    expect(api.getMailCount).toHaveBeenCalledOnce();
    expect(api.listMail).not.toHaveBeenCalled();

    rerender({ active: true });
    await settle();
    expect(api.listMail).toHaveBeenCalledWith("unread", undefined, 50, expect.any(AbortSignal));
    expect(api.getMail).toHaveBeenCalledWith("a", undefined, expect.any(AbortSignal));
    expect(api.getMailThread).toHaveBeenCalledWith("thread-a", undefined, expect.any(AbortSignal));
    expect(result.current.collection.selected).toEqual({ id: "a", rig: undefined });

    vi.mocked(api.getMailCount).mockClear();
    vi.mocked(api.listMail).mockClear();
    rerender({ active: false });
    await act(async () => { vi.advanceTimersByTime(10_000); });
    await settle();
    expect(api.getMailCount).toHaveBeenCalledOnce();
    expect(api.listMail).not.toHaveBeenCalled();

    rerender({ active: true });
    await settle();
    vi.mocked(api.getMailCount).mockClear();
    vi.mocked(api.listMail).mockClear();
    await act(async () => { vi.advanceTimersByTime(10_000); });
    await settle();
    expect(api.getMailCount).toHaveBeenCalledOnce();
    expect(api.listMail).toHaveBeenCalled();
  });

  it("selects with detail and thread GETs only and forwards the exact rig identity", async () => {
    const api = fakeMailAPI();
    const { result } = renderHook(() => useMail({ api, active: true }));
    await waitFor(() => expect(result.current.collection.items).toHaveLength(2));
    vi.mocked(api.getMail).mockClear();
    vi.mocked(api.getMailThread).mockClear();

    act(() => result.current.select({ id: "b", rig: "rig-two" }));
    await waitFor(() => expect(api.getMail).toHaveBeenCalledWith("b", "rig-two", expect.any(AbortSignal)));
    expect(api.getMailThread).toHaveBeenCalledWith("thread-b", "rig-two", expect.any(AbortSignal));
    expect(Object.keys(api).sort()).toEqual(["getMail", "getMailCount", "getMailThread", "listMail"]);
  });

  it("retains last-confirmed rows when an invalidated refresh fails", async () => {
    const listMail = vi.fn<MailAPI["listMail"]>()
      .mockResolvedValueOnce(page([message("a")]))
      .mockRejectedValueOnce(new Error("offline"));
    const api = fakeMailAPI({ listMail });
    const { result } = renderHook(() => useMail({ api, active: true }));
    await waitFor(() => expect(result.current.collection.items.map((item) => item.id)).toEqual(["a"]));

    act(() => result.current.onInvalidation());
    await waitFor(() => expect(result.current.errors.list).toBe(true));
    expect(result.current.collection.items.map((item) => item.id)).toEqual(["a"]);
    expect(result.current.stale).toBe(true);
  });

  it("aborts and generation-guards an old filter refresh while the new first page loads", async () => {
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
    const api = fakeMailAPI({ listMail });
    const { result } = renderHook(() => useMail({ api, active: true }));
    await waitFor(() => expect(result.current.collection.items.map((item) => item.id)).toEqual(["a", "b"]));

    act(() => result.current.refresh());
    await waitFor(() => expect(listMail).toHaveBeenCalledTimes(2));
    act(() => result.current.setFilter("all"));

    expect(oldSignal?.aborted).toBe(true);
    expect(result.current.collection.filter).toBe("all");
    expect(result.current.collection.items).toEqual([]);

    await act(async () => { oldUnread.resolve(page([message("stale-unread")])); });
    await waitFor(() => expect(listMail).toHaveBeenCalledTimes(3));
    expect(result.current.collection.filter).toBe("all");
    expect(result.current.collection.items).toEqual([]);

    await act(async () => { nextAll.resolve(page([message("all-a"), message("all-b")])); });
    await waitFor(() => expect(result.current.collection.items.map((item) => item.id)).toEqual(["all-a", "all-b"]));
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
    const { result } = renderHook(() => useMail({ api, active: true }));
    await waitFor(() => expect(result.current.collection.items).toHaveLength(2));

    act(() => result.current.refresh());
    await waitFor(() => expect(listMail).toHaveBeenCalledTimes(2));
    act(() => result.current.select({ id: "same", rig: "two" }));
    expect(result.current.collection.selected).toEqual({ id: "same", rig: "two" });

    await act(async () => { refreshed.resolve(page([first, second])); });
    await waitFor(() => expect(result.current.loading.list).toBe(false));
    expect(result.current.collection.selected).toEqual({ id: "same", rig: "two" });
    await waitFor(() => expect(result.current.detail?.identity).toEqual({ id: "same", rig: "two" }));
  });

  it("pauses hidden polling and refreshes immediately on visible restore and focus", async () => {
    vi.useFakeTimers();
    const api = fakeMailAPI();
    renderHook(() => useMail({ api, active: true, pollInterval: 10_000 }));
    await settle();
    vi.mocked(api.getMailCount).mockClear();
    vi.mocked(api.listMail).mockClear();

    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    await act(async () => { vi.advanceTimersByTime(30_000); });
    expect(api.getMailCount).not.toHaveBeenCalled();
    expect(api.listMail).not.toHaveBeenCalled();

    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await settle();
    expect(api.getMailCount).toHaveBeenCalledOnce();
    expect(api.listMail).toHaveBeenCalledOnce();

    vi.mocked(api.getMailCount).mockClear();
    vi.mocked(api.listMail).mockClear();
    act(() => window.dispatchEvent(new Event("focus")));
    await settle();
    expect(api.getMailCount).toHaveBeenCalledOnce();
    expect(api.listMail).toHaveBeenCalledOnce();
  });

  it("coalesces event and poll bursts into one in-flight count plus one trailing rerun", async () => {
    vi.useFakeTimers();
    let release: ((value: MailCount) => void) | undefined;
    const getMailCount = vi.fn<MailAPI["getMailCount"]>()
      .mockImplementationOnce(() => new Promise<MailCount>((resolve) => { release = resolve; }))
      .mockResolvedValue(count);
    const api = fakeMailAPI({ getMailCount });
    const { result } = renderHook(() => useMail({ api, active: false, pollInterval: 10_000 }));
    await settle();
    expect(getMailCount).toHaveBeenCalledOnce();

    act(() => {
      result.current.onInvalidation();
      result.current.onInvalidation();
      vi.advanceTimersByTime(10_000);
    });
    expect(getMailCount).toHaveBeenCalledOnce();
    await act(async () => { release?.(count); });
    await settle();
    expect(getMailCount).toHaveBeenCalledTimes(2);
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
    const { result, unmount } = renderHook(() => useMail({ api, active: true }));
    await waitFor(() => expect(result.current.collection.items).toHaveLength(2));

    act(() => result.current.select({ id: "b", rig: "rig-two" }));
    await waitFor(() => expect(signals).toHaveLength(1));
    act(() => result.current.select({ id: "a" }));
    expect(signals[0].aborted).toBe(true);

    act(() => result.current.select({ id: "b", rig: "rig-two" }));
    await waitFor(() => expect(signals).toHaveLength(2));
    unmount();
    expect(signals[1].aborted).toBe(true);
  });

  it("marks disconnect stale and performs a full count/list/detail/thread refresh after reconnect", async () => {
    const api = fakeMailAPI();
    const { result, rerender } = renderHook(({ active }) => useMail({ api, active }), { initialProps: { active: true } });
    await waitFor(() => expect(result.current.detail?.value.id).toBe("a"));
    rerender({ active: false });
    vi.mocked(api.getMailCount).mockClear();
    vi.mocked(api.listMail).mockClear();
    vi.mocked(api.getMail).mockClear();
    vi.mocked(api.getMailThread).mockClear();

    act(() => result.current.onDisconnect());
    expect(result.current.disconnected).toBe(true);
    expect(result.current.stale).toBe(true);
    act(() => result.current.onReconnect());

    await waitFor(() => expect(api.getMailCount).toHaveBeenCalled());
    await waitFor(() => expect(api.listMail).toHaveBeenCalled());
    expect(api.getMail).toHaveBeenCalledWith("a", undefined, expect.any(AbortSignal));
    expect(api.getMailThread).toHaveBeenCalledWith("thread-a", undefined, expect.any(AbortSignal));
    await waitFor(() => expect(result.current.disconnected).toBe(false));
    expect(result.current.stale).toBe(false);
  });
});
