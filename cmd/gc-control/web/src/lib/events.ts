import type { components } from "@/generated/schema";

export type LiveResource = "convoys" | "orders" | "mail";
type Invalidation = components["schemas"]["Invalidation"];

export interface EventSourceLike {
  onopen?: ((event: Event) => void) | null;
  onerror: ((event: Event) => void) | null;
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void): void;
  removeEventListener(type: string, listener: (event: MessageEvent<string>) => void): void;
  close(): void;
}

export type EventSourceFactory = (url: string) => EventSourceLike;

export interface InvalidationFeedOptions {
  factory?: EventSourceFactory;
  onInvalidate(resources: LiveResource[]): void;
  onReconnect?(): void;
  onStale(): void;
}

const reconnectDelays = [1_000, 2_000, 4_000, 8_000, 10_000] as const;

function defaultEventSourceFactory(url: string): EventSourceLike {
  return new EventSource(url) as EventSourceLike;
}

function isInvalidation(value: unknown): value is Invalidation {
  if (value === null || typeof value !== "object") return false;
  if (!("cursor" in value) || typeof value.cursor !== "string") return false;
  if (!("resources" in value)) return false;
  return value.resources === null
    || (Array.isArray(value.resources) && value.resources.every((resource) => typeof resource === "string"));
}

function decodeResources(data: string): LiveResource[] | undefined {
  try {
    const decoded: unknown = JSON.parse(data);
    if (!isInvalidation(decoded) || !decoded.cursor.trim() || decoded.resources === null) {
      return undefined;
    }
    const resources = new Set<LiveResource>();
    for (const resource of decoded.resources) {
      if (resource === "convoys" || resource === "orders" || resource === "mail") resources.add(resource);
    }
    if (resources.size === 0) return undefined;
    return [...resources].sort();
  } catch {
    return undefined;
  }
}

export function createInvalidationFeed(options: InvalidationFeedOptions): () => void {
  const factory = options.factory ?? defaultEventSourceFactory;
  let source: EventSourceLike | undefined;
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  let backoffIndex = 0;
  let disposed = false;
  let reconnecting = false;
  let detach: (() => void) | undefined;

  const connect = () => {
    if (disposed) return;
    const next = factory("/api/v1/events");
    source = next;
    next.onopen = () => {
      if (disposed || source !== next || !reconnecting) return;
      reconnecting = false;
      options.onReconnect?.();
    };
    const onMessage = (event: MessageEvent<string>) => {
      if (disposed || source !== next) return;
      const resources = decodeResources(event.data);
      if (!resources) return;
      backoffIndex = 0;
      options.onInvalidate(resources);
    };
    next.addEventListener("invalidate", onMessage);
    next.onerror = () => {
      if (disposed || source !== next) return;
      reconnecting = true;
      options.onStale();
      detach?.();
      const delay = reconnectDelays[Math.min(backoffIndex, reconnectDelays.length - 1)];
      if (backoffIndex < reconnectDelays.length - 1) backoffIndex += 1;
      reconnectTimer = setTimeout(connect, delay);
    };
    detach = () => {
      next.removeEventListener("invalidate", onMessage);
      next.onopen = null;
      next.onerror = null;
      next.close();
      if (source === next) source = undefined;
    };
  };

  connect();
  return () => {
    disposed = true;
    if (reconnectTimer !== undefined) clearTimeout(reconnectTimer);
    detach?.();
    detach = undefined;
  };
}

export interface RefreshQueue {
  request(resources: string[]): void;
  idle(): Promise<void>;
  lastError(): unknown;
  dispose(): void;
}

export function createRefreshQueue(refresh: (resources: string[], signal: AbortSignal) => Promise<void>): RefreshQueue {
  const pending = new Set<string>();
  let running: Promise<void> | undefined;
  let controller: AbortController | undefined;
  let recordedError: unknown;
  let disposed = false;

  const start = () => {
    if (running || disposed || pending.size === 0) return;
    running = (async () => {
      while (!disposed && pending.size > 0) {
        const resources = [...pending].sort();
        pending.clear();
        controller = new AbortController();
        try {
          await refresh(resources, controller.signal);
        } catch (error) {
          recordedError = error;
        } finally {
          controller = undefined;
        }
      }
    })().finally(() => {
      running = undefined;
      if (!disposed && pending.size > 0) start();
    });
  };

  return {
    request(resources) {
      if (disposed) return;
      resources.forEach((resource) => pending.add(resource));
      start();
    },
    async idle() {
      while (running) await running;
    },
    lastError() {
      return recordedError;
    },
    dispose() {
      disposed = true;
      pending.clear();
      controller?.abort();
    },
  };
}
