import type { MailMessage, MailPage } from "@/lib/api";

export type MailFilter = "unread" | "all";

export interface MailIdentity {
  id: string;
  rig?: string;
}

export interface LoadedMailPage {
  cursor: string;
  value: MailPage;
}

export interface MailCollection {
  filter: MailFilter;
  pages: LoadedMailPage[];
  items: MailMessage[];
  total: number;
  nextCursor: string;
  selected: MailIdentity | null;
}

export function createMailCollection(): MailCollection {
  return { filter: "unread", pages: [], items: [], total: 0, nextCursor: "", selected: null };
}

export function mailIdentityKey(value: MailIdentity | MailMessage): string {
  return `${value.rig ?? ""}\u0000${value.id}`;
}

export function identityFromMessage(message: MailMessage): MailIdentity {
  return { id: message.id, rig: message.rig };
}

export function selectMail(state: MailCollection, selected: MailIdentity | null): MailCollection {
  return { ...state, selected };
}

export function replaceMailPages(
  state: MailCollection,
  filter: MailFilter,
  pages: LoadedMailPage[],
): MailCollection {
  const normalizedPages = pages.map(({ cursor, value }) => ({
    cursor,
    value: { ...value, items: value.items ?? [], partial_errors: value.partial_errors ?? [] },
  }));
  const items = uniqueMessages(normalizedPages);
  const selected = reconcileSelection(state.selected, state.items, items);
  return {
    filter,
    pages: normalizedPages,
    items,
    total: normalizedPages[0]?.value.total ?? 0,
    nextCursor: normalizedPages.at(-1)?.value.next_cursor ?? "",
    selected,
  };
}

export function appendMailPage(state: MailCollection, cursor: string, value: MailPage): MailCollection {
  const nextPage = { cursor, value };
  const existing = state.pages.findIndex((page) => page.cursor === cursor);
  const pages = existing < 0
    ? [...state.pages, nextPage]
    : state.pages.map((page, index) => (index === existing ? nextPage : page));
  return replaceMailPages(state, state.filter, pages);
}

function uniqueMessages(pages: LoadedMailPage[]): MailMessage[] {
  const seen = new Set<string>();
  const result: MailMessage[] = [];
  for (const page of pages) {
    for (const message of page.value.items ?? []) {
      const key = mailIdentityKey(message);
      if (seen.has(key)) continue;
      seen.add(key);
      result.push(message);
    }
  }
  return result;
}

function reconcileSelection(
  selected: MailIdentity | null,
  previousItems: MailMessage[],
  nextItems: MailMessage[],
): MailIdentity | null {
  if (selected === null) return nextItems[0] ? identityFromMessage(nextItems[0]) : null;
  const selectedKey = mailIdentityKey(selected);
  const retained = nextItems.find((message) => mailIdentityKey(message) === selectedKey);
  if (retained) return identityFromMessage(retained);
  const previousIndex = previousItems.findIndex((message) => mailIdentityKey(message) === selectedKey);
  if (previousIndex < 0) return nextItems[0] ? identityFromMessage(nextItems[0]) : null;
  const fallback = nextItems[previousIndex] ?? nextItems[previousIndex - 1];
  return fallback ? identityFromMessage(fallback) : null;
}
