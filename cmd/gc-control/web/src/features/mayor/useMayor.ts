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
  let reconnecting = false;
  let attempt = 0;
  let disconnectCurrent: (() => void) | null = null;

  const connect = () => {
    if (stopped) return;
    const current = new EventSource("/api/v1/mayor/events") as MayorEventSourceLike;
    source = current;
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
      if (stopped || source !== current) return;
      attempt = 0;
      if (!reconnecting) return;
      reconnecting = false;
      void callbacks.onReconnect().catch(() => callbacks.onStale());
    };
    current.onerror = () => {
      if (stopped || source !== current) return;
      callbacks.onStale();
      reconnecting = true;
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

export function useMayor({ api, connector = connectMayorEvents, requestRefresh }: UseMayorOptions): MayorController {
  const [view, setView] = useState<MayorView | null>(null);
  const [transcript, setTranscript] = useState<TranscriptPage | null>(null);
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
  const activeControllers = useRef(new Set<AbortController>());
  const lifecycleGeneration = useRef(0);
  const hasLastGood = useRef(false);
  const liveViewRevision = useRef(0);
  const viewRequestSerial = useRef(0);
  const transcriptRequestSerial = useRef(0);
  const streamRevision = useRef(0);
  const snapshotDemandRevision = useRef(0);
  const lastSnapshotConfirmedRevision = useRef(-1);
  const unconfirmedLiveTurns = useRef<LiveTurnRecord[]>([]);
  const loadedOlderTurns = useRef<TranscriptTurn[]>([]);
  const authoritativeTurnCounts = useRef<Map<string, number> | null>(null);
  const pendingRequestID = useRef<string | null>(null);
  const mutationActive = useRef(false);

  const refreshSnapshots = useCallback(async (signal?: AbortSignal): Promise<boolean> => {
    if (!api) {
      setLoading(false);
      return false;
    }
    const ownedController = signal ? null : new AbortController();
    const requestSignal = signal ?? ownedController!.signal;
    const generation = lifecycleGeneration.current;
    const demandRevision = snapshotDemandRevision.current;
    const startedAtRevision = liveViewRevision.current;
    const requestSerial = ++viewRequestSerial.current;
    const transcriptSerial = ++transcriptRequestSerial.current;
    if (ownedController) activeControllers.current.add(ownedController);
    try {
      const settleView = async (): Promise<boolean> => {
        try {
          const normalizedView = normalizeView(await api.getMayor(requestSignal));
          if (requestSignal.aborted || generation !== lifecycleGeneration.current || requestSerial !== viewRequestSerial.current) return false;
          setView((current) => preserveLiveView(normalizedView, current, startedAtRevision === liveViewRevision.current));
          hasLastGood.current = true;
          setStatusError(null);
          setFailureState(null);
          setStatusStale(false);
          setLoading(false);
          return true;
        } catch (cause) {
          if (requestSignal.aborted || generation !== lifecycleGeneration.current || requestSerial !== viewRequestSerial.current) return false;
          setStatusError(errorMessage(cause));
          setStatusStale(true);
          if (!hasLastGood.current) setFailureState(classifyFailure(cause));
          setLoading(false);
          return false;
        }
      };

      const settleTranscript = async (): Promise<boolean> => {
        try {
          const normalizedTranscript = normalizeTranscript(await api.getMayorTranscript(undefined, requestSignal));
          if (requestSignal.aborted || generation !== lifecycleGeneration.current || transcriptSerial !== transcriptRequestSerial.current) return false;
          const reconciliation = reconcileLiveTurns(
            normalizedTranscript.turns ?? [],
            unconfirmedLiveTurns.current,
            authoritativeTurnCounts.current,
          );
          unconfirmedLiveTurns.current = reconciliation.remaining;
          authoritativeTurnCounts.current = reconciliation.snapshotCounts;
          setTranscript(normalizedTranscript);
          setTurns([...loadedOlderTurns.current, ...reconciliation.turns]);
          setSnapshotError(null);
          return true;
        } catch (cause) {
          if (requestSignal.aborted || generation !== lifecycleGeneration.current || transcriptSerial !== transcriptRequestSerial.current) return false;
          setSnapshotError(errorMessage(cause));
          return false;
        }
      };

      const viewLane = settleView();
      const transcriptLane = settleTranscript();
      const [viewConfirmed, transcriptConfirmed] = await Promise.all([viewLane, transcriptLane]);
      if (requestSignal.aborted || generation !== lifecycleGeneration.current) return false;
      if (requestSerial !== viewRequestSerial.current || transcriptSerial !== transcriptRequestSerial.current) return false;

      const confirmed = viewConfirmed && transcriptConfirmed;
      if (confirmed) {
        lastSnapshotConfirmedRevision.current = Math.max(lastSnapshotConfirmedRevision.current, demandRevision);
        if (demandRevision === snapshotDemandRevision.current) setSnapshotStale(false);
      } else {
        setSnapshotStale(true);
      }
      return confirmed;
    } finally {
      if (ownedController) activeControllers.current.delete(ownedController);
    }
  }, [api]);

  const refreshStatus = useCallback(async (signal?: AbortSignal): Promise<boolean> => {
    if (!api) {
      setLoading(false);
      return false;
    }
    const ownedController = signal ? null : new AbortController();
    const requestSignal = signal ?? ownedController!.signal;
    const generation = lifecycleGeneration.current;
    const startedAtRevision = liveViewRevision.current;
    const requestSerial = ++viewRequestSerial.current;
    if (ownedController) activeControllers.current.add(ownedController);
    try {
      const nextView = normalizeView(await api.getMayor(requestSignal));
      if (requestSignal.aborted || generation !== lifecycleGeneration.current) return false;
      if (requestSerial === viewRequestSerial.current) {
        setView((current) => preserveLiveView(nextView, current, startedAtRevision === liveViewRevision.current));
        hasLastGood.current = true;
        setStatusError(null);
        setStatusStale(false);
        setFailureState(null);
        setLoading(false);
      }
      return true;
    } catch (cause) {
      if (requestSignal.aborted || generation !== lifecycleGeneration.current) return false;
      if (requestSerial === viewRequestSerial.current) {
        setStatusError(errorMessage(cause));
        setStatusStale(true);
        if (!hasLastGood.current) setFailureState(classifyFailure(cause));
        setLoading(false);
      }
      return false;
    } finally {
      if (ownedController) activeControllers.current.delete(ownedController);
    }
  }, [api]);

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
    loadedOlderTurns.current = [];
    unconfirmedLiveTurns.current = [];
    authoritativeTurnCounts.current = null;
    queueMicrotask(() => {
      if (active && generation === lifecycleGeneration.current) void refreshSnapshots();
    });
    return () => {
      active = false;
      lifecycleGeneration.current += 1;
      controllers.forEach((controller) => controller.abort());
      controllers.clear();
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

  const onEvent = useCallback((event: MayorEvent) => {
    if (event.kind === "stale") {
      markStreamStale();
      return;
    }
    if (event.kind === "turn" && event.turn) {
      unconfirmedLiveTurns.current.push({ turn: event.turn });
      setTurns((current) => [...current, event.turn!]);
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
    if (event.kind === "invalidate") {
      setSnapshotStale(true);
      const startedAtRevision = streamRevision.current;
      void requestSnapshotRefresh().then((confirmed) => {
        if (confirmed && startedAtRevision === streamRevision.current) setStreamStale(false);
      });
    }
  }, [markStreamStale, requestSnapshotRefresh]);

  useEffect(() => {
    if (!api) return;
    return connector({
      onEvent,
      onStale: markStreamStale,
      onReconnect: async () => {
        const startedAtRevision = streamRevision.current;
        const confirmed = await requestSnapshotRefresh();
        if (confirmed && startedAtRevision === streamRevision.current) setStreamStale(false);
      },
    });
  }, [api, connector, markStreamStale, onEvent, requestSnapshotRefresh]);

  const loadOlder = useCallback(async () => {
    if (!api || loadingOlder || !transcript?.has_older || !transcript.before) return;
    setLoadingOlder(true);
    const generation = lifecycleGeneration.current;
    try {
      const older = normalizeTranscript(await api.getMayorTranscript(transcript.before));
      if (generation !== lifecycleGeneration.current) return;
      const olderTurns = older.turns ?? [];
      loadedOlderTurns.current = [...olderTurns, ...loadedOlderTurns.current];
      setTurns((current) => [...olderTurns, ...current]);
      setTranscript((current) => current ? { ...current, has_older: older.has_older, before: older.before, total: Math.max(current.total, older.total), problems: [...(older.problems ?? []), ...(current.problems ?? [])] } : older);
    } catch (cause) {
      if (generation !== lifecycleGeneration.current) return;
      setMutationError(errorMessage(cause));
    } finally {
      if (generation === lifecycleGeneration.current) setLoadingOlder(false);
    }
  }, [api, loadingOlder, transcript]);

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
    hasOlder: Boolean(transcript?.has_older), loadingOlder,
  };
}

function normalizeView(view: MayorView): MayorView {
  return { ...view, problems: view.problems ?? [] };
}

function normalizeTranscript(transcript: TranscriptPage): TranscriptPage {
  return { ...transcript, turns: transcript.turns ?? [], problems: transcript.problems ?? [] };
}

function preserveLiveView(next: MayorView, current: MayorView | null, snapshotIsCurrent: boolean): MayorView {
  if (snapshotIsCurrent || !current) return next;
  return {
    ...next,
    activity: current.activity,
    state: current.state,
    pending: current.pending,
  };
}

function isMayorEvent(value: unknown): value is MayorEvent {
  if (value === null || typeof value !== "object" || !("kind" in value) || !("resources" in value)) return false;
  const event = value as { kind?: unknown; resources?: unknown };
  return ["turn", "activity", "pending", "invalidate", "stale"].includes(String(event.kind))
    && (event.resources === null || (Array.isArray(event.resources) && event.resources.every((resource) => typeof resource === "string")));
}

function reconcileLiveTurns(
  snapshot: TranscriptTurn[],
  buffered: LiveTurnRecord[],
  previousSnapshotCounts: Map<string, number> | null,
): { turns: TranscriptTurn[]; remaining: LiveTurnRecord[]; snapshotCounts: Map<string, number> } {
  const snapshotCounts = new Map<string, number>();
  snapshot.forEach((turn) => {
    const key = turnKey(turn);
    snapshotCounts.set(key, (snapshotCounts.get(key) ?? 0) + 1);
  });
  const confirmations = new Map<string, number>();
  if (previousSnapshotCounts !== null) {
    snapshotCounts.forEach((count, key) => {
      const growth = count - (previousSnapshotCounts.get(key) ?? 0);
      if (growth > 0) confirmations.set(key, growth);
    });
  }
  const remaining = buffered.filter(({ turn }) => {
    const key = turnKey(turn);
    const count = confirmations.get(key) ?? 0;
    if (count === 0) return true;
    confirmations.set(key, count - 1);
    return false;
  });
  return { turns: [...snapshot, ...remaining.map(({ turn }) => turn)], remaining, snapshotCounts };
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
