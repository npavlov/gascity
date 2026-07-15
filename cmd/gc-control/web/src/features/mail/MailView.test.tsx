import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { MailMessage, MailPage, MailThread } from "@/lib/api";

import { MailView } from "./MailView";
import type { MailModel } from "./useMail";

afterEach(cleanup);

const first: MailMessage = {
  schema_version: 1,
  id: "mail-1",
  from: "mayor",
  to: "crew",
  cc: ["reviewer"],
  subject: "Fix the gate",
  body: "Please inspect <script>alert('no')</script> as plain text.",
  created_at: "2026-07-15T10:00:00Z",
  read: false,
  thread_id: "thread-1",
  reply_to: "mail-root",
  priority: 2,
  rig: "taxdome",
};

const second: MailMessage = {
  ...first,
  id: "mail-2",
  from: "worker",
  subject: "Gate fixed",
  body: "The gate now passes.",
  created_at: "2026-07-15T10:02:00Z",
  read: true,
};

function page(overrides: Partial<MailPage> = {}): MailPage {
  return {
    schema_version: 1,
    items: [first, second],
    total: 8,
    next_cursor: "next-page",
    partial: false,
    partial_errors: [],
    ...overrides,
  };
}

function thread(overrides: Partial<MailThread> = {}): MailThread {
  return {
    schema_version: 1,
    items: [first, second],
    total: 2,
    partial: false,
    truncated: false,
    partial_errors: [],
    ...overrides,
  };
}

function model(overrides: Partial<MailModel> = {}): MailModel {
  const firstPage = page();
  return {
    collection: {
      filter: "unread",
      pages: [{ cursor: "", value: firstPage }],
      items: firstPage.items ?? [],
      total: firstPage.total,
      nextCursor: firstPage.next_cursor ?? "",
      selected: { id: first.id, rig: first.rig },
    },
    count: { schema_version: 1, total: 8, unread: 3, partial: false, partial_errors: [] },
    detail: { identity: { id: first.id, rig: first.rig }, value: first },
    thread: { identity: { id: first.id, rig: first.rig }, value: thread() },
    loading: { count: false, list: false, detail: false, thread: false },
    errors: { count: false, list: false, detail: false, thread: false, notFound: false },
    stale: false,
    disconnected: false,
    setFilter: vi.fn(),
    select: vi.fn(),
    loadMore: vi.fn(),
    refresh: vi.fn(),
    onInvalidation: vi.fn(),
    onDisconnect: vi.fn(),
    onReconnect: vi.fn(),
    ...overrides,
  };
}

