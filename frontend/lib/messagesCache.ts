import type { InfiniteData } from "@tanstack/react-query";

import type { MessageWithAuthor, WsMessage } from "@/lib/types";

// The React Query message cache lives here as pure, framework-free helpers so
// the three places that touch it — useMessages (read), useSendMessage
// (optimistic send), and the WebSocket provider (live message.new + reconnect
// resync) — all agree on one shape and one set of merge rules. Everything
// below operates on the useInfiniteQuery structure without importing React or
// React Query hooks.

export const PAGE_SIZE = 50;

// A message still in flight or that failed to send carries a client-only
// `status`; a confirmed server message never has one (the field is optional,
// so real API responses satisfy this type as-is). Kept out of lib/types.ts,
// since that file mirrors the backend 1:1 and this is UI-only state.
export type ChatMessage = MessageWithAuthor & { status?: "sending" | "failed" };

export type MessagesPage = ChatMessage[];

// Page-param left as the default `unknown` to match exactly what
// useInfiniteQuery's `data` is inferred as — these helpers only ever pass
// pageParams through untouched (the keyset cursor is derived from page content
// via nextCursor/getNextPageParam, never from here).
export type MessagesData = InfiniteData<MessagesPage>;

export function messagesQueryKey(roomId: string | undefined) {
  return ["messages", roomId] as const;
}

// Pages arrive newest-first (see CLAUDE.md keyset pagination). The cursor for
// the next (older) page is the created_at of the last element of the last page
// fetched — a short page means there's nothing older left.
export function nextCursor(page: MessagesPage): string | undefined {
  return page.length < PAGE_SIZE ? undefined : page[page.length - 1]?.created_at;
}

// message.new's payload (WsMessage) is narrower than the REST history shape:
// no message_type/updated_at/reactions. Fill the gaps with defaults so a live
// message renders identically to a fetched one. message_type defaults to
// "text" — v1 only ever sends text over WS (image/file are v2 uploads), and
// the REST history carries the real type if it ever matters.
export function wsMessageToChat(msg: WsMessage): ChatMessage {
  return {
    id: msg.id,
    room_id: msg.room_id,
    user: msg.user,
    content: msg.content,
    message_type: "text",
    created_at: msg.created_at,
    updated_at: msg.created_at,
    read_by: msg.read_by ?? [],
    reactions: [],
  };
}

// A message to merge into the cache, plus the optional nonce it should
// reconcile against. clientId is set only for a WS message.new echo (the
// sender's own message coming back) or a REST send's success swap.
export interface IncomingMessage {
  message: ChatMessage;
  clientId?: string;
}

function findById(pages: MessagesPage[], id: string): boolean {
  return pages.some((page) => page.some((m) => m.id === id));
}

// Looks up a single message by id across all held pages — used by the send
// hook to check a bubble's current status before applying a transition (e.g.
// only flip an ack-timeout to "failed", or a retry click to "sending", if
// that's actually still its state — it may have already been reconciled by a
// live event or resync in the background).
export function findMessage(data: MessagesData | undefined, id: string): ChatMessage | undefined {
  return data?.pages.flat().find((m) => m.id === id);
}

// Replaces the first message matching `pred` with `replacement`, or returns
// null if none matched (so the caller can fall through to the next rule).
function replaceFirst(
  pages: MessagesPage[],
  pred: (m: ChatMessage) => boolean,
  replacement: ChatMessage,
): MessagesPage[] | null {
  let done = false;
  const next = pages.map((page) =>
    page.map((m) => {
      if (!done && pred(m)) {
        done = true;
        return replacement;
      }
      return m;
    }),
  );
  return done ? next : null;
}

function applyOne(pages: MessagesPage[], inc: IncomingMessage, currentUserId: string): MessagesPage[] {
  const { message, clientId } = inc;

  // (a) The sender's own optimistic bubble, matched exactly by nonce — the
  //     common WS-send and REST-send success path. Replace it in place.
  if (clientId) {
    const byNonce = replaceFirst(pages, (m) => m.id === clientId, message);
    if (byNonce) return byNonce;
  }

  // (b) Already have this real message (duplicate delivery, or resync overlap).
  if (findById(pages, message.id)) return pages;

  // (c) Resync backstop for a WS send whose live echo was lost: a *confirmed*
  //     message of our own, arriving with no matching nonce, that lines up with
  //     a still-pending/failed bubble by content — reconcile rather than
  //     duplicate. Scoped to our own messages at merge time (never the hot live
  //     path, which (a) covers), so the rare identical-in-flight mispair is an
  //     accepted, self-healing edge.
  if (!message.status && message.user.id === currentUserId) {
    const byContent = replaceFirst(pages, (m) => !!m.status && m.content === message.content, message);
    if (byContent) return byContent;
  }

  // (d) A genuinely new message → front of page 0 (newest-first). This never
  //     touches the last page's tail, so the keyset cursor stays correct; final
  //     display order comes from flattenSortedDedup regardless of page order.
  if (pages.length === 0) return [[message]];
  return [[message, ...pages[0]], ...pages.slice(1)];
}

