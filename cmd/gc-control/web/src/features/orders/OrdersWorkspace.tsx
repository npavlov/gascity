import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";

import type { Order, OrderList, OrderRunList, OrderRunOutput, OrdersAPI, Problem } from "@/lib/api";
import { Badge, Button, EmptyState, Panel, Spinner, Stack, Text } from "@/ui";

import "./OrdersWorkspace.css";

export interface OrdersWorkspaceHandle {
  refresh(signal?: AbortSignal): Promise<boolean>;
}

interface OrdersWorkspaceProps {
  api: OrdersAPI;
  cache: OrdersWorkspaceCache;
  onListChange(list: OrderList, items: Order[]): void;
  onHistoryChange(history: IdentifiedHistory | null): void;
  selectedName: string | null;
  onSelectedNameChange(name: string | null): void;
  onConfirmedChange(confirmed: boolean): void;
  externallyStale: boolean;
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

function nextSelection(previousName: string | null, previousItems: Order[], items: Order[]) {
  if (previousName === null) return items[0]?.scoped_name ?? null;
  if (items.some((item) => item.scoped_name === previousName)) return previousName;
  const previousIndex = previousItems.findIndex((item) => item.scoped_name === previousName);
  if (previousIndex < 0) return items[0]?.scoped_name ?? null;
  return items[previousIndex]?.scoped_name ?? items[previousIndex - 1]?.scoped_name ?? null;
}

function Problems({ problems }: { problems: Problem[] | null }) {
  if (!problems?.length) return null;
  return (
    <Stack className="live-problems" gap="1" role="alert">
      {problems.map((problem, index) => <Text variant="caption" key={`${problem.code}-${index}`}>{problem.detail}</Text>)}
    </Stack>
  );
}

function runKey(beadID: string, storeRef: string) {
  return `${storeRef}\u0000${beadID}`;
}

export interface IdentifiedHistory {
  scopedName: string;
  value: OrderRunList;
}

export interface OrdersWorkspaceCache {
  list: OrderList | null;
  history: IdentifiedHistory | null;
  items: Order[];
}

interface IdentifiedOutput {
  key: string;
  value: OrderRunOutput;
}

export const OrdersWorkspace = forwardRef<OrdersWorkspaceHandle, OrdersWorkspaceProps>(function OrdersWorkspace(
  { api, cache, onListChange, onHistoryChange, selectedName, onSelectedNameChange, onConfirmedChange, externallyStale },
  ref,
) {
  const [list, setList] = useState<OrderList | null>(() => cache.list);
  const [listError, setListError] = useState(false);
  const [history, setHistory] = useState<IdentifiedHistory | null>(() => cache.history);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState(false);
  const [output, setOutput] = useState<IdentifiedOutput | null>(null);
  const [outputLoading, setOutputLoading] = useState<string | null>(null);
  const [outputError, setOutputError] = useState(false);
  const listController = useRef<AbortController | null>(null);
  const historyController = useRef<AbortController | null>(null);
  const outputController = useRef<AbortController | null>(null);
  const itemsRef = useRef<Order[]>(cache.items);
  const historyRef = useRef<IdentifiedHistory | null>(cache.history);
  const selectedRef = useRef(selectedName);
  useEffect(() => {
    selectedRef.current = selectedName;
  }, [selectedName]);

  const loadHistory = useCallback(async (scopedName: string | null, parentSignal?: AbortSignal) => {
    historyController.current?.abort();
    outputController.current?.abort();
    outputController.current = null;
    setOutput(null);
    setOutputLoading(null);
    setOutputError(false);
    if (!scopedName) {
      historyRef.current = null;
      onHistoryChange(null);
      setHistory(null);
      setHistoryError(false);
      setHistoryLoading(false);
      return true;
    }
    if (historyRef.current?.scopedName !== scopedName) {
      historyRef.current = null;
      onHistoryChange(null);
      setHistory(null);
    }
    const controller = new AbortController();
    const abort = () => controller.abort();
    parentSignal?.addEventListener("abort", abort, { once: true });
    if (parentSignal?.aborted) controller.abort();
    historyController.current = controller;
    setHistoryLoading(true);
    setHistoryError(false);
    try {
      const next = await api.listOrderHistory(scopedName, undefined, 20, controller.signal);
      if (controller.signal.aborted || historyController.current !== controller) return false;
      const identified = { scopedName, value: next };
      historyRef.current = identified;
      onHistoryChange(identified);
      setHistory(identified);
      return !next.stale;
    } catch (error) {
      if (historyController.current === controller && !controller.signal.aborted && !isAbort(error)) setHistoryError(true);
      return false;
    } finally {
      parentSignal?.removeEventListener("abort", abort);
      if (historyController.current === controller) setHistoryLoading(false);
    }
  }, [api, onHistoryChange]);

  const refresh = useCallback(async (parentSignal?: AbortSignal) => {
    listController.current?.abort();
    historyController.current?.abort();
    outputController.current?.abort();
    outputController.current = null;
    setHistoryLoading(false);
    setOutput(null);
    setOutputLoading(null);
    setOutputError(false);
    const controller = new AbortController();
    const abort = () => controller.abort();
    parentSignal?.addEventListener("abort", abort, { once: true });
    if (parentSignal?.aborted) controller.abort();
    listController.current = controller;
    try {
      const next = await api.listOrders(controller.signal);
      if (controller.signal.aborted) return false;
      const items = next.items ?? [];
      const nextName = nextSelection(selectedRef.current, itemsRef.current, items);
      itemsRef.current = items;
      selectedRef.current = nextName;
      onListChange(next, items);
      setList(next);
      setListError(false);
      onSelectedNameChange(nextName);
      const historyConfirmed = await loadHistory(nextName, parentSignal);
      const confirmed = historyConfirmed && !next.stale;
      if (!controller.signal.aborted) onConfirmedChange(confirmed);
      return confirmed;
    } catch (error) {
      if (!controller.signal.aborted && !isAbort(error)) {
        setListError(true);
        onConfirmedChange(false);
      }
      return false;
    } finally {
      parentSignal?.removeEventListener("abort", abort);
    }
  }, [api, loadHistory, onConfirmedChange, onListChange, onSelectedNameChange]);

  useImperativeHandle(ref, () => ({ refresh }), [refresh]);
  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (active) void refresh();
    });
    return () => {
      active = false;
      listController.current?.abort();
      historyController.current?.abort();
      outputController.current?.abort();
    };
  }, [refresh]);

  const select = (name: string) => {
    selectedRef.current = name;
    onSelectedNameChange(name);
    void loadHistory(name).then((confirmed) => {
      onConfirmedChange(confirmed && !list?.stale);
    });
  };

  const loadOutput = async (beadID: string, storeRef: string) => {
    outputController.current?.abort();
    const controller = new AbortController();
    outputController.current = controller;
    const key = runKey(beadID, storeRef);
    setOutputLoading(key);
    setOutputError(false);
    try {
      const next = await api.getOrderRunOutput(beadID, storeRef, controller.signal);
      if (!controller.signal.aborted && outputController.current === controller) setOutput({ key, value: next });
    } catch (error) {
      if (outputController.current === controller && !controller.signal.aborted && !isAbort(error)) setOutputError(true);
    } finally {
      if (outputController.current === controller) setOutputLoading(null);
    }
  };

  const items = list?.items ?? [];
  const selected = items.find((item) => item.scoped_name === selectedName);
  const selectedHistory = history?.scopedName === selectedName ? history.value : null;
  const stale = externallyStale || list?.stale || Boolean(listError && list);

  return (
    <div className="live-workspace" data-resource="orders">
      <Panel className="live-workspace__rail" title="Orders">
        {!list && !listError ? <Spinner label="Loading orders" /> : null}
        {listError && !list ? <div role="alert"><EmptyState title="Unable to load orders" description="No last-confirmed order list is available." /></div> : null}
        {list && stale ? <Text className="live-notice" role="status">Showing last confirmed order data</Text> : null}
        {list?.degraded ? <Text className="live-notice" role="status">Order data is partial</Text> : null}
        <Problems problems={list?.problems ?? null} />
        {list && items.length === 0 ? <EmptyState title="No orders" description="No configured orders were returned." /> : null}
        <Stack className="live-rail-list" gap="2">
          {items.map((order) => (
            <Button
              className="live-rail-row"
              variant="quiet"
              key={order.scoped_name}
              aria-label={`Select order ${order.name}`}
              aria-pressed={order.scoped_name === selectedName}
              onClick={() => select(order.scoped_name)}
            >
              <span className="live-rail-row__identity"><Text variant="label">{order.name}</Text><Badge tone={order.enabled ? "success" : "neutral"}>{order.enabled ? "enabled" : "disabled"}</Badge></span>
              <Text variant="code">{order.scoped_name}</Text>
              <Text variant="caption">Last run: {order.last_run?.status ?? "not available"}</Text>
            </Button>
          ))}
        </Stack>
      </Panel>

      <Panel className="live-workspace__detail" title={selected?.name ?? "Order detail"}>
        {!selectedName ? <EmptyState title="Select an order" description="Choose an order to inspect its recent runs." /> : null}
        {selected ? (
          <Stack gap="4">
            <div className="order-definition">
              <Stack gap="1"><Text variant="code">{selected.scoped_name}</Text><Text>{selected.type}</Text></Stack>
              <Badge tone={selected.enabled ? "success" : "neutral"}>{selected.enabled ? "enabled" : "disabled"}</Badge>
            </div>
            {selected.last_run ? (
              <Stack gap="1" aria-label="Last known order run">
                <Text variant="label">Last known run</Text>
                <Text>{selected.last_run.status}</Text>
                <Text variant="caption">{selected.last_run.created_at || "Time unavailable"}</Text>
                {selected.last_run.store_ref ? <Text variant="caption">Store: {selected.last_run.store_ref}</Text> : null}
                <Problems problems={selected.last_run.problems} />
              </Stack>
            ) : <Text variant="caption">No last-run fact is available.</Text>}
            <Problems problems={selected.problems} />
            <Stack gap="2">
              <Text variant="label">Run history</Text>
              {historyLoading ? <Spinner label="Loading order history" /> : null}
              {historyError && !selectedHistory ? <div role="alert"><EmptyState title="Unable to load order history" /></div> : null}
              {historyError && selectedHistory ? <Text className="live-notice" role="status">Showing last confirmed order history</Text> : null}
              {selectedHistory?.stale ? <Text className="live-notice" role="status">Showing last confirmed order history</Text> : null}
              {selectedHistory?.degraded ? <Text className="live-notice" role="status">Order history is partial</Text> : null}
              <Problems problems={selectedHistory?.problems ?? null} />
              {selectedHistory && !selectedHistory.items?.length ? <EmptyState title="No order runs" /> : null}
              {selectedHistory?.items?.map((run) => (
                <div className="order-run" key={`${run.store_ref}:${run.bead_id}`}>
                  <Stack gap="1">
                    <Text variant="code">{run.bead_id}</Text>
                    <Text variant="caption">{run.created_at || "Time unavailable"}</Text>
                    <Text variant="caption">Store: {run.store_ref}</Text>
                    {run.duration_ms ? <Text variant="caption">Duration: {run.duration_ms} ms</Text> : null}
                    {run.exit_code ? <Text variant="caption">Exit code: {run.exit_code}</Text> : null}
                    <Problems problems={run.problems} />
                  </Stack>
                  <Badge tone={run.status === "failed" ? "danger" : run.status === "completed" ? "success" : run.status === "active" ? "info" : "warning"}>{run.status}</Badge>
                  {run.has_output ? (
                    <Button size="compact" onClick={() => void loadOutput(run.bead_id, run.store_ref)} aria-label={`View output for ${run.bead_id}`}>
                      {outputLoading === runKey(run.bead_id, run.store_ref) ? "Loading…" : "View output"}
                    </Button>
                  ) : null}
                </div>
              ))}
            </Stack>
            {outputError ? <Text role="alert">Unable to load run output.</Text> : null}
            {output && selectedHistory?.items?.some((run) => runKey(run.bead_id, run.store_ref) === output.key) ? (
              <Stack className="order-output" gap="2" aria-label={`Output for ${output.value.bead_id}`}>
                <Text variant="label">Output</Text>
                <Text variant="code">{output.value.output}</Text>
              </Stack>
            ) : null}
          </Stack>
        ) : null}
      </Panel>
    </div>
  );
});
