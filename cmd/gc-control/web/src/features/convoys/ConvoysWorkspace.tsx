import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";

import { BeadList } from "@/features/beads/BeadList";
import type { ConvoyDetail, ConvoyList, ConvoySummary, ConvoysAPI, Problem } from "@/lib/api";
import {
  Badge,
  Button,
  EmptyState,
  Panel,
  Progress,
  Spinner,
  Stack,
  Text,
} from "@/ui";

import { SignalList } from "./SignalList";
import "./ConvoysWorkspace.css";

export interface ConvoysWorkspaceHandle {
  refresh(signal?: AbortSignal): Promise<boolean>;
}

export interface ConvoysWorkspaceCache {
  list: ConvoyList | null;
  detail: ConvoyDetail | null;
  items: ConvoySummary[];
}

interface ConvoysWorkspaceProps {
  api: ConvoysAPI;
  cache: ConvoysWorkspaceCache;
  onListChange(list: ConvoyList, items: ConvoySummary[]): void;
  onDetailChange(detail: ConvoyDetail | null): void;
  selectedID: string | null;
  onSelectedIDChange(id: string | null): void;
  onConfirmedChange(confirmed: boolean): void;
  externallyStale: boolean;
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

function nextSelection(previousID: string | null, previousItems: ConvoySummary[], items: ConvoySummary[]) {
  if (previousID === null) return items[0]?.id ?? null;
  if (items.some((item) => item.id === previousID)) return previousID;
  const previousIndex = previousItems.findIndex((item) => item.id === previousID);
  if (previousIndex < 0) return items[0]?.id ?? null;
  return items[previousIndex]?.id ?? items[previousIndex - 1]?.id ?? null;
}

function ProblemList({ problems }: { problems: Problem[] | null }) {
  if (!problems?.length) return null;
  return (
    <Stack className="live-problems" gap="1" role="alert">
      {problems.map((problem, index) => (
        <Text key={`${problem.code}-${problem.resource_id ?? index}`} variant="caption">
          {problem.detail}
        </Text>
      ))}
    </Stack>
  );
}

export const ConvoysWorkspace = forwardRef<ConvoysWorkspaceHandle, ConvoysWorkspaceProps>(function ConvoysWorkspace(
  { api, cache, onListChange, onDetailChange, selectedID, onSelectedIDChange, onConfirmedChange, externallyStale },
  ref,
) {
  const [list, setList] = useState<ConvoyList | null>(() => cache.list);
  const [listError, setListError] = useState(false);
  const [detail, setDetail] = useState<ConvoyDetail | null>(() => cache.detail);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState(false);
  const listController = useRef<AbortController | null>(null);
  const detailController = useRef<AbortController | null>(null);
  const itemsRef = useRef<ConvoySummary[]>(cache.items);
  const selectedRef = useRef(selectedID);
  useEffect(() => {
    selectedRef.current = selectedID;
  }, [selectedID]);

  const loadDetail = useCallback(async (id: string | null, parentSignal?: AbortSignal) => {
    detailController.current?.abort();
    if (!id) {
      onDetailChange(null);
      setDetail(null);
      setDetailError(false);
      setDetailLoading(false);
      return true;
    }
    const controller = new AbortController();
    const abort = () => controller.abort();
    parentSignal?.addEventListener("abort", abort, { once: true });
    if (parentSignal?.aborted) controller.abort();
    detailController.current = controller;
    setDetailLoading(true);
    setDetailError(false);
    try {
      const next = await api.getConvoy(id, controller.signal);
      if (controller.signal.aborted || detailController.current !== controller) return false;
      onDetailChange(next);
      setDetail(next);
      return true;
    } catch (error) {
      if (detailController.current === controller && !controller.signal.aborted && !isAbort(error)) setDetailError(true);
      return false;
    } finally {
      parentSignal?.removeEventListener("abort", abort);
      if (detailController.current === controller) setDetailLoading(false);
    }
  }, [api, onDetailChange]);

  const refresh = useCallback(async (parentSignal?: AbortSignal) => {
    listController.current?.abort();
    detailController.current?.abort();
    setDetailLoading(false);
    const controller = new AbortController();
    const abort = () => controller.abort();
    parentSignal?.addEventListener("abort", abort, { once: true });
    if (parentSignal?.aborted) controller.abort();
    listController.current = controller;
    try {
      const next = await api.listConvoys(controller.signal);
      if (controller.signal.aborted) return false;
      const items = next.items ?? [];
      const nextID = nextSelection(selectedRef.current, itemsRef.current, items);
      itemsRef.current = items;
      selectedRef.current = nextID;
      onListChange(next, items);
      setList(next);
      setListError(false);
      onSelectedIDChange(nextID);
      const detailConfirmed = await loadDetail(nextID, parentSignal);
      const confirmed = detailConfirmed && !next.stale;
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
  }, [api, loadDetail, onConfirmedChange, onListChange, onSelectedIDChange]);

  useImperativeHandle(ref, () => ({ refresh }), [refresh]);

  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (active) void refresh();
    });
    return () => {
      active = false;
      listController.current?.abort();
      detailController.current?.abort();
    };
  }, [refresh]);

  const select = (id: string) => {
    selectedRef.current = id;
    onSelectedIDChange(id);
    void loadDetail(id).then((confirmed) => {
      onConfirmedChange(confirmed && !list?.stale);
    });
  };

  const items = list?.items ?? [];
  const selectedSummary = items.find((item) => item.id === selectedID)
    ?? (detail?.convoy.id === selectedID ? detail.convoy : undefined);
  const stale = externallyStale || list?.stale || Boolean(listError && list);

  return (
    <div className="live-workspace" data-resource="convoys">
      <Panel className="live-workspace__rail" title="Convoys">
        {!list && !listError ? <Spinner label="Loading convoys" /> : null}
        {listError && !list ? (
          <div role="alert"><EmptyState title="Unable to load convoys" description="No last-confirmed convoy list is available." /></div>
        ) : null}
        {list && stale ? <Text className="live-notice" role="status">Showing last confirmed convoy data</Text> : null}
        {list?.degraded ? <Text className="live-notice" role="status">Convoy data is partial</Text> : null}
        <ProblemList problems={list?.problems ?? null} />
        {list && items.length === 0 ? <EmptyState title="No convoys" description="No active or recent tracking convoys were returned." /> : null}
        <Stack className="live-rail-list" gap="2">
          {items.map((item) => (
            <Button
              className="live-rail-row"
              key={item.id}
              variant="quiet"
              aria-label={`Select convoy ${item.title}`}
              aria-pressed={item.id === selectedID}
              onClick={() => select(item.id)}
            >
              <span className="live-rail-row__identity">
                <Text variant="label">{item.title}</Text>
                <Text variant="code">{item.id}</Text>
              </span>
              <Text variant="caption">
                {item.progress ? `${item.progress.closed} / ${item.progress.total}` : "Progress unavailable"}
              </Text>
              <SignalList signals={item.signals} />
            </Button>
          ))}
        </Stack>
      </Panel>

      <Panel className="live-workspace__detail" title={selectedSummary?.title ?? "Convoy detail"}>
        {!selectedID ? <EmptyState title="Select a convoy" description="Choose a tracking convoy to inspect its authoritative detail." /> : null}
        {detailLoading ? <Spinner label="Loading convoy detail" /> : null}
        {detailError && detail?.convoy.id !== selectedID ? <div role="alert"><EmptyState title="Unable to load convoy detail" /></div> : null}
        {detailError && detail?.convoy.id === selectedID ? (
          <Text className="live-notice" role="status">Showing last confirmed convoy detail</Text>
        ) : null}
        {detail && detail.convoy.id === selectedID && !detailLoading ? (
          <Stack gap="4">
            <div className="live-detail-heading">
              <Stack gap="1">
                <Text variant="code">{detail.convoy.id}</Text>
                <Text>{detail.convoy.status}</Text>
              </Stack>
              <SignalList signals={detail.convoy.signals} />
            </div>
            {detail.convoy.progress && detail.convoy.progress.total > 0 ? (
              <Progress
                label={`${detail.convoy.progress.closed} of ${detail.convoy.progress.total} tracked beads complete`}
                value={detail.convoy.progress.closed}
                max={detail.convoy.progress.total}
              />
            ) : <Text variant="caption">Progress unavailable</Text>}
            <ProblemList problems={detail.convoy.problems} />
            <Stack gap="2">
              <Text variant="label">Workflow stage</Text>
              <div className="live-badge-list">
                {(detail.convoy.stage.active ?? []).map((stage) => <Badge key={`active-${stage}`} tone="info">Active: {stage}</Badge>)}
                {(detail.convoy.stage.waiting ?? []).map((stage) => <Badge key={`waiting-${stage}`} tone="warning">Waiting: {stage}</Badge>)}
                {detail.convoy.stage.complete ? <Badge tone="success">Complete</Badge> : null}
              </div>
            </Stack>
            <Stack gap="2">
              <Text variant="label">Tracked beads</Text>
              <BeadList beads={detail.beads} />
            </Stack>
            <Stack gap="2">
              <Text variant="label">Matched sessions</Text>
              {detail.sessions?.length ? detail.sessions.map((session) => (
                <div className="live-session-row" key={session.id}>
                  <Stack gap="1"><Text variant="label">{session.session_name}</Text><Text variant="code">{session.id}</Text></Stack>
                  <Badge tone={session.running ? "info" : "warning"}>{session.running ? "running" : session.state}</Badge>
                </div>
              )) : <Text variant="caption">No matched sessions.</Text>}
            </Stack>
          </Stack>
        ) : null}
      </Panel>
    </div>
  );
});
