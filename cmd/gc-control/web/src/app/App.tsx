import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import { ConvoysWorkspace } from "@/features/convoys/ConvoysWorkspace";
import type { ConvoysWorkspaceCache, ConvoysWorkspaceHandle } from "@/features/convoys/ConvoysWorkspace";
import { MayorWorkspace } from "@/features/mayor/MayorWorkspace";
import { useMayor } from "@/features/mayor/useMayor";
import type { MayorRefreshRequester, MayorStreamConnector } from "@/features/mayor/useMayor";
import { OrdersWorkspace } from "@/features/orders/OrdersWorkspace";
import type { OrdersWorkspaceCache, OrdersWorkspaceHandle } from "@/features/orders/OrdersWorkspace";
import type { ControlCenterAPI, ConvoysAPI, Health, MayorAPI, OrdersAPI } from "@/lib/api";
import { createInvalidationFeed, createRefreshQueue } from "@/lib/events";
import type { EventSourceFactory, LiveResource, RefreshQueue } from "@/lib/events";
import {
  AlertIcon,
  CheckIcon,
  DetailHeader,
  EmptyState,
  Panel,
  Spinner,
  StatusSignal,
  Tabs,
  Text,
  ToolFrame,
} from "@/ui";
import type { TabItem } from "@/ui";

type AppState =
  | { kind: "loading" }
  | { kind: "ready"; health: Health }
  | { kind: "error" };

type TopLevelTab = LiveResource | "mayor";

export interface AppProps {
  api: ControlCenterAPI;
  eventSourceFactory?: EventSourceFactory;
  mayorConnector?: MayorStreamConnector;
  pollInterval?: number;
}

function convoyFacet(api: ControlCenterAPI): ConvoysAPI | null {
  if (!api.listConvoys || !api.getConvoy || !api.listBeads) return null;
  return { listConvoys: api.listConvoys, getConvoy: api.getConvoy, listBeads: api.listBeads };
}

function orderFacet(api: ControlCenterAPI): OrdersAPI | null {
  if (!api.listOrders || !api.listOrderHistory || !api.getOrderRunOutput) return null;
  return { listOrders: api.listOrders, listOrderHistory: api.listOrderHistory, getOrderRunOutput: api.getOrderRunOutput };
}

function mayorFacet(api: ControlCenterAPI): MayorAPI | null {
  if (!api.getMayor || !api.getMayorTranscript || !api.sendMayorMessage || !api.respondMayorInteraction) return null;
  return {
    getMayor: api.getMayor,
    getMayorTranscript: api.getMayorTranscript,
    sendMayorMessage: api.sendMayorMessage,
    respondMayorInteraction: api.respondMayorInteraction,
  };
}

