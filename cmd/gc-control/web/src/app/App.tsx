import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { ConvoysWorkspace } from "@/features/convoys/ConvoysWorkspace";
import type { ConvoysWorkspaceCache, ConvoysWorkspaceHandle } from "@/features/convoys/ConvoysWorkspace";
import { OrdersWorkspace } from "@/features/orders/OrdersWorkspace";
import type { OrdersWorkspaceCache, OrdersWorkspaceHandle } from "@/features/orders/OrdersWorkspace";
import { MailView } from "@/features/mail/MailView";
import { useMail } from "@/features/mail/useMail";
import type { ControlCenterAPI, ConvoysAPI, Health, MailAPI, OrdersAPI } from "@/lib/api";
import { createInvalidationFeed, createRefreshQueue } from "@/lib/events";
import type { EventSourceFactory, LiveResource } from "@/lib/events";
import {
  AlertIcon,
  Badge,
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

type AppState =
  | { kind: "loading" }
  | { kind: "ready"; health: Health }
  | { kind: "error" };

export interface AppProps {
  api: ControlCenterAPI;
  eventSourceFactory?: EventSourceFactory;
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

function supervisorMailFacet(api: ControlCenterAPI): MailAPI | null {
  if (!api.getMailCount || !api.listMail || !api.getMail || !api.getMailThread) return null;
  return {
    getMailCount: api.getMailCount,
    listMail: api.listMail,
    getMail: api.getMail,
    getMailThread: api.getMailThread,
  };
}

export function App({ api, eventSourceFactory, pollInterval = 10_000 }: AppProps) {
  const [state, setState] = useState<AppState>({ kind: "loading" });
  const [activeTab, setActiveTab] = useState<LiveResource>("convoys");
  const [selectedConvoyID, setSelectedConvoyID] = useState<string | null>(null);
  const [selectedOrderName, setSelectedOrderName] = useState<string | null>(null);
  const [stale, setStale] = useState<Record<"convoys" | "orders", boolean>>({ convoys: false, orders: false });
  const convoyRef = useRef<ConvoysWorkspaceHandle>(null);
  const orderRef = useRef<OrdersWorkspaceHandle>(null);
  const [convoyCache, setConvoyCache] = useState<ConvoysWorkspaceCache>({ list: null, detail: null, items: [] });
  const [orderCache, setOrderCache] = useState<OrdersWorkspaceCache>({ list: null, history: null, items: [] });
  const activeTabRef = useRef(activeTab);
  useEffect(() => {
    activeTabRef.current = activeTab;
  }, [activeTab]);
  const convoys = useMemo(() => convoyFacet(api), [api]);
  const orders = useMemo(() => orderFacet(api), [api]);
  const mail = useMemo(() => supervisorMailFacet(api), [api]);
  const mailModel = useMail({ api: mail, active: activeTab === "mail", pollInterval });
  const invalidateMail = mailModel.onInvalidation;
  const disconnectMail = mailModel.onDisconnect;
  const reconnectMail = mailModel.onReconnect;
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
    const results = await Promise.all(resources.map(async (resource) => {
      if (resource === "convoys") {
        return { resource, confirmed: convoyRef.current ? await convoyRef.current.refresh(signal) : false };
      }
      if (resource === "orders") {
        return { resource, confirmed: orderRef.current ? await orderRef.current.refresh(signal) : false };
      }
      return { resource, confirmed: true };
    }));
    if (signal.aborted) return;
    setStale((current) => {
      const next = { ...current };
      results.forEach(({ resource, confirmed }) => {
        if (resource === "convoys" || resource === "orders") next[resource] = !confirmed;
      });
      return next;
    });
  }, []);

  useEffect(() => {
    if (state.kind !== "ready" || (!convoys && !orders && !mail)) return;
    const queue = createRefreshQueue(refreshResources);
    const stopFeed = eventSourceFactory || typeof EventSource !== "undefined"
      ? createInvalidationFeed({
          factory: eventSourceFactory,
          onInvalidate: (resources) => {
            if (resources.includes("mail")) invalidateMail();
            const workspaceResources = resources.filter((resource) => resource !== "mail");
            setStale((current) => {
              const next = { ...current };
              workspaceResources.forEach((resource) => { next[resource] = true; });
              return next;
            });
            queue.request(workspaceResources);
          },
          onReconnect: () => {
            reconnectMail();
            if (activeTabRef.current !== "mail") queue.request([activeTabRef.current]);
          },
          onStale: () => {
            setStale({ convoys: true, orders: true });
            disconnectMail();
          },
        })
      : () => undefined;
    const refreshVisible = () => {
      if (document.visibilityState === "visible" && activeTabRef.current !== "mail") queue.request([activeTabRef.current]);
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
      queue.dispose();
    };
  }, [convoys, disconnectMail, eventSourceFactory, invalidateMail, mail, orders, pollInterval, reconnectMail, refreshResources, state.kind]);

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
  const noLiveFacets = !convoys && !orders && !mail;
  const unreadBadge = (() => {
    if (!mail) return null;
    if (mailModel.errors.count && !mailModel.count) return { label: "Unread unavailable", tone: "danger" as const };
    if (!mailModel.count) return { label: "Unread loading", tone: "neutral" as const };
    if (mailModel.count.partial) {
      return mailModel.count.unread === 0
        ? { label: "Unread partial", tone: "warning" as const }
        : { label: `${mailModel.count.unread} unread · partial`, tone: "warning" as const };
    }
    if (mailModel.stale || mailModel.disconnected || mailModel.errors.count) {
      return mailModel.count.unread === 0
        ? { label: "Unread stale", tone: "warning" as const }
        : { label: `${mailModel.count.unread} unread · stale`, tone: "warning" as const };
    }
    return { label: `${mailModel.count.unread} unread`, tone: mailModel.count.unread > 0 ? "info" as const : "neutral" as const };
  })();
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
        <div className="app-workspace">
          {unreadBadge ? <div className="app-resource-summary"><Badge role="status" tone={unreadBadge.tone}>{unreadBadge.label}</Badge></div> : null}
          <Tabs
            aria-label="Control Center resources"
            value={activeTab}
            onValueChange={(value) => {
              if (value === "convoys" || value === "orders" || (value === "mail" && mail)) setActiveTab(value);
            }}
            items={[
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
            ...(mail ? [{
              id: "mail",
              label: "Mail",
              content: <MailView model={mailModel} />,
            }] : []),
          ]}
          />
        </div>
      )}
    </ToolFrame>
  );
}
