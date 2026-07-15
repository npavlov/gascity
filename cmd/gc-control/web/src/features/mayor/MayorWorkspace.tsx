import { useLayoutEffect, useRef } from "react";

import type { MayorState } from "@/lib/api";
import {
  AlertIcon,
  Badge,
  Button,
  CheckIcon,
  EmptyState,
  MessageIcon,
  Panel,
  RefreshIcon,
  Spinner,
  Stack,
  StatusSignal,
  Text,
  Textarea,
} from "@/ui";
import type { StatusTone, UIIcon } from "@/ui";

import type { MayorController } from "./useMayor";
import "./MayorWorkspace.css";

export interface MayorWorkspaceProps {
  controller: MayorController;
}

const statePresentation: Record<MayorState, { label: string; tone: StatusTone; icon: UIIcon }> = {
  available_dormant: { label: "Ready on first message", tone: "info", icon: MessageIcon },
  idle: { label: "Idle", tone: "success", icon: CheckIcon },
  in_turn: { label: "In turn", tone: "info", icon: MessageIcon },
  sleeping: { label: "Sleeping", tone: "warning", icon: MessageIcon },
  stopped: { label: "Stopped", tone: "warning", icon: AlertIcon },
  unsupported: { label: "Unsupported", tone: "danger", icon: AlertIcon },
  missing: { label: "Not configured", tone: "danger", icon: AlertIcon },
  ambiguous: { label: "Identity ambiguous", tone: "danger", icon: AlertIcon },
  disconnected: { label: "Disconnected", tone: "danger", icon: AlertIcon },
};

const failureTitles = {
  missing: "Mayor is not configured",
  ambiguous: "Mayor identity is ambiguous",
  disconnected: "Mayor is disconnected",
} as const;

