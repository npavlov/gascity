import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { MayorRequestError } from "@/lib/api";
import type {
  MayorAPI,
  MayorEvent,
  MayorState,
  MayorView,
  MessageReceipt,
  TranscriptPage,
  TranscriptTurn,
} from "@/lib/api";
import type { MayorFailureState, MayorMutation } from "./types";

export interface MayorStreamCallbacks {
  onEvent(event: MayorEvent): void;
  onStale(): void;
  onReconnect(): Promise<void>;
}

export type MayorStreamConnector = (callbacks: MayorStreamCallbacks) => () => void;

export type MayorRefreshResource = "mayor-status" | "mayor-snapshots";
export type MayorRefreshRequester = (resources: MayorRefreshResource[]) => Promise<void>;

export interface UseMayorOptions {
  api: MayorAPI | null;
  connector?: MayorStreamConnector;
  requestRefresh?: MayorRefreshRequester;
}

export interface MayorController {
  view: MayorView | null;
  turns: TranscriptTurn[];
  loading: boolean;
  stale: boolean;
  error: string | null;
  failureState: MayorFailureState | null;
  liveTurn: TranscriptTurn | null;
  composerDraft: string;
  setComposerDraft(value: string): void;
  pendingDraft: string;
  setPendingDraft(value: string): void;
  receipt: MessageReceipt | null;
  mutation: MayorMutation | null;
  mutationError: string | null;
  scrollOffset: number;
  setScrollOffset(value: number): void;
  refresh(): Promise<void>;
  refreshStatus(signal?: AbortSignal): Promise<boolean>;
  refreshSnapshots(signal?: AbortSignal): Promise<boolean>;
  loadOlder(): Promise<void>;
  send(): Promise<void>;
  respond(action: string): Promise<void>;
  canSend: boolean;
  hasOlder: boolean;
  loadingOlder: boolean;
}

const reconnectDelays = [1_000, 2_000, 4_000, 8_000, 10_000] as const;

interface MayorEventSourceLike {
  onopen: ((event: Event) => void) | null;
  onerror: ((event: Event) => void) | null;
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void): void;
  removeEventListener(type: string, listener: (event: MessageEvent<string>) => void): void;
  close(): void;
}

export const connectMayorEvents: MayorStreamConnector = (callbacks) => {
  let source: MayorEventSourceLike | null = null;
  let reconnectTimer: number | null = null;
  let stopped = false;
  let attempt = 0;
  let disconnectCurrent: (() => void) | null = null;

  const connect = () => {
    if (stopped) return;
    const current = new EventSource("/api/v1/mayor/events") as MayorEventSourceLike;
    source = current;
    let opened = false;
    const onMayor = (frame: MessageEvent<string>) => {
      if (stopped || source !== current) return;
      try {
        const event: unknown = JSON.parse(frame.data);
        if (!isMayorEvent(event)) return;
        attempt = 0;
        callbacks.onEvent(event);
      } catch {
        return;
      }
    };
    current.addEventListener("mayor", onMayor);
    const disconnect = () => {
      current.removeEventListener("mayor", onMayor);
      current.onopen = null;
      current.onerror = null;
      current.close();
      if (source === current) source = null;
      if (disconnectCurrent === disconnect) disconnectCurrent = null;
    };
    disconnectCurrent = disconnect;
    current.onopen = () => {
      if (stopped || source !== current || opened) return;
      opened = true;
      attempt = 0;
      void callbacks.onReconnect().catch(() => callbacks.onStale());
    };
    current.onerror = () => {
      if (stopped || source !== current) return;
      callbacks.onStale();
      disconnect();
      const delay = reconnectDelays[Math.min(attempt, reconnectDelays.length - 1)];
      attempt += 1;
      reconnectTimer = window.setTimeout(() => {
        reconnectTimer = null;
        connect();
      }, delay);
    };
  };

  connect();
  return () => {
    stopped = true;
    if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
    disconnectCurrent?.();
  };
};

interface LiveTurnRecord {
  turn: TranscriptTurn;
}

type TranscriptPagination = Pick<TranscriptPage, "before" | "has_older">;

type RequestLane = "view" | "transcript" | "older";

interface RequestHandle {
  controller: AbortController;
  signal: AbortSignal;
  finish(): void;
}

