import type { MailMessage } from "@/lib/api";
import {
  AlertIcon,
  Badge,
  Button,
  CheckIcon,
  EmptyState,
  IconButton,
  MailIcon,
  Panel,
  RefreshIcon,
  Skeleton,
  Spinner,
  Stack,
  StatusSignal,
  Text,
} from "@/ui";

import { identityFromMessage, mailIdentityKey } from "./mailState";
import type { MailIdentity } from "./mailState";
import type { MailModel } from "./useMail";
import "./MailView.css";

export interface MailViewProps {
  model: MailModel;
}

function sameIdentity(left: MailIdentity, right: MailIdentity) {
  return mailIdentityKey(left) === mailIdentityKey(right);
}

function readableTimestamp(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleString();
}

function ReadSignal({ message }: { message: MailMessage }) {
  return (
    <StatusSignal
      className="mail-read-signal"
      label={message.read ? "Read" : "Unread"}
      tone={message.read ? "neutral" : "info"}
      icon={message.read ? CheckIcon : MailIcon}
    />
  );
}

function Notice({ children, tone = "warning" }: { children: React.ReactNode; tone?: "warning" | "danger" | "info" }) {
  return (
    <StatusSignal
      className="mail-notice"
      role="status"
      label={String(children)}
      tone={tone}
      icon={tone === "danger" ? AlertIcon : MailIcon}
    />
  );
}

function MailMetadata({ message }: { message: MailMessage }) {
  return (
    <dl className="mail-metadata">
      <div><dt>From</dt><dd>{message.from}</dd></div>
      <div><dt>To</dt><dd>{message.to}</dd></div>
      <div><dt>CC</dt><dd>{message.cc?.length ? message.cc.join(", ") : "None"}</dd></div>
      <div><dt>Sent</dt><dd><time dateTime={message.created_at}>{readableTimestamp(message.created_at)}</time></dd></div>
      <div><dt>Priority</dt><dd>{message.priority === undefined ? "Default priority" : `Priority ${message.priority}`}</dd></div>
      <div><dt>Reply to</dt><dd>{message.reply_to ?? "None"}</dd></div>
      <div><dt>Thread</dt><dd>{message.thread_id ?? message.id}</dd></div>
      <div><dt>Rig</dt><dd>{message.rig ?? "City"}</dd></div>
    </dl>
  );
}