export function MayorWorkspace({ controller }: MayorWorkspaceProps) {
  const transcriptRef = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    const transcript = transcriptRef.current;
    if (transcript && transcript.scrollTop !== controller.scrollOffset) {
      transcript.scrollTop = controller.scrollOffset;
    }
  }, [controller.scrollOffset]);

  if (controller.loading && !controller.view) {
    return (
      <Panel className="mayor-workspace__loading" title="Mayor">
        <Spinner label="Loading Mayor workspace" />
      </Panel>
    );
  }

  if (!controller.view && controller.failureState) {
    const title = failureTitles[controller.failureState];
    return (
      <Panel className="mayor-workspace__failure" title="Mayor">
        <EmptyState
          title={title}
          description={controller.error ?? "The configured named session cannot be resolved."}
          icon={AlertIcon}
          actions={<Button onClick={() => void controller.refresh()}>Retry</Button>}
        />
      </Panel>
    );
  }

  if (!controller.view) {
    return (
      <Panel className="mayor-workspace__failure" title="Mayor">
        <EmptyState title="Mayor workspace is unavailable" description={controller.error ?? "No confirmed Mayor state is available."} icon={AlertIcon} />
      </Panel>
    );
  }

  const view = controller.view;
  const presentation = statePresentation[view.state];
  const pendingOptions = view.pending?.options ?? [];
  const pendingBusy = controller.mutation !== null;

  return (
    <div className="mayor-workspace" data-resource="mayor">
      <Panel
        className="mayor-workspace__status"
        title="Mayor session"
        actions={
          <Button size="compact" onClick={() => void controller.refresh()} disabled={controller.loading}>
            <RefreshIcon aria-hidden="true" /> Refresh
          </Button>
        }
      >
        <Stack gap="3">
          <div className="mayor-workspace__status-row">
            <StatusSignal label={presentation.label} tone={presentation.tone} icon={presentation.icon} />
            {view.degraded ? <Badge tone="warning">Degraded</Badge> : null}
            {controller.stale ? <Badge tone="warning">Last confirmed</Badge> : null}
          </div>
          <Stack gap="1">
            <Text variant="label">Configured identity</Text>
            <Text variant="code">{view.identity}</Text>
          </Stack>
          {view.session_name || view.provider || view.model ? (
            <div className="mayor-workspace__facts">
              {view.session_name ? <Text variant="caption">Session: {view.session_name}</Text> : null}
              {view.provider ? <Text variant="caption">Provider: {view.provider}</Text> : null}
              {view.model ? <Text variant="caption">Model: {view.model}</Text> : null}
            </div>
          ) : null}
          {controller.stale ? <Text className="mayor-workspace__notice" role="status">Showing last confirmed Mayor data</Text> : null}
          {controller.error ? <Text className="mayor-workspace__error" role="alert">{controller.error}</Text> : null}
          {view.problems?.length ? (
            <Stack className="mayor-workspace__problems" gap="1" role="alert">
              {view.problems.map((problem, index) => (
                <Text key={`${problem.code}-${problem.source}-${index}`} variant="caption">{problem.detail}</Text>
              ))}
            </Stack>
          ) : null}
        </Stack>
      </Panel>

      <div className="mayor-workspace__main">
        <Panel
          className="mayor-workspace__conversation"
          title="Mayor conversation"
          actions={controller.hasOlder ? (
            <Button size="compact" onClick={() => void controller.loadOlder()} disabled={controller.loadingOlder}>
              {controller.loadingOlder ? "Loading…" : "Load older"}
            </Button>
          ) : null}
        >
          <Stack gap="4">
            {controller.turns.length === 0 ? (
              <EmptyState title="No Mayor messages yet" description="Send the first message to materialize a dormant Mayor session." icon={MessageIcon} />
            ) : (
              <div
                ref={transcriptRef}
                className="mayor-transcript"
                aria-label="Mayor transcript"
                onScroll={(event) => controller.setScrollOffset(event.currentTarget.scrollTop)}
              >
                {controller.turns.map((turn, index) => (
                  <article className="mayor-turn" data-role={turn.role} key={`${turn.role}-${turn.timestamp ?? "untimed"}-${index}`}>
                    <div className="mayor-turn__meta">
                      <Text variant="label">{turn.role}</Text>
                      {turn.timestamp ? <Text variant="caption">{new Date(turn.timestamp).toLocaleString()}</Text> : null}
                    </div>
                    <Text className="mayor-turn__text">{turn.text}</Text>
                  </article>
                ))}
              </div>
            )}

            <div className="mayor-live-announcer" aria-live="polite" aria-atomic="true">
              {controller.liveTurn ? <Text variant="caption">{controller.liveTurn.text}</Text> : null}
            </div>

            <form
              className="mayor-composer"
              onSubmit={(event) => {
                event.preventDefault();
                void controller.send();
              }}
            >
              <Textarea
                aria-label="Message Mayor"
                placeholder="Message the configured Mayor session"
                value={controller.composerDraft}
                onChange={(event) => controller.setComposerDraft(event.currentTarget.value)}
                disabled={controller.mutation !== null}
              />
              <div className="mayor-composer__actions">
                <Text variant="caption">
                  {view.state === "in_turn" ? "Active messages use follow-up when supported." : "The server derives a safe submit intent."}
                </Text>
                <Button aria-label="Send to Mayor" variant="primary" type="submit" disabled={!controller.canSend}>
                  {controller.mutation === "send" ? "Sending…" : "Send to Mayor"}
                </Button>
              </div>
            </form>

            {controller.receipt ? <Text className="mayor-workspace__notice" role="status">Mayor message {controller.receipt.status}</Text> : null}
            {controller.mutationError ? <Text className="mayor-workspace__error" role="alert">{controller.mutationError}</Text> : null}
          </Stack>
        </Panel>

        {view.pending ? (
          <Panel className="mayor-workspace__pending" title="Mayor needs input">
            <Stack gap="3">
              <Text>{view.pending.prompt ?? "The Mayor session is waiting for an explicit response."}</Text>
              <Textarea
                aria-label="Mayor response details"
                placeholder="Optional response details"
                value={controller.pendingDraft}
                onChange={(event) => controller.setPendingDraft(event.currentTarget.value)}
                disabled={pendingBusy}
              />
              <div className="mayor-workspace__pending-actions">
                {pendingOptions.length > 0 ? pendingOptions.map((option) => (
                  <Button key={option} disabled={pendingBusy} onClick={() => void controller.respond(option)} aria-label={`Answer ${option}`}>
                    {option}
                  </Button>
                )) : (
                  <Button disabled={pendingBusy} onClick={() => void controller.respond("submit")} aria-label="Answer Mayor">
                    Answer Mayor
                  </Button>
                )}
              </div>
            </Stack>
          </Panel>
        ) : null}
      </div>
    </div>
  );
}