export function useMayor({ api, connector = connectMayorEvents, requestRefresh }: UseMayorOptions): MayorController {
  const [view, setView] = useState<MayorView | null>(null);
  const [pagination, setPagination] = useState<TranscriptPagination | null>(null);
  const [turns, setTurns] = useState<TranscriptTurn[]>([]);
  const [loading, setLoading] = useState(api !== null);
  const [streamStale, setStreamStale] = useState(false);
  const [snapshotStale, setSnapshotStale] = useState(false);
  const [statusStale, setStatusStale] = useState(false);
  const [snapshotError, setSnapshotError] = useState<string | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [failureState, setFailureState] = useState<MayorFailureState | null>(null);
  const [liveTurn, setLiveTurn] = useState<TranscriptTurn | null>(null);
  const [composerDraft, setComposerDraft] = useState("");
  const [pendingDraft, setPendingDraft] = useState("");
  const [receipt, setReceipt] = useState<MessageReceipt | null>(null);
  const [mutation, setMutation] = useState<MayorMutation | null>(null);
  const [mutationError, setMutationError] = useState<string | null>(null);
  const [scrollOffset, setScrollOffset] = useState(0);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [streamBarrierEpoch, setStreamBarrierEpoch] = useState<number | null>(null);
  const activeControllers = useRef(new Set<AbortController>());
  const laneControllers = useRef<Record<RequestLane, AbortController | null>>({ view: null, transcript: null, older: null });
  const lifecycleGeneration = useRef(0);
  const hasLastGood = useRef(false);
  const liveViewRevision = useRef(0);
  const viewRequestSerial = useRef(0);
  const transcriptRequestSerial = useRef(0);
  const streamRevision = useRef(0);
  const snapshotDemandRevision = useRef(0);
  const lastSnapshotConfirmedRevision = useRef(-1);
  const unconfirmedLiveTurns = useRef<LiveTurnRecord[]>([]);
  const authoritativeHistory = useRef<TranscriptTurn[]>([]);
  const hasLoadedOlder = useRef(false);
  const turnsRef = useRef<TranscriptTurn[]>([]);
  const activeSessionID = useRef<string | null>(null);
  const hasSessionIdentity = useRef(false);
  const sessionEpoch = useRef(0);
  const pendingRequestID = useRef<string | null>(null);
  const mutationActive = useRef(false);

  const abortLane = useCallback((lane: RequestLane, except?: AbortController) => {
    const controller = laneControllers.current[lane];
    if (controller && controller !== except) controller.abort();
  }, []);

  const beginLane = useCallback((lane: RequestLane, externalSignal?: AbortSignal): RequestHandle => {
    abortLane(lane);
    const controller = new AbortController();
    laneControllers.current[lane] = controller;
    activeControllers.current.add(controller);
    const forwardAbort = () => controller.abort();
    if (externalSignal?.aborted) controller.abort();
    else externalSignal?.addEventListener("abort", forwardAbort, { once: true });
    return {
      controller,
      signal: controller.signal,
      finish() {
        externalSignal?.removeEventListener("abort", forwardAbort);
        activeControllers.current.delete(controller);
        if (laneControllers.current[lane] === controller) laneControllers.current[lane] = null;
      },
    };
  }, [abortLane]);

  const acceptSessionIdentity = useCallback((nextSessionID: string | null, currentTranscript?: AbortController): boolean => {
    const changed = hasSessionIdentity.current && activeSessionID.current !== nextSessionID;
    activeSessionID.current = nextSessionID;
    hasSessionIdentity.current = true;
    if (!changed) return false;

    sessionEpoch.current += 1;
    abortLane("transcript", currentTranscript);
    abortLane("older");
    authoritativeHistory.current = [];
    hasLoadedOlder.current = false;
    unconfirmedLiveTurns.current = [];
    turnsRef.current = [];
    setStreamBarrierEpoch(null);
    setPagination(null);
    setTurns([]);
    setLiveTurn(null);
    setScrollOffset(0);
    setLoadingOlder(false);
    return true;
  }, [abortLane]);

  const refreshSnapshots = useCallback(async (signal?: AbortSignal): Promise<boolean> => {
    if (!api) {
      setLoading(false);
      return false;
    }
    const viewRequest = beginLane("view", signal);
    const transcriptRequest = beginLane("transcript", signal);
    const generation = lifecycleGeneration.current;
    const demandRevision = snapshotDemandRevision.current;
    const startedAtRevision = liveViewRevision.current;
    const requestSerial = ++viewRequestSerial.current;
    const transcriptSerial = ++transcriptRequestSerial.current;
    try {
      const settleView = async (): Promise<MayorView | null> => {
        try {
          const normalizedView = normalizeView(await api.getMayor(viewRequest.signal));
          if (viewRequest.signal.aborted || generation !== lifecycleGeneration.current || requestSerial !== viewRequestSerial.current) return null;
          acceptSessionIdentity(normalizedView.session_id ?? null, transcriptRequest.controller);
          setView((current) => preserveLiveView(normalizedView, current, startedAtRevision === liveViewRevision.current));
          hasLastGood.current = true;
          setStatusError(null);
          setFailureState(null);
          setStatusStale(false);
          setLoading(false);
          return normalizedView;
        } catch (cause) {
          if (viewRequest.signal.aborted || generation !== lifecycleGeneration.current || requestSerial !== viewRequestSerial.current) return null;
          setStatusError(errorMessage(cause));
          setStatusStale(true);
          if (!hasLastGood.current) setFailureState(classifyFailure(cause));
          setLoading(false);
          return null;
        } finally {
          viewRequest.finish();
        }
      };

      const settleTranscript = async (): Promise<boolean> => {
        try {
          const normalizedTranscript = normalizeTranscript(await api.getMayorTranscript(undefined, transcriptRequest.signal));
          const confirmedView = await viewLane;
          if (
            transcriptRequest.signal.aborted
            || generation !== lifecycleGeneration.current
            || requestSerial !== viewRequestSerial.current
            || transcriptSerial !== transcriptRequestSerial.current
          ) return false;
          if (!confirmedView) return false;
          const responseSessionID = normalizedTranscript.session_id?.trim();
          if (confirmedView.materialized) {
            if (!responseSessionID || responseSessionID !== confirmedView.session_id || responseSessionID !== activeSessionID.current) {
              throw new Error("Mayor transcript belongs to a different session");
            }
          } else if (responseSessionID || (normalizedTranscript.turns?.length ?? 0) > 0) {
            throw new Error("Dormant Mayor transcript unexpectedly names a session");
          }
          const merged = mergeAuthoritativeTail(authoritativeHistory.current, normalizedTranscript.turns ?? []);
          authoritativeHistory.current = merged.turns;
          unconfirmedLiveTurns.current = reconcileLiveTurns(merged.appended, unconfirmedLiveTurns.current);
          const visibleTurns = [...merged.turns, ...unconfirmedLiveTurns.current.map(({ turn }) => turn)];
          turnsRef.current = visibleTurns;
          if (!hasLoadedOlder.current) {
            setPagination({ before: normalizedTranscript.before, has_older: normalizedTranscript.has_older });
          }
          setTurns(visibleTurns);
          setSnapshotError(null);
          return true;
        } catch (cause) {
          if (
            transcriptRequest.signal.aborted
            || generation !== lifecycleGeneration.current
            || requestSerial !== viewRequestSerial.current
            || transcriptSerial !== transcriptRequestSerial.current
          ) return false;
          setSnapshotError(errorMessage(cause));
          return false;
        } finally {
          transcriptRequest.finish();
        }
      };

      const viewLane = settleView();
      const transcriptLane = settleTranscript();
      const [confirmedView, transcriptConfirmed] = await Promise.all([viewLane, transcriptLane]);
      if (generation !== lifecycleGeneration.current) return false;
      if (requestSerial !== viewRequestSerial.current || transcriptSerial !== transcriptRequestSerial.current) return false;

      const confirmed = confirmedView !== null && transcriptConfirmed;
      if (confirmed) {
        lastSnapshotConfirmedRevision.current = Math.max(lastSnapshotConfirmedRevision.current, demandRevision);
        setStreamBarrierEpoch(sessionEpoch.current);
        if (demandRevision === snapshotDemandRevision.current) setSnapshotStale(false);
      } else {
        setSnapshotStale(true);
      }
      return confirmed;
    } finally {
      viewRequest.finish();
      transcriptRequest.finish();
    }
  }, [acceptSessionIdentity, api, beginLane]);

  const refreshStatus = useCallback(async (signal?: AbortSignal): Promise<boolean> => {
    if (!api) {
      setLoading(false);
      return false;
    }
    abortLane("transcript");
    transcriptRequestSerial.current += 1;
    const viewRequest = beginLane("view", signal);
    const generation = lifecycleGeneration.current;
    const startedAtRevision = liveViewRevision.current;
    const requestSerial = ++viewRequestSerial.current;
    try {
      const nextView = normalizeView(await api.getMayor(viewRequest.signal));
      if (viewRequest.signal.aborted || generation !== lifecycleGeneration.current) return false;
      if (requestSerial === viewRequestSerial.current) {
        const sessionChanged = acceptSessionIdentity(nextView.session_id ?? null);
        setView((current) => preserveLiveView(nextView, current, startedAtRevision === liveViewRevision.current));
        hasLastGood.current = true;
        setStatusError(null);
        setStatusStale(false);
        setFailureState(null);
        setLoading(false);
        if (sessionChanged) {
          const epoch = sessionEpoch.current;
          queueMicrotask(() => {
            if (generation !== lifecycleGeneration.current || epoch !== sessionEpoch.current) return;
            const baseline = requestRefresh ? requestRefresh(["mayor-snapshots"]) : refreshSnapshots();
            void baseline.catch((cause) => {
              if (generation !== lifecycleGeneration.current || epoch !== sessionEpoch.current) return;
              setSnapshotError(errorMessage(cause));
              setSnapshotStale(true);
            });
          });
        }
      }
      return true;
    } catch (cause) {
      if (viewRequest.signal.aborted || generation !== lifecycleGeneration.current) return false;
      if (requestSerial === viewRequestSerial.current) {
        setStatusError(errorMessage(cause));
        setStatusStale(true);
        if (!hasLastGood.current) setFailureState(classifyFailure(cause));
        setLoading(false);
      }
      return false;
    } finally {
      viewRequest.finish();
    }
  }, [abortLane, acceptSessionIdentity, api, beginLane, refreshSnapshots, requestRefresh]);

  const requestSnapshotRefresh = useCallback(async (): Promise<boolean> => {
    const demandRevision = ++snapshotDemandRevision.current;
    if (requestRefresh) {
      await requestRefresh(["mayor-snapshots"]);
    } else {
      await refreshSnapshots();
    }
    return lastSnapshotConfirmedRevision.current >= demandRevision;
  }, [refreshSnapshots, requestRefresh]);

  const refresh = useCallback(async () => { await requestSnapshotRefresh(); }, [requestSnapshotRefresh]);

  useEffect(() => {
    let active = true;
    const controllers = activeControllers.current;
    const generation = ++lifecycleGeneration.current;
    authoritativeHistory.current = [];
    hasLoadedOlder.current = false;
    unconfirmedLiveTurns.current = [];
    turnsRef.current = [];
    activeSessionID.current = null;
    hasSessionIdentity.current = false;
    sessionEpoch.current += 1;
    queueMicrotask(() => {
      if (!active || generation !== lifecycleGeneration.current) return;
      void refreshSnapshots();
    });
    return () => {
      active = false;
      lifecycleGeneration.current += 1;
      sessionEpoch.current += 1;
      controllers.forEach((controller) => controller.abort());
      controllers.clear();
      laneControllers.current = { view: null, transcript: null, older: null };
    };
  }, [refreshSnapshots]);

  useEffect(() => {
    const nextRequestID = view?.pending?.request_id ?? null;
    if (pendingRequestID.current === nextRequestID) return;
    pendingRequestID.current = nextRequestID;
    setPendingDraft("");
  }, [view?.pending?.request_id]);

  const markStreamStale = useCallback(() => {
    streamRevision.current += 1;
    setStreamStale(true);
  }, []);

  const onEvent = useCallback((event: MayorEvent, expectedEpoch: number, expectedSessionID: string) => {
    if (event.kind === "stale") {
      markStreamStale();
      return;
    }
    if (event.kind === "invalidate") {
      setSnapshotStale(true);
      const startedAtRevision = streamRevision.current;
      void requestSnapshotRefresh().then((confirmed) => {
        if (confirmed && startedAtRevision === streamRevision.current) setStreamStale(false);
      });
      return;
    }
    if (
      expectedEpoch !== sessionEpoch.current
      || expectedSessionID !== activeSessionID.current
      || event.session_id?.trim() !== expectedSessionID
    ) return;
    if (event.kind === "turn" && event.turn) {
      unconfirmedLiveTurns.current.push({ turn: event.turn });
      turnsRef.current = [...turnsRef.current, event.turn];
      setTurns(turnsRef.current);
      setLiveTurn(event.turn);
      return;
    }
    if (event.kind === "activity" && event.activity) {
      liveViewRevision.current += 1;
      setView((current) => current ? { ...current, activity: event.activity, state: event.activity === "in-turn" ? "in_turn" : event.activity === "idle" ? "idle" : current.state } : current);
      return;
    }
    if (event.kind === "pending") {
      liveViewRevision.current += 1;
      setView((current) => current ? { ...current, pending: event.pending } : current);
      return;
    }
  }, [markStreamStale, requestSnapshotRefresh]);

  useEffect(() => {
    const expectedSessionID = activeSessionID.current;
    const expectedEpoch = sessionEpoch.current;
    if (!api || !expectedSessionID || streamBarrierEpoch !== expectedEpoch) return;
    const isCurrentEpoch = () => expectedEpoch === sessionEpoch.current && expectedSessionID === activeSessionID.current;
    return connector({
      onEvent: (event) => onEvent(event, expectedEpoch, expectedSessionID),
      onStale: () => { if (isCurrentEpoch()) markStreamStale(); },
      onReconnect: async () => {
        if (!isCurrentEpoch()) return;
        const startedAtRevision = streamRevision.current;
        const confirmed = await requestSnapshotRefresh();
        if (isCurrentEpoch() && confirmed && startedAtRevision === streamRevision.current) setStreamStale(false);
      },
    });
  }, [api, connector, markStreamStale, onEvent, requestSnapshotRefresh, streamBarrierEpoch]);

  const loadOlder = useCallback(async () => {
    if (!api || loadingOlder || !pagination?.has_older || !pagination.before) return;
    setLoadingOlder(true);
    const generation = lifecycleGeneration.current;
    const pageEpoch = sessionEpoch.current;
    const pageSessionID = activeSessionID.current;
    const before = pagination.before;
    const request = beginLane("older");
    try {
      const older = normalizeTranscript(await api.getMayorTranscript(before, request.signal));
      if (request.signal.aborted || generation !== lifecycleGeneration.current || pageEpoch !== sessionEpoch.current) return;
      if (!pageSessionID || older.session_id?.trim() !== pageSessionID || activeSessionID.current !== pageSessionID) {
        throw new Error("Older Mayor transcript belongs to a different session");
      }
      const olderTurns = older.turns ?? [];
      const merged = mergeTranscriptPages(olderTurns, authoritativeHistory.current);
      authoritativeHistory.current = merged.turns;
      const visibleTurns = [...merged.turns, ...unconfirmedLiveTurns.current.map(({ turn }) => turn)];
      turnsRef.current = visibleTurns;
      setTurns(visibleTurns);
      hasLoadedOlder.current = true;
      setPagination({ before: older.before, has_older: older.has_older });
    } catch (cause) {
      if (request.signal.aborted || generation !== lifecycleGeneration.current || pageEpoch !== sessionEpoch.current) return;
      setMutationError(errorMessage(cause));
    } finally {
      request.finish();
      if (generation === lifecycleGeneration.current && pageEpoch === sessionEpoch.current) setLoadingOlder(false);
    }
  }, [api, beginLane, loadingOlder, pagination]);

  const send = useCallback(async () => {
    if (!api || mutationActive.current || !canSendMayor(view, composerDraft)) return;
    mutationActive.current = true;
    setMutation("send");
    setMutationError(null);
    setReceipt(null);
    try {
      const nextReceipt = await api.sendMayorMessage(composerDraft);
      setReceipt(nextReceipt);
      setComposerDraft("");
      await requestSnapshotRefresh();
    } catch (cause) {
      setMutationError(errorMessage(cause));
    } finally {
      mutationActive.current = false;
      setMutation(null);
    }
  }, [api, composerDraft, requestSnapshotRefresh, view]);

  const respond = useCallback(async (action: string) => {
    const pending = view?.pending;
    if (!api || !pending || mutationActive.current || action.trim() === "") return;
    mutationActive.current = true;
    setMutation("respond");
    setMutationError(null);
    try {
      await api.respondMayorInteraction(pending.request_id, action, pendingDraft || undefined, pending.metadata ?? undefined);
      setPendingDraft("");
      await requestSnapshotRefresh();
    } catch (cause) {
      setMutationError(errorMessage(cause));
    } finally {
      mutationActive.current = false;
      setMutation(null);
    }
  }, [api, pendingDraft, requestSnapshotRefresh, view?.pending]);

  const canSend = useMemo(() => canSendMayor(view, composerDraft) && mutation === null, [composerDraft, mutation, view]);
  const error = statusError ?? snapshotError;

  return {
    view, turns, loading, stale: streamStale || snapshotStale || statusStale || Boolean(view?.stale), error, failureState, liveTurn,
    composerDraft, setComposerDraft, pendingDraft, setPendingDraft, receipt, mutation, mutationError,
    scrollOffset, setScrollOffset, refresh, refreshStatus, refreshSnapshots, loadOlder, send, respond, canSend,
    hasOlder: Boolean(pagination?.has_older), loadingOlder,
  };
}