export function MailView({ model }: MailViewProps) {
  const { collection, loading, errors } = model;
  const selectedRow = collection.selected
    ? collection.items.find((message) => sameIdentity(message, collection.selected!))
    : undefined;
  const detail = collection.selected && model.detail && sameIdentity(model.detail.identity, collection.selected)
    ? model.detail.value
    : null;
  const thread = collection.selected && model.thread && sameIdentity(model.thread.identity, collection.selected)
    ? model.thread.value
    : null;
  const pageProblems = collection.pages.flatMap((page) => page.value.partial_errors ?? []);
  const partial = collection.pages.some((page) => page.value.partial);

  return (
    <div className="mail-workspace" data-resource="mail">
      <Panel
        className="mail-workspace__rail"
        title="Mail"
        actions={<IconButton icon={RefreshIcon} aria-label="Refresh mail" variant="quiet" size="compact" onClick={model.refresh} />}
      >
        <div className="mail-filter" aria-label="Mail filter">
          <Button
            size="compact"
            variant={collection.filter === "unread" ? "primary" : "quiet"}
            aria-label="Unread mail"
            aria-pressed={collection.filter === "unread"}
            onClick={() => model.setFilter("unread")}
          >
            Unread
          </Button>
          <Button
            size="compact"
            variant={collection.filter === "all" ? "primary" : "quiet"}
            aria-label="All mail"
            aria-pressed={collection.filter === "all"}
            onClick={() => model.setFilter("all")}
          >
            All
          </Button>
        </div>

        <Text className="mail-loaded-count" variant="caption">
          {collection.items.length} loaded of {collection.total}
        </Text>
        <Stack className="mail-notices" gap="2">
          {model.disconnected ? <Notice tone="danger">Mail connection is unavailable</Notice> : null}
          {model.stale ? <Notice>Showing last confirmed mail data</Notice> : null}
          {partial ? <Notice tone="info">Mail data is partial</Notice> : null}
          {pageProblems.map((problem, index) => <Text key={`${problem}-${index}`} className="mail-problem" variant="caption">{problem}</Text>)}
        </Stack>

        {loading.list && collection.items.length === 0 ? (
          <Stack className="mail-loading" gap="2" role="status" aria-label="Loading mail">
            <Spinner label="Loading mail list" />
            <Skeleton />
            <Skeleton />
          </Stack>
        ) : null}
        {errors.list && collection.items.length === 0 ? (
          <div role="alert"><EmptyState icon={AlertIcon} title="Unable to load mail" description="No last-confirmed inbox page is available." /></div>
        ) : null}
        {!loading.list && !errors.list && collection.items.length === 0 ? (
          <EmptyState icon={MailIcon} title="No mail" description="No messages match the current filter." />
        ) : null}

        <Stack className="mail-list" gap="2">
          {collection.items.map((message) => {
            const identity = identityFromMessage(message);
            const selected = collection.selected ? sameIdentity(identity, collection.selected) : false;
            return (
              <Button
                className="mail-row"
                key={mailIdentityKey(message)}
                variant="quiet"
                aria-label={`Select mail ${message.subject} from ${message.from}`}
                aria-pressed={selected}
                onClick={() => model.select(identity)}
              >
                <span className="mail-row__heading">
                  <Text variant="label">{message.subject}</Text>
                  <ReadSignal message={message} />
                </span>
                <span className="mail-row__meta">
                  <Text variant="caption">{message.from}</Text>
                  <time dateTime={message.created_at}>{readableTimestamp(message.created_at)}</time>
                </span>
                <span className="mail-row__meta">
                  <Text variant="code">{message.rig ?? "City"}</Text>
                  <Text variant="code">{message.id}</Text>
                </span>
              </Button>
            );
          })}
        </Stack>

        {collection.nextCursor ? (
          <Button className="mail-load-more" variant="secondary" aria-label="Load more mail" onClick={model.loadMore} disabled={loading.list}>
            {loading.list ? "Loading…" : "Load more"}
          </Button>
        ) : null}
      </Panel>

      <Panel className="mail-workspace__detail" title={selectedRow?.subject ?? "Mail detail"}>
        {!collection.selected ? <EmptyState icon={MailIcon} title="Select a message" description="Choose a message to inspect its detail and thread." /> : null}
        {errors.notFound ? <EmptyState icon={AlertIcon} title="Mail not found" description="The selected message no longer exists." /> : null}
        {loading.detail && !detail ? <Spinner label="Loading mail detail" /> : null}
        {errors.detail && !errors.notFound && !detail ? (
          <div role="alert"><EmptyState icon={AlertIcon} title="Unable to load mail detail" /></div>
        ) : null}
        {errors.detail && detail ? <Notice>Unable to refresh detail; showing last confirmed message</Notice> : null}

        {detail ? (
          <Stack gap="5">
            <div className="mail-detail-heading">
              <Stack gap="1">
                <Text variant="title">{detail.subject}</Text>
                <Text variant="code">{detail.id}</Text>
              </Stack>
              <ReadSignal message={detail} />
            </div>
            <MailMetadata message={detail} />
            <Text className="mail-body" data-testid="mail-detail-body">{detail.body}</Text>
          </Stack>
        ) : null}

        {collection.selected ? (
          <Stack className="mail-thread" gap="3" aria-label="Mail thread">
            <div className="mail-thread-heading">
              <Text variant="title">Thread</Text>
              {thread ? <Badge tone={thread.partial || thread.truncated ? "warning" : "neutral"}>{thread.items?.length ?? 0} of {thread.total}</Badge> : null}
            </div>
            {loading.thread && !thread ? <Spinner label="Loading mail thread" /> : null}
            {errors.thread && !thread ? <div role="alert"><EmptyState icon={AlertIcon} title="Unable to load thread" /></div> : null}
            {errors.thread && thread ? <Notice>Unable to refresh thread; showing last confirmed messages</Notice> : null}
            {thread?.partial || thread?.truncated ? <Notice tone="info">Thread is partial and truncated</Notice> : null}
            {thread?.partial_errors?.map((problem, index) => <Text key={`${problem}-${index}`} className="mail-problem" variant="caption">{problem}</Text>)}
            {thread && (thread.items?.length ?? 0) === 0 ? <EmptyState title="No thread messages" /> : null}
            {thread?.items?.map((message) => (
              <article className="mail-thread-message" data-testid="mail-thread-message" key={mailIdentityKey(message)}>
                <div className="mail-thread-message__heading">
                  <Stack gap="1">
                    <Text variant="label">{message.from}</Text>
                    <time dateTime={message.created_at}>{readableTimestamp(message.created_at)}</time>
                  </Stack>
                  <ReadSignal message={message} />
                </div>
                <Text className="mail-body">{message.body}</Text>
              </article>
            ))}
          </Stack>
        ) : null}
      </Panel>
    </div>
  );
}
