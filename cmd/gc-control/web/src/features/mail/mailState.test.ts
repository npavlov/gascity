import { describe, expect, it } from "vitest";

import type { MailMessage, MailPage } from "@/lib/api";

import {
  appendMailPage,
  createMailCollection,
  mailIdentityKey,
  replaceMailPages,
  selectMail,
} from "./mailState";

function message(id: string, rig?: string): MailMessage {
  return {
    schema_version: 1,
    id,
    from: "mayor",
    to: "crew",
    subject: id,
    body: id,
    created_at: "2026-07-15T12:00:00Z",
    read: false,
    cc: [],
    rig,
  };
}

function page(items: MailMessage[] | null, total: number, next?: string): MailPage {
  return { schema_version: 1, items, total, next_cursor: next, partial: false, partial_errors: [] };
}

describe("mailState", () => {
  it("appends pages, deduplicates by rig and id, and retains upstream order", () => {
    let state = replaceMailPages(createMailCollection(), "unread", [
      { cursor: "", value: page([message("a", "one"), message("same", "one")], 7, "cursor-2") },
    ]);
    state = appendMailPage(state, "cursor-2", page([
      message("same", "one"),
      message("same", "two"),
      message("b"),
    ], 7));

    expect(state.items.map(mailIdentityKey)).toEqual(["one\u0000a", "one\u0000same", "two\u0000same", "\u0000b"]);
    expect(state.pages).toHaveLength(2);
    expect(state.total).toBe(7);
    expect(state.nextCursor).toBe("");
  });

  it("keeps total separate from loaded length and normalizes nullable arrays", () => {
    const state = replaceMailPages(createMailCollection(), "unread", [
      { cursor: "", value: page(null, 19, "next") },
    ]);

    expect(state.items).toEqual([]);
    expect(state.total).toBe(19);
    expect(state.nextCursor).toBe("next");
  });

  it("preserves exact rig and id selection across a first-page refresh", () => {
    const selected = { id: "same", rig: "two" };
    let state = replaceMailPages(createMailCollection(), "unread", [
      { cursor: "", value: page([message("same", "one"), message("same", "two")], 2) },
    ]);
    state = selectMail(state, selected);
    state = replaceMailPages(state, "unread", [
      { cursor: "", value: page([message("same", "two"), message("same", "one")], 2) },
    ]);

    expect(state.selected).toEqual(selected);
  });

  it("falls to next, then previous, then none when a selected row disappears", () => {
    const initial = replaceMailPages(createMailCollection(), "unread", [
      { cursor: "", value: page([message("a"), message("b"), message("c")], 3) },
    ]);
    const selected = selectMail(initial, { id: "b" });

    const next = replaceMailPages(selected, "unread", [{ cursor: "", value: page([message("a"), message("c")], 2) }]);
    expect(next.selected).toEqual({ id: "c", rig: undefined });

    const previous = replaceMailPages(selected, "unread", [{ cursor: "", value: page([message("a")], 1) }]);
    expect(previous.selected).toEqual({ id: "a", rig: undefined });

    const none = replaceMailPages(selected, "unread", [{ cursor: "", value: page([], 0) }]);
    expect(none.selected).toBeNull();
  });

  it("resets to the first page on filter change and preserves selection only when present", () => {
    let state = replaceMailPages(createMailCollection(), "unread", [
      { cursor: "", value: page([message("a"), message("b")], 2, "older") },
      { cursor: "older", value: page([message("c")], 2) },
    ]);
    state = selectMail(state, { id: "b" });

    const retained = replaceMailPages(state, "all", [{ cursor: "", value: page([message("b"), message("d")], 4, "all-next") }]);
    expect(retained.filter).toBe("all");
    expect(retained.pages).toHaveLength(1);
    expect(retained.selected).toEqual({ id: "b", rig: undefined });

    const changed = replaceMailPages(state, "all", [{ cursor: "", value: page([message("d")], 4) }]);
    expect(changed.selected).toEqual({ id: "d", rig: undefined });
  });
});