describe("MailView", () => {
  it("renders a controlled inbox, exact selection, full detail, and ordered plain-text thread", async () => {
    const user = userEvent.setup();
    const value = model();
    const { container } = render(<MailView model={value} />);

    expect(screen.getByRole("button", { name: "Unread mail" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "All mail" })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByText("2 loaded of 8")).toBeVisible();
    expect(screen.getAllByText("Fix the gate")[0]).toBeVisible();
    expect(screen.getAllByText("mayor")[0]).toBeVisible();
    expect(screen.getAllByText("taxdome")[0]).toBeVisible();
    expect(screen.getAllByText("Unread")[0]).toBeVisible();
    expect(screen.getAllByText("Read")[0]).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Select mail Gate fixed from worker" }));
    expect(value.select).toHaveBeenCalledWith({ id: "mail-2", rig: "taxdome" });
    await user.click(screen.getByRole("button", { name: "All mail" }));
    expect(value.setFilter).toHaveBeenCalledWith("all");
    await user.click(screen.getByRole("button", { name: "Load more mail" }));
    expect(value.loadMore).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Refresh mail" }));
    expect(value.refresh).toHaveBeenCalledTimes(1);

    expect(screen.getByText("crew")).toBeVisible();
    expect(screen.getByText("reviewer")).toBeVisible();
    expect(screen.getByText("mail-root")).toBeVisible();
    expect(screen.getByText("thread-1")).toBeVisible();
    expect(screen.getByText("Priority 2")).toBeVisible();
    expect(screen.getByTestId("mail-detail-body")).toHaveTextContent(first.body);
    expect(container.querySelector("script")).not.toBeInTheDocument();

    const threadMessages = screen.getAllByTestId("mail-thread-message");
    expect(threadMessages).toHaveLength(2);
    expect(threadMessages[0]).toHaveTextContent("mayor");
    expect(threadMessages[0]).toHaveTextContent("Unread");
    expect(threadMessages[1]).toHaveTextContent("worker");
    expect(threadMessages[1]).toHaveTextContent("Read");

    for (const excluded of ["Reply", "Send", "Archive", "Delete", "Mark read", "Mark unread"]) {
      expect(screen.queryByRole("button", { name: excluded })).not.toBeInTheDocument();
    }
    expect(container.querySelector("textarea, input, select")).not.toBeInTheDocument();
  });

  it("hides pagination without a next cursor and distinguishes loading, empty, not-found, and hard errors", () => {
    const empty = model({
      collection: { filter: "unread", pages: [], items: [], total: 0, nextCursor: "", selected: null },
      detail: null,
      thread: null,
      loading: { count: false, list: true, detail: false, thread: false },
    });
    const view = render(<MailView model={empty} />);
    expect(screen.getByRole("status", { name: "Loading mail" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "Load more mail" })).not.toBeInTheDocument();

    view.rerender(<MailView model={model({ collection: { ...empty.collection }, loading: { count: false, list: false, detail: false, thread: false } })} />);
    expect(screen.getByRole("heading", { name: "No mail" })).toBeVisible();

    view.rerender(<MailView model={model({ errors: { count: false, list: true, detail: false, thread: false, notFound: false }, collection: empty.collection, detail: null, thread: null })} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Unable to load mail");

    view.rerender(<MailView model={model({ errors: { count: false, list: false, detail: true, thread: false, notFound: true }, detail: null, thread: null })} />);
    expect(screen.getByRole("heading", { name: "Mail not found" })).toBeVisible();
  });

  it("keeps usable data visible with partial, stale, disconnected, and thread failure notices", () => {
    const partialPage = page({ partial: true, partial_errors: ["rig unavailable"] });
    render(<MailView model={model({
      collection: {
        filter: "unread",
        pages: [{ cursor: "", value: partialPage }],
        items: partialPage.items ?? [],
        total: partialPage.total,
        nextCursor: "",
        selected: { id: first.id, rig: first.rig },
      },
      thread: { identity: { id: first.id, rig: first.rig }, value: thread({ partial: true, truncated: true, partial_errors: ["thread truncated"] }) },
      errors: { count: false, list: false, detail: false, thread: true, notFound: false },
      stale: true,
      disconnected: true,
    })} />);

    expect(screen.getByText("Mail connection is unavailable")).toBeVisible();
    expect(screen.getByText("Showing last confirmed mail data")).toBeVisible();
    expect(screen.getByText("Mail data is partial")).toBeVisible();
    expect(screen.getByText("rig unavailable")).toBeVisible();
    expect(screen.getByText("Thread is partial and truncated")).toBeVisible();
    expect(screen.getByText("thread truncated")).toBeVisible();
    expect(screen.getByText("Unable to refresh thread; showing last confirmed messages")).toBeVisible();
    expect(screen.getAllByText("Fix the gate")[0]).toBeVisible();
  });

  it("renders a successful thread independently when selected detail is not found", () => {
    render(<MailView model={model({
      detail: null,
      thread: { identity: { id: first.id, rig: first.rig }, value: thread() },
      errors: { count: false, list: false, detail: true, thread: false, notFound: true },
    })} />);

    expect(screen.getByRole("heading", { name: "Mail not found" })).toBeVisible();
    expect(screen.getByLabelText("Mail thread")).toBeVisible();
    expect(screen.getAllByTestId("mail-thread-message")).toHaveLength(2);
    expect(screen.getByText("The gate now passes.")).toBeVisible();
  });
});