// Merges incoming messages into the infinite-query cache under the rules above.
// Handles undefined data (first write). Preserves existing pageParams (the
// keyset cursors) since it never adds or removes pages, only page contents.
export function upsertMessages(
  data: MessagesData | undefined,
  incoming: IncomingMessage[],
  currentUserId: string,
): MessagesData {
  let pages: MessagesPage[] = data ? data.pages.map((p) => [...p]) : [];
  for (const inc of incoming) {
    pages = applyOne(pages, inc, currentUserId);
  }
  if (pages.length === 0) pages = [[]];

  const pageParams =
    data && data.pageParams.length === pages.length ? data.pageParams : Array(pages.length).fill(undefined);
  return { pages, pageParams };
}

// Flips a still-"sending" optimistic bubble to "failed" (the send never
// resolved). A no-op if that bubble was already reconciled away — so a
// self-checking ack timeout can call this blindly.
export function markFailed(data: MessagesData | undefined, clientId: string): MessagesData | undefined {
  if (!data) return data;
  return {
    ...data,
    pages: data.pages.map((page) =>
      page.map((m) => (m.id === clientId && m.status === "sending" ? { ...m, status: "failed" as const } : m)),
    ),
  };
}

// The retry counterpart: flips a "failed" bubble back to "sending" right
// before re-attempting it (see useSendMessage's retry). A no-op if the
// bubble is already gone (e.g. a resync reconciled it in the background
// before the user clicked retry).
export function markSending(data: MessagesData | undefined, clientId: string): MessagesData | undefined {
  if (!data) return data;
  return {
    ...data,
    pages: data.pages.map((page) =>
      page.map((m) => (m.id === clientId && m.status === "failed" ? { ...m, status: "sending" as const } : m)),
    ),
  };
}

// Adds userId to messageId's read_by (deduped, order not meaningful) in
// response to a live message.read_by event. A no-op MessagesData reference
// swap when the message isn't found in any held page (e.g. it belongs to a
// room whose cache isn't loaded) or userId is already recorded, so callers
// (queryClient.setQueriesData across every room's cache — the event carries
// no room_id, see CLAUDE.md) can apply this broadly without special-casing.
export function addReadReceipt(
  data: MessagesData | undefined,
  messageId: string,
  userId: string,
): MessagesData | undefined {
  if (!data) return data;
  let changed = false;
  const pages = data.pages.map((page) =>
    page.map((m) => {
      if (m.id === messageId && !m.read_by.includes(userId)) {
        changed = true;
        return { ...m, read_by: [...m.read_by, userId] };
      }
      return m;
    }),
  );
  return changed ? { ...data, pages } : data;
}

// The created_at of the newest *confirmed* message we hold — the resync anchor.
// Optimistic/failed bubbles are excluded because their timestamps are
// client-clock guesses, not server truth.
export function newestConfirmedCreatedAt(data: MessagesData | undefined): string | undefined {
  let newest: string | undefined;
  for (const m of data?.pages.flat() ?? []) {
    if (m.status) continue;
    if (!newest || m.created_at > newest) newest = m.created_at;
  }
  return newest;
}

// Flatten all pages, dedup by id (preferring a confirmed message over an
// optimistic one on the rare collision), and sort ascending by created_at with
// id as a stable tiebreak — the render order, stable across page merges and
// live inserts.
//
// Sorts by parsed time value, not by comparing the created_at strings
// directly: an optimistic bubble's timestamp is a client new Date().
// toISOString() (always UTC, "Z" suffix), but the backend's own
// created_at comes back with the server's local offset (e.g.
// "+05:30") rather than normalized to UTC — two ISO 8601 strings in
// different offsets don't sort correctly by plain string comparison even
// though Date correctly parses either. Found via Phase 15's virtualization
// scroll-preservation test: a locally-only failed bubble sorted into the
// middle of the list instead of at the end, because its "Z" string
// lexicographically preceded the server's "+05:30"-suffixed ones for the
// same clock time. Every other created_at comparison in this file only
// ever compares same-origin (server-to-server) timestamps, so it isn't
// exposed to this — this is the one place client and server timestamps are
// ordered against each other.
export function flattenSortedDedup(data: MessagesData | undefined): ChatMessage[] {
  const byId = new Map<string, ChatMessage>();
  for (const m of data?.pages.flat() ?? []) {
    const existing = byId.get(m.id);
    if (!existing || (existing.status && !m.status)) {
      byId.set(m.id, m);
    }
  }
  return Array.from(byId.values()).sort(
    (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime() || a.id.localeCompare(b.id),
  );
}
