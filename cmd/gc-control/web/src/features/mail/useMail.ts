import { useCallback, useEffect, useRef, useState } from "react";

import type { MailAPI, MailCount, MailMessage, MailThread } from "@/lib/api";
import { ControlCenterAPIError } from "@/lib/api";
import { createRefreshQueue } from "@/lib/events";

import {
  appendMailPage,
  createMailCollection,
  identityFromMessage,
  mailIdentityKey,
  replaceMailPages,
  selectMail,
} from "./mailState";
import type { LoadedMailPage, MailCollection, MailFilter, MailIdentity } from "./mailState";

interface Identified<T> {
  identity: MailIdentity;
  value: T;
}

interface MailLoading {
  count: boolean;
  list: boolean;
  detail: boolean;
  thread: boolean;
}

interface MailErrors {
  count: boolean;
  list: boolean;
  detail: boolean;
  thread: boolean;
  notFound: boolean;
}

export interface UseMailOptions {
  api: MailAPI | null;
  active: boolean;
  pollInterval?: number;
}

export interface MailModel {
  collection: MailCollection;
  count: MailCount | null;
  detail: Identified<MailMessage> | null;
  thread: Identified<MailThread> | null;
  loading: MailLoading;
  errors: MailErrors;
  stale: boolean;
  disconnected: boolean;
  setFilter(filter: MailFilter): void;
  select(identity: MailIdentity): void;
  loadMore(): void;
  refresh(): void;
  onInvalidation(): void;
  onDisconnect(): void;
  onReconnect(): void;
}

const initialLoading: MailLoading = { count: false, list: false, detail: false, thread: false };
const initialErrors: MailErrors = { count: false, list: false, detail: false, thread: false, notFound: false };

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

function sameIdentity(left: MailIdentity, right: MailIdentity) {
  return mailIdentityKey(left) === mailIdentityKey(right);
}

function linkedController(parent?: AbortSignal) {
  const controller = new AbortController();
  const abort = () => controller.abort();
  parent?.addEventListener("abort", abort, { once: true });
  if (parent?.aborted) controller.abort();
  return {
    controller,
    detach: () => parent?.removeEventListener("abort", abort),
  };
}

function requestStatus(error: unknown) {
  return error instanceof ControlCenterAPIError ? error.status : undefined;
}