function normalizeView(view: MayorView): MayorView {
  return { ...view, problems: view.problems ?? [] };
}

function normalizeTranscript(transcript: TranscriptPage): TranscriptPage {
  return { ...transcript, turns: transcript.turns ?? [], problems: transcript.problems ?? [] };
}

function preserveLiveView(next: MayorView, current: MayorView | null, snapshotIsCurrent: boolean): MayorView {
  if (snapshotIsCurrent || !current || next.session_id !== current.session_id) return next;
  return {
    ...next,
    activity: current.activity,
    state: current.state,
    pending: current.pending,
  };
}

function isMayorEvent(value: unknown): value is MayorEvent {
  if (value === null || typeof value !== "object" || !("kind" in value) || !("resources" in value)) return false;
  const event = value as { kind?: unknown; resources?: unknown; session_id?: unknown };
  const kind = String(event.kind);
  if (!["turn", "activity", "pending", "invalidate", "stale"].includes(kind)) return false;
  if (!(event.resources === null || (Array.isArray(event.resources) && event.resources.every((resource) => typeof resource === "string")))) return false;
  if (kind === "turn" || kind === "activity" || kind === "pending") {
    return typeof event.session_id === "string" && event.session_id.trim() !== "";
  }
  return event.session_id === undefined || typeof event.session_id === "string";
}

