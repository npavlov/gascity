import { useCallback, useEffect, useRef, useState } from "react";

import type { MailAPI, MailCount, MailMessage, MailThread } from "@/lib/api";
import { ControlCenterAPIError } from "@/lib/api";

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
  requestRefresh(resources: MailRefreshResource[]): void;
}

export type MailRefreshResource = "count" | "snapshots";

export interface MailModel {
  collection: MailCollection;
  count: MailCount | null;
  detail: Identified<MailMessage> | null;
  thread: Identified<MailThread> | null;
  loading: MailLoading;
  errors: MailErrors;
  stale: boolean;
  countStale: boolean;
  snapshotsStale: boolean;
  disconnected: boolean;
  setFilter(filter: MailFilter): void;
  select(identity: MailIdentity): void;
  loadMore(): void;
  refresh(): void;
  refreshCount(signal?: AbortSignal): Promise<boolean>;
  refreshSnapshots(signal?: AbortSignal): Promise<boolean>;
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

export function useMail({ api, requestRefresh }: UseMailOptions): MailModel {
  const [collection, setCollection] = useState<MailCollection>(createMailCollection);
  const [count, setCount] = useState<MailCount | null>(null);
  const [detail, setDetail] = useState<Identified<MailMessage> | null>(null);
  const [thread, setThread] = useState<Identified<MailThread> | null>(null);
  const [loading, setLoading] = useState<MailLoading>(initialLoading);
  const [errors, setErrors] = useState<MailErrors>(initialErrors);
  const [countStale, setCountStale] = useState(false);
  const [listStale, setListStale] = useState(false);
  const [selectionStale, setSelectionStale] = useState(false);
  const [disconnected, setDisconnected] = useState(false);

  const mounted = useRef(true);
  const collectionRef = useRef(collection);
  const detailRef = useRef(detail);
  const threadRef = useRef(thread);
  const countStaleRef = useRef(false);
  const listStaleRef = useRef(false);
  const selectionStaleRef = useRef(false);
  const reconnectingRef = useRef(false);
  const snapshotGeneration = useRef(0);
  const countController = useRef<AbortController | null>(null);
  const listController = useRef<AbortController | null>(null);
  const selectionController = useRef<AbortController | null>(null);

  const invalidateInFlight = useCallback(() => {
    snapshotGeneration.current += 1;
    countController.current?.abort();
    listController.current?.abort();
    selectionController.current?.abort();
  }, []);

  const maybeFinishReconnect = useCallback(() => {
    if (
      reconnectingRef.current
      && !countStaleRef.current
      && !listStaleRef.current
      && !selectionStaleRef.current
    ) {
      reconnectingRef.current = false;
      if (mounted.current) setDisconnected(false);
    }
  }, []);

  const markCountStale = useCallback((value: boolean) => {
    countStaleRef.current = value;
    if (mounted.current) setCountStale(value);
  }, []);

  const markListStale = useCallback((value: boolean) => {
    listStaleRef.current = value;
    if (mounted.current) setListStale(value);
  }, []);

  const markSelectionStale = useCallback((value: boolean) => {
    selectionStaleRef.current = value;
    if (mounted.current) setSelectionStale(value);
  }, []);

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
      markCountStale(false);
      maybeFinishReconnect();
      return true;
    } catch (error) {
      if (!controller.signal.aborted && !isAbort(error) && mounted.current) {
        setErrors((current) => ({ ...current, count: true }));
        markCountStale(true);
      }
      return false;
    } finally {
      detach();
      if (countController.current === controller && mounted.current) {
        setLoading((current) => ({ ...current, count: false }));
      }
    }
  }, [api, markCountStale, maybeFinishReconnect]);

  const loadSelected = useCallback(async (identity: MailIdentity | null, parentSignal?: AbortSignal) => {
    selectionController.current?.abort();
    if (!api || identity === null) {
      updateDetail(null);
      updateThread(null);
      if (mounted.current) {
        setLoading((current) => ({ ...current, detail: false, thread: false }));
        setErrors((current) => ({ ...current, detail: false, thread: false, notFound: false }));
      }
      markSelectionStale(false);
      maybeFinishReconnect();
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
    markSelectionStale(!confirmed);
    maybeFinishReconnect();
    return confirmed;
  }, [api, markSelectionStale, maybeFinishReconnect, updateDetail, updateThread]);

  const refreshSnapshots = useCallback(async (parentSignal?: AbortSignal) => {
    if (!api) return false;
    const requiresFullRevalidation = listStaleRef.current || selectionStaleRef.current || reconnectingRef.current;
    if (requiresFullRevalidation) {
      markSelectionStale(true);
    }
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
      if (requiresFullRevalidation) markSelectionStale(true);
      markListStale(false);
    } catch (error) {
      if (!controller.signal.aborted && !isAbort(error) && mounted.current) {
        setErrors((value) => ({ ...value, list: true }));
        markListStale(true);
      }
      return false;
    } finally {
      detach();
      if (listController.current === controller && mounted.current) {
        setLoading((value) => ({ ...value, list: false }));
      }
    }
    return loadSelected(next.selected, parentSignal);
  }, [api, loadSelected, markListStale, markSelectionStale, updateCollection]);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      countController.current?.abort();
      listController.current?.abort();
      selectionController.current?.abort();
    };
  }, [api]);

  const setFilter = useCallback((filter: MailFilter) => {
    if (filter === collectionRef.current.filter) return;
    snapshotGeneration.current += 1;
    listController.current?.abort();
    markListStale(true);
    markSelectionStale(true);
    updateCollection({
      ...collectionRef.current,
      filter,
      pages: [],
      items: [],
      total: 0,
      nextCursor: "",
    });
    requestRefresh(["snapshots"]);
  }, [markListStale, markSelectionStale, requestRefresh, updateCollection]);

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
        markListStale(true);
      }
    }).finally(() => {
      if (listController.current === controller && mounted.current) setLoading((value) => ({ ...value, list: false }));
    });
  }, [api, markListStale, updateCollection]);

  const refresh = useCallback(() => {
    requestRefresh(["count", "snapshots"]);
  }, [requestRefresh]);

  const onInvalidation = useCallback(() => {
    invalidateInFlight();
    markCountStale(true);
    markListStale(true);
    markSelectionStale(true);
  }, [invalidateInFlight, markCountStale, markListStale, markSelectionStale]);

  const onDisconnect = useCallback(() => {
    invalidateInFlight();
    reconnectingRef.current = false;
    if (mounted.current) {
      setDisconnected(true);
    }
    markCountStale(true);
    markListStale(true);
    markSelectionStale(true);
  }, [invalidateInFlight, markCountStale, markListStale, markSelectionStale]);

  const onReconnect = useCallback(() => {
    invalidateInFlight();
    reconnectingRef.current = true;
    markCountStale(true);
    markListStale(true);
    markSelectionStale(true);
  }, [invalidateInFlight, markCountStale, markListStale, markSelectionStale]);

  const snapshotsStale = listStale || selectionStale;
  const stale = countStale || snapshotsStale || disconnected;

  return {
    collection,
    count,
    detail,
    thread,
    loading,
    errors,
    stale,
    countStale,
    snapshotsStale,
    disconnected,
    setFilter,
    select,
    loadMore,
    refresh,
    refreshCount,
    refreshSnapshots,
    onInvalidation,
    onDisconnect,
    onReconnect,
  };
}

export { identityFromMessage };