export function App({ api, eventSourceFactory, mayorConnector, pollInterval = 10_000 }: AppProps) {
  const [state, setState] = useState<AppState>({ kind: "loading" });
  const [activeTab, setActiveTab] = useState<TopLevelTab>("convoys");
  const [selectedConvoyID, setSelectedConvoyID] = useState<string | null>(null);
  const [selectedOrderName, setSelectedOrderName] = useState<string | null>(null);
  const [stale, setStale] = useState<Record<LiveResource, boolean>>({ convoys: false, orders: false });
  const convoyRef = useRef<ConvoysWorkspaceHandle>(null);
  const orderRef = useRef<OrdersWorkspaceHandle>(null);
  const refreshDispatcher = useRef<(resources: string[], signal: AbortSignal) => Promise<void>>(async () => undefined);
  const refreshQueueRef = useRef<RefreshQueue | null>(null);
  const buildRefreshQueue = useCallback(
    () => createRefreshQueue((resources, signal) => refreshDispatcher.current(resources, signal)),
    [],
  );
  useEffect(() => {
    if (refreshQueueRef.current === null) refreshQueueRef.current = buildRefreshQueue();
    return () => {
      const queue = refreshQueueRef.current;
      refreshQueueRef.current = null;
      queue?.dispose();
    };
  }, [buildRefreshQueue]);
  const [convoyCache, setConvoyCache] = useState<ConvoysWorkspaceCache>({ list: null, detail: null, items: [] });
  const [orderCache, setOrderCache] = useState<OrdersWorkspaceCache>({ list: null, history: null, items: [] });
  const activeTabRef = useRef(activeTab);
  useEffect(() => {
    activeTabRef.current = activeTab;
  }, [activeTab]);
  const convoys = useMemo(() => convoyFacet(api), [api]);
  const orders = useMemo(() => orderFacet(api), [api]);
  const mayor = useMemo(() => mayorFacet(api), [api]);
  const requestMayorRefresh = useCallback<MayorRefreshRequester>(async (resources) => {
    const queue = refreshQueueRef.current;
    if (!queue) return;
    queue.request(resources);
    await queue.idle();
  }, []);
  const requestRefresh = useCallback((resources: string[]) => {
    refreshQueueRef.current?.request(resources);
  }, []);
  const mayorController = useMayor({ api: mayor, connector: mayorConnector, requestRefresh: requestMayorRefresh });
  const refreshMayorSnapshots = mayorController.refreshSnapshots;
  const refreshMayorStatus = mayorController.refreshStatus;
  const setConvoysConfirmed = useCallback((confirmed: boolean) => {
    setStale((current) => ({ ...current, convoys: !confirmed }));
  }, []);
  const setOrdersConfirmed = useCallback((confirmed: boolean) => {
    setStale((current) => ({ ...current, orders: !confirmed }));
  }, []);
  const updateConvoyList = useCallback((list: ConvoysWorkspaceCache["list"], items: ConvoysWorkspaceCache["items"]) => {
    setConvoyCache((current) => ({ ...current, list, items }));
  }, []);
  const updateConvoyDetail = useCallback((detail: ConvoysWorkspaceCache["detail"]) => {
    setConvoyCache((current) => ({ ...current, detail }));
  }, []);
  const updateOrderList = useCallback((list: OrdersWorkspaceCache["list"], items: OrdersWorkspaceCache["items"]) => {
    setOrderCache((current) => ({ ...current, list, items }));
  }, []);
  const updateOrderHistory = useCallback((history: OrdersWorkspaceCache["history"]) => {
    setOrderCache((current) => ({ ...current, history }));
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    api
      .health(controller.signal)
      .then((health) => {
        if (!controller.signal.aborted) setState({ kind: "ready", health });
      })
      .catch(() => {
        if (!controller.signal.aborted) setState({ kind: "error" });
      });
    return () => controller.abort();
  }, [api]);

  const refreshResources = useCallback(async (resources: string[], signal: AbortSignal) => {
    const requested = new Set(resources);
    const cityResources = resources.filter((resource) => resource === "convoys" || resource === "orders");
    const results = await Promise.all(cityResources.map(async (resource) => {
      if (resource === "convoys") {
        return { resource, confirmed: convoyRef.current ? await convoyRef.current.refresh(signal) : false };
      }
      if (resource === "orders") {
        return { resource, confirmed: orderRef.current ? await orderRef.current.refresh(signal) : false };
      }
      return { resource, confirmed: true };
    }));
    if (requested.has("mayor-snapshots")) {
      await refreshMayorSnapshots(signal);
    } else if (requested.has("mayor-status")) {
      await refreshMayorStatus(signal);
    }
    if (signal.aborted) return;
    setStale((current) => {
      const next = { ...current };
      results.forEach(({ resource, confirmed }) => {
        if (resource === "convoys" || resource === "orders") next[resource] = !confirmed;
      });
      return next;
    });
  }, [refreshMayorSnapshots, refreshMayorStatus]);

  useLayoutEffect(() => {
    refreshDispatcher.current = refreshResources;
  }, [refreshResources]);

  useEffect(() => {
    if (state.kind !== "ready" || (!convoys && !orders && !mayor)) return;
    const stopFeed = (convoys || orders) && (eventSourceFactory || typeof EventSource !== "undefined")
      ? createInvalidationFeed({
          factory: eventSourceFactory,
          onInvalidate: (resources) => {
            setStale((current) => {
              const next = { ...current };
              resources.forEach((resource) => { next[resource] = true; });
              return next;
            });
            requestRefresh(resources);
          },
          onReconnect: () => {
            const active = activeTabRef.current;
            if (active === "convoys" || active === "orders") requestRefresh([active]);
          },
          onStale: () => setStale({ convoys: true, orders: true }),
        })
      : () => undefined;
    const refreshVisible = () => {
      const active = activeTabRef.current;
      if (document.visibilityState !== "visible") return;
      if (active === "convoys" || active === "orders") requestRefresh([active]);
      if (active === "mayor") requestRefresh(["mayor-status"]);
    };
    const interval = window.setInterval(refreshVisible, pollInterval);
    window.addEventListener("focus", refreshVisible);
    const onVisibility = () => {
      if (document.visibilityState === "visible") refreshVisible();
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.clearInterval(interval);
      window.removeEventListener("focus", refreshVisible);
      document.removeEventListener("visibilitychange", onVisibility);
      stopFeed();
    };
  }, [convoys, eventSourceFactory, mayor, orders, pollInterval, requestRefresh, state.kind]);

  if (state.kind === "loading") {
    return (
      <main className="app-state">
        <Spinner label="Loading Control Center" />
        <Text>Loading Control Center…</Text>
      </main>
    );
  }
  if (state.kind === "error") {
    return (
      <main className="app-state">
        <Panel className="app-state__panel" role="alert" title="Unable to connect to Control Center">
          <Text>The local server did not return its typed health response.</Text>
        </Panel>
      </main>
    );
  }

  const connected = state.health.supervisor_reachable;
  const noLiveFacets = !convoys && !orders && !mayor;
  const tabItems: TabItem[] = [
    {
      id: "convoys",
      label: "Convoys",
      content: convoys ? (
        <ConvoysWorkspace
          ref={convoyRef}
          api={convoys}
          cache={convoyCache}
          onListChange={updateConvoyList}
          onDetailChange={updateConvoyDetail}
          selectedID={selectedConvoyID}
          onSelectedIDChange={setSelectedConvoyID}
          onConfirmedChange={setConvoysConfirmed}
          externallyStale={stale.convoys}
        />
      ) : <Panel title="Convoys"><EmptyState title="Convoy projections unavailable" /></Panel>,
    },
    {
      id: "orders",
      label: "Orders",
      content: orders ? (
        <OrdersWorkspace
          ref={orderRef}
          api={orders}
          cache={orderCache}
          onListChange={updateOrderList}
          onHistoryChange={updateOrderHistory}
          selectedName={selectedOrderName}
          onSelectedNameChange={setSelectedOrderName}
          onConfirmedChange={setOrdersConfirmed}
          externallyStale={stale.orders}
        />
      ) : <Panel title="Orders"><EmptyState title="Order projections unavailable" /></Panel>,
    },
    ...(mayor ? [{ id: "mayor", label: "Mayor", content: <MayorWorkspace controller={mayorController} /> }] : []),
  ];
  return (
    <ToolFrame
      header={
        <DetailHeader
          title={state.health.city}
          subtitle="GasCity Control Center"
          status={
            <StatusSignal
              label={connected ? "Supervisor connected" : "Supervisor unavailable"}
              tone={connected ? "success" : "warning"}
              icon={connected ? CheckIcon : AlertIcon}
            />
          }
        />
      }
    >
      {noLiveFacets ? (
        <Panel className="app-workspace" title="Control Center workspace">
          <EmptyState title="Operator workspace is ready" description="Live projection methods are not connected." />
        </Panel>
      ) : (
        <Tabs
          className="app-workspace"
          aria-label="Control Center resources"
          value={activeTab}
          onValueChange={(value) => {
            if (value === "convoys" || value === "orders" || value === "mayor") setActiveTab(value);
          }}
          items={tabItems}
        />
      )}
    </ToolFrame>
  );
}