function reconcileLiveTurns(appended: TranscriptTurn[], buffered: LiveTurnRecord[]): LiveTurnRecord[] {
  const confirmations = new Map<string, number>();
  appended.forEach((turn) => {
    const key = turnKey(turn);
    confirmations.set(key, (confirmations.get(key) ?? 0) + 1);
  });
  return buffered.filter(({ turn }) => {
    const key = turnKey(turn);
    const count = confirmations.get(key) ?? 0;
    if (count === 0) return true;
    confirmations.set(key, count - 1);
    return false;
  });
}

function mergeAuthoritativeTail(
  history: TranscriptTurn[],
  tail: TranscriptTurn[],
): { turns: TranscriptTurn[]; appended: TranscriptTurn[] } {
  let overlap = Math.min(history.length, tail.length);
  while (overlap > 0) {
    let matches = true;
    for (let index = 0; index < overlap; index += 1) {
      if (turnKey(history[history.length - overlap + index]) !== turnKey(tail[index])) {
        matches = false;
        break;
      }
    }
    if (matches) break;
    overlap -= 1;
  }
  const appended = tail.slice(overlap);
  return { appended, turns: [...history, ...appended] };
}

function mergeTranscriptPages(
  older: TranscriptTurn[],
  current: TranscriptTurn[],
): { turns: TranscriptTurn[]; added: TranscriptTurn[] } {
  let overlap = Math.min(older.length, current.length);
  while (overlap > 0) {
    let matches = true;
    for (let index = 0; index < overlap; index += 1) {
      if (turnKey(older[older.length - overlap + index]) !== turnKey(current[index])) {
        matches = false;
        break;
      }
    }
    if (matches) break;
    overlap -= 1;
  }
  const added = older.slice(0, older.length - overlap);
  return { added, turns: [...added, ...current] };
}

function turnKey(turn: TranscriptTurn): string {
  return JSON.stringify([turn.role, turn.timestamp ?? null, turn.text]);
}

function classifyFailure(error: unknown): MayorFailureState {
  if (error instanceof MayorRequestError) {
    if (error.code === "mayor_identity_ambiguous") return "ambiguous";
    if (error.code === "mayor_not_configured") return "missing";
    return "disconnected";
  }
  const detail = errorMessage(error);
  const normalized = detail.toLowerCase();
  if (normalized.includes("ambiguous") || normalized.includes("more than once")) return "ambiguous";
  if (normalized.includes("not configured") || normalized.includes("not present")) return "missing";
  return "disconnected";
}

function canSendMayor(view: MayorView | null, draft: string): boolean {
  if (!view || draft.trim() === "") return false;
  if (["missing", "ambiguous", "disconnected", "unsupported"].includes(view.state)) return false;
  if (view.state === "in_turn" && !view.follow_up_supported) return false;
  return true;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Mayor request failed";
}

export type { MayorState };