export function useMail({ api, active, pollInterval = 10_000 }: UseMailOptions): MailModel {
  const [collection, setCollection] = useState<MailCollection>(createMailCollection);
  const [count, setCount] = useState<MailCount | null>(null);
  const [detail, setDetail] = useState<Identified<MailMessage> | null>(null);
  const [thread, setThread] = useState<Identified<MailThread> | null>(null);
  const [loading, setLoading] = useState<MailLoading>(initialLoading);
  const [errors, setErrors] = useState<MailErrors>(initialErrors);
  const [stale, setStale] = useState(false);
  const [disconnected, setDisconnected] = useState(false);

  const mounted = useRef(true);
  const activeRef = useRef(active);
  const previousActive = useRef(active);
  const collectionRef = useRef(collection);
  const detailRef = useRef(detail);
  const threadRef = useRef(thread);
  const disconnectedRef = useRef(disconnected);
  const invalidSnapshots = useRef(false);
  const snapshotGeneration = useRef(0);
  const queueRef = useRef<ReturnType<typeof createRefreshQueue> | null>(null);
  const countController = useRef<AbortController | null>(null);
  const listController = useRef<AbortController | null>(null);
  const selectionController = useRef<AbortController | null>(null);

  useEffect(() => {
    activeRef.current = active;
  }, [active]);

  const updateCollection = useCallback((next: MailCollection) => {
    collectionRef.current = next;
    if (mounted.current) setCollection(next);
  }, []);

  const updateDetail = useCallback((next: Identified<MailMessage> | null) => {
    detailRef.current = next;
    if (mounted.current) setDetail(next);
  }, []);

  const updateThread = useCallback((next: Identified<MailThread> | null) => {
    threadRef.current = next;
    if (mounted.current) setThread(next);
  }, []);

  const refreshCount = useCallback(async (parentSignal?: AbortSignal) => {
    if (!api) return false;
    countController.current?.abort();
    const { controller, detach } = linkedController(parentSignal);
    countController.current = controller;
    if (mounted.current) {
      setLoading((current) => ({ ...current, count: true }));
      setErrors((current) => ({ ...current, count: false }));
    }
    try {
      const next = await api.getMailCount(controller.signal);
      if (controller.signal.aborted || countController.current !== controller || !mounted.current) return false;
      setCount(next);
      return true;
    } catch (error) {
      if (!controller.signal.aborted && !isAbort(error) && mounted.current) {
        setErrors((current) => ({ ...current, count: true }));
        setStale(true);
      }
      return false;
    } finally {
      detach();
      if (countController.current === controller && mounted.current) {
        setLoading((current) => ({ ...current, count: false }));
      }
    }
  }, [api]);

  const loadSelected = useCallback(async (identity: MailIdentity | null, parentSignal?: AbortSignal) => {
    selectionController.current?.abort();
    if (!api || identity === null) {
      updateDetail(null);
      updateThread(null);
      if (mounted.current) {
        setLoading((current) => ({ ...current, detail: false, thread: false }));
        setErrors((current) => ({ ...current, detail: false, thread: false, notFound: false }));
      }
      return true;
    }
    const { controller, detach } = linkedController(parentSignal);
    selectionController.current = controller;
    const selectedRow = collectionRef.current.items.find((item) => mailIdentityKey(item) === mailIdentityKey(identity));
    const threadID = selectedRow?.thread_id ?? identity.id;
    if (!detailRef.current || !sameIdentity(detailRef.current.identity, identity)) updateDetail(null);
    if (!threadRef.current || !sameIdentity(threadRef.current.identity, identity)) updateThread(null);
    if (mounted.current) {
      setLoading((current) => ({ ...current, detail: true, thread: true }));
      setErrors((current) => ({ ...current, detail: false, thread: false, notFound: false }));
    }
    const [detailResult, threadResult] = await Promise.allSettled([
      api.getMail(identity.id, identity.rig, controller.signal),
      api.getMailThread(threadID, identity.rig, controller.signal),
    ]);
    detach();
    if (controller.signal.aborted || selectionController.current !== controller || !mounted.current) return false;
    let confirmed = true;
    if (detailResult.status === "fulfilled") {
      updateDetail({ identity, value: detailResult.value });
    } else if (!isAbort(detailResult.reason)) {
      confirmed = false;
      setErrors((current) => ({ ...current, detail: true, notFound: requestStatus(detailResult.reason) === 404 }));
    }
    if (threadResult.status === "fulfilled") {
      updateThread({ identity, value: threadResult.value });
    } else if (!isAbort(threadResult.reason)) {
      confirmed = false;
      setErrors((current) => ({ ...current, thread: true }));
    }
    setLoading((current) => ({ ...current, detail: false, thread: false }));
    if (!confirmed) setStale(true);
    return confirmed;
  }, [api, updateDetail, updateThread]);

  const refreshSnapshots = useCallback(async (parentSignal?: AbortSignal) => {
    if (!api) return false;
    listController.current?.abort();
    selectionController.current?.abort();
    const generation = ++snapshotGeneration.current;
    const { controller, detach } = linkedController(parentSignal);
    listController.current = controller;
    const current = collectionRef.current;
    const requestedFilter = current.filter;
    const cursors = current.pages.length > 0 ? current.pages.map((page) => page.cursor) : [""];
    if (mounted.current) {
      setLoading((value) => ({ ...value, list: true }));
      setErrors((value) => ({ ...value, list: false }));
    }
    let next: MailCollection;
    try {
      const pages = await Promise.all(cursors.map(async (cursor): Promise<LoadedMailPage> => ({
        cursor,
        value: await api.listMail(requestedFilter, cursor || undefined, 50, controller.signal),
      })));
      if (
        controller.signal.aborted
        || listController.current !== controller
        || generation !== snapshotGeneration.current
        || !mounted.current
      ) return false;
      const latest = collectionRef.current;
      if (latest.filter !== requestedFilter) return false;
      next = replaceMailPages(latest, requestedFilter, pages);
      updateCollection(next);
      invalidSnapshots.current = false;
    } catch (error) {
      if (!controller.signal.aborted && !isAbort(error) && mounted.current) {
        setErrors((value) => ({ ...value, list: true }));
        setStale(true);
      }
      return false;
    } finally {
      detach();
      if (listController.current === controller && mounted.current) {
        setLoading((value) => ({ ...value, list: false }));
      }
    }
    return loadSelected(next.selected, parentSignal);
  }, [api, loadSelected, updateCollection]);

  useEffect(() => {
    mounted.current = true;
    if (!api) return () => { mounted.current = false; };
    const queue = createRefreshQueue(async (resources, signal) => {
      const requested = new Set(resources);
      const results: boolean[] = [];
      if (requested.has("count")) results.push(await refreshCount(signal));
      if (requested.has("snapshots")) results.push(await refreshSnapshots(signal));
      const confirmed = results.length > 0 && results.every(Boolean);
      if (requested.has("reconnect")) {
        if (mounted.current) setDisconnected(false);
        disconnectedRef.current = false;
      }
      if (confirmed && !invalidSnapshots.current && !disconnectedRef.current && mounted.current) setStale(false);
    });
    queueRef.current = queue;
    queue.request(activeRef.current ? ["count", "snapshots"] : ["count"]);

    const requestVisible = () => {
      if (document.visibilityState !== "visible") return;
      queue.request(activeRef.current ? ["count", "snapshots"] : ["count"]);
    };
    const interval = window.setInterval(requestVisible, pollInterval);
    window.addEventListener("focus", requestVisible);
    const onVisibility = () => {
      if (document.visibilityState === "visible") requestVisible();
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      mounted.current = false;
      window.clearInterval(interval);
      window.removeEventListener("focus", requestVisible);
      document.removeEventListener("visibilitychange", onVisibility);
      queue.dispose();
      if (queueRef.current === queue) queueRef.current = null;
      countController.current?.abort();
      listController.current?.abort();
      selectionController.current?.abort();
    };
  }, [api, pollInterval, refreshCount, refreshSnapshots]);

  useEffect(() => {
    const becameActive = active && !previousActive.current;
    previousActive.current = active;
    if (becameActive) queueRef.current?.request(["snapshots"]);
  }, [active]);

  const setFilter = useCallback((filter: MailFilter) => {
    if (filter === collectionRef.current.filter) return;
    snapshotGeneration.current += 1;
    listController.current?.abort();
    updateCollection({
      ...collectionRef.current,
      filter,
      pages: [],
      items: [],
      total: 0,
      nextCursor: "",
    });
    invalidSnapshots.current = true;
    queueRef.current?.request(["snapshots"]);
  }, [updateCollection]);

  const select = useCallback((identity: MailIdentity) => {
    const next = selectMail(collectionRef.current, identity);
    updateCollection(next);
    void loadSelected(identity);
  }, [loadSelected, updateCollection]);

  const loadMore = useCallback(() => {
    if (!api) return;
    const current = collectionRef.current;
    const cursor = current.nextCursor;
    if (!cursor) return;
    listController.current?.abort();
    const { controller } = linkedController();
    listController.current = controller;
    if (mounted.current) {
      setLoading((value) => ({ ...value, list: true }));
      setErrors((value) => ({ ...value, list: false }));
    }
    void api.listMail(current.filter, cursor, 50, controller.signal).then((page) => {
      if (controller.signal.aborted || listController.current !== controller || !mounted.current) return;
      updateCollection(appendMailPage(collectionRef.current, cursor, page));
    }).catch((error: unknown) => {
      if (!controller.signal.aborted && !isAbort(error) && mounted.current) {
        setErrors((value) => ({ ...value, list: true }));
        setStale(true);
      }
    }).finally(() => {
      if (listController.current === controller && mounted.current) setLoading((value) => ({ ...value, list: false }));
    });
  }, [api, updateCollection]);

  const refresh = useCallback(() => {
    queueRef.current?.request(["count", "snapshots"]);
  }, []);

  const onInvalidation = useCallback(() => {
    invalidSnapshots.current = true;
    if (mounted.current) setStale(true);
    queueRef.current?.request(activeRef.current ? ["count", "snapshots"] : ["count"]);
  }, []);

  const onDisconnect = useCallback(() => {
    disconnectedRef.current = true;
    if (mounted.current) {
      setDisconnected(true);
      setStale(true);
    }
  }, []);

  const onReconnect = useCallback(() => {
    invalidSnapshots.current = true;
    queueRef.current?.request(["count", "snapshots", "reconnect"]);
  }, []);

  return {
    collection,
    count,
    detail,
    thread,
    loading,
    errors,
    stale,
    disconnected,
    setFilter,
    select,
    loadMore,
    refresh,
    onInvalidation,
    onDisconnect,
    onReconnect,
  };
}

export { identityFromMessage };
