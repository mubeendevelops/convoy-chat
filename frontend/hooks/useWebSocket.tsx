"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { useAuthStore } from "@/lib/auth-store";
import {
  addReadReceipt,
  applyReaction,
  messagesQueryKey,
  newestConfirmedCreatedAt,
  PAGE_SIZE,
  upsertMessages,
  wsMessageToChat,
  type ChatMessage,
  type IncomingMessage,
  type MessagesData,
} from "@/lib/messagesCache";
import { usePresenceStore } from "@/lib/presence-store";
import type { ClientEvent, MessageWithAuthor, ServerEvent } from "@/lib/types";

const WS_URL = process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8080/ws";

// Reconnect backoff: 1s, 2s, 4s … capped at 30s, each with equal jitter
// (half fixed + half random) so a fleet of clients doesn't reconnect in
// lockstep after a server blip. Reset to 0 on a successful open.
const BASE_DELAY_MS = 1000;
const MAX_DELAY_MS = 30000;
// Hard cap on resync paging so a pathological gap can't loop unbounded.
const MAX_RESYNC_PAGES = 20;

type ConnStatus = "connecting" | "open" | "closed";
type ServerEventType = ServerEvent["type"];
type Handler<T extends ServerEventType> = (event: Extract<ServerEvent, { type: T }>) => void;

interface WebSocketContextValue {
  status: ConnStatus;
  /** True once the socket has completed at least one successful handshake —
   * lets consumers (the reconnecting banner) distinguish "still doing the
   * first connect" from "was open, dropped, now reconnecting". */
  hasConnectedOnce: boolean;
  /** Send a typed client event. Returns true only if it went out (socket open). */
  send: (event: ClientEvent) => boolean;
  /** Register a listener for one server event type; returns an unsubscribe fn. */
  subscribe: <T extends ServerEventType>(type: T, handler: Handler<T>) => () => void;
  joinRoom: (roomId: string) => void;
  leaveRoom: (roomId: string) => void;
  /** Skip the remaining backoff wait and retry immediately. A harmless no-op
   * if a connection attempt is already open or in flight. */
  reconnectNow: () => void;
}

const WebSocketContext = createContext<WebSocketContextValue | null>(null);

// One WebSocket for the whole authenticated chat session. Mounted in
// app/chat/layout.tsx, which persists across /chat/* route changes, so the
// socket survives room switches. Owns: connection lifecycle + reconnect,
// active-room (re)join tracking, reconnect resync, and central routing of
// inbound events into the React Query message cache / presence store. Any
// component can also observe raw events via subscribe() (used from Phase 14).
export function WebSocketProvider({ children }: { children: ReactNode }) {
  const token = useAuthStore((s) => s.token);
  const hasHydrated = useAuthStore((s) => s.hasHydrated);
  const queryClient = useQueryClient();

  const [status, setStatus] = useState<ConnStatus>("connecting");
  const [hasConnectedOnce, setHasConnectedOnce] = useState(false);

  const wsRef = useRef<WebSocket | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const intentionalCloseRef = useRef(false);
  const hasConnectedOnceRef = useRef(false);
  const joinedRoomsRef = useRef<Set<string>>(new Set());
  const subscribersRef = useRef<Map<ServerEventType, Set<(e: ServerEvent) => void>>>(new Map());
  // Populated inside the connection effect so reconnectNow (a stable
  // useCallback declared outside it) can trigger an out-of-band connect.
  const connectFnRef = useRef<() => void>(() => {});

  // ---- Context API (stable references, read live socket/refs) ----

  const send = useCallback((event: ClientEvent): boolean => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(event));
      return true;
    }
    return false;
  }, []);

  const joinRoom = useCallback(
    (roomId: string) => {
      joinedRoomsRef.current.add(roomId);
      send({ type: "room.join", room_id: roomId });
    },
    [send],
  );

  const leaveRoom = useCallback(
    (roomId: string) => {
      joinedRoomsRef.current.delete(roomId);
      send({ type: "room.leave", room_id: roomId });
    },
    [send],
  );

  const reconnectNow = useCallback(() => {
    const ws = wsRef.current;
    // Already open or a handshake already in flight — forcing a second
    // connect() here would orphan that socket instead of helping.
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
    reconnectAttemptsRef.current = 0;
    connectFnRef.current();
  }, []);

  const subscribe = useCallback(<T extends ServerEventType>(type: T, handler: Handler<T>) => {
    let set = subscribersRef.current.get(type);
    if (!set) {
      set = new Set();
      subscribersRef.current.set(type, set);
    }
    const fn = handler as (e: ServerEvent) => void;
    set.add(fn);
    return () => {
      subscribersRef.current.get(type)?.delete(fn);
    };
  }, []);

  // ---- Inbound routing ----

  const routeEvent = useCallback(
    (event: ServerEvent) => {
      switch (event.type) {
        case "message.new": {
          const roomId = event.message.room_id;
          const incoming: IncomingMessage = {
            message: wsMessageToChat(event.message),
            clientId: event.message.client_id,
          };
          const currentUserId = useAuthStore.getState().user?.id ?? "";
          // Only merge when the room's history is already loaded — we only ever
          // receive events for a room we've joined (i.e. one whose ChatWindow is
          // mounted and has fetched page 1), so `old` is defined in practice.
          // Guarding on it avoids seeding a partial cache for a room the user
          // hasn't opened, which would suppress its real history fetch.
          queryClient.setQueryData<MessagesData>(messagesQueryKey(roomId), (old) =>
            old ? upsertMessages(old, [incoming], currentUserId) : old,
          );
          break;
        }
        case "user.status_changed":
          usePresenceStore.getState().setStatus(event.user_id, event.status, event.last_seen_at);
          break;
        case "user.joined":
          // A join implies the user is online; the authoritative signal is
          // user.status_changed, which the backend also fans out.
          usePresenceStore.getState().setStatus(event.user.id, "online");
          // Inviting has no live broadcast of its own (REST-only, see
          // CLAUDE.md) — this is the first signal an already-open room gets
          // that its member list might be stale, since it fires whenever
          // *anyone* WS room.joins it, new member or not (an existing
          // member just reopening the room fires it too — invalidating then
          // is a harmless extra refetch, not a correctness issue, and far
          // simpler than trying to tell the two cases apart client-side).
          // Found via Phase 17 QA: a room left open in one tab never picked
          // up a member invited after it was opened — the members sheet
          // silently omitted them and TypingIndicator fell back to
          // "Someone" for their username, both because RoomHeader/
          // MembersList/TypingIndicator all read from this same
          // now-stale room.members array with nothing to ever refresh it.
          queryClient.invalidateQueries({ queryKey: ["room", event.room_id] });
          break;
        case "user.left":
          // Mirror of user.joined: a member leaving makes the room's cached
          // members[] stale, so the members sheet, header count, and
          // TypingIndicator's username lookups all keep showing them until a
          // refetch. Invalidate the room detail to drop them live. Fires for a
          // WS room.leave (the leaver's ChatWindow unmount on navigating out
          // after POST /rooms/{id}/leave) and for a dropped connection the hub
          // synthesizes a leave for — see CLAUDE.md's WebSocket event contract.
          queryClient.invalidateQueries({ queryKey: ["room", event.room_id] });
          break;
        case "message.read_by":
          // No room_id on this event (see CLAUDE.md) — the marker already
          // knows the room they're viewing, but we don't, so patch every
          // cached room's messages and let addReadReceipt no-op wherever the
          // message isn't found. In practice only one room's cache is ever
          // "live" at a time (ChatWindow joins/leaves as the user navigates),
          // so this touches at most one real match.
          queryClient.setQueriesData<MessagesData>({ queryKey: ["messages"] }, (old) =>
            addReadReceipt(old, event.message_id, event.read_by_user_id),
          );
          break;
        case "message.reaction":
          // No room_id on this event either (see CLAUDE.md) — same broad-
          // apply-and-no-op pattern as message.read_by above. This is the
          // only place a reaction toggle's REST call and the toggling
          // user's own UI update meet: reacting has no client→server WS
          // event (REST-only to trigger), so even our own toggle is only
          // reflected here, once the broadcast round-trips back.
          queryClient.setQueriesData<MessagesData>({ queryKey: ["messages"] }, (old) =>
            applyReaction(old, event.message_id, event.user_id, event.emoji, event.action),
          );
          break;
        case "error":
          console.warn("ws error event:", event.code, event.message);
          break;
        default:
          break;
      }

      // Fan out to component subscribers regardless of central handling, so a
      // later phase can observe typing / read receipts / reactions without
      // touching this switch.
      subscribersRef.current.get(event.type)?.forEach((listener) => listener(event));
    },
    [queryClient],
  );

  // Backfill messages missed while the socket was down. Anchored on the newest
  // *confirmed* message we hold; pages backward (reusing keyset pagination)
  // until it overlaps that anchor, then merges — deduped by id, preserving
  // local optimistic/failed bubbles (see upsertMessages).
  const resyncRoom = useCallback(
    async (roomId: string) => {
      const existing = queryClient.getQueryData<MessagesData>(messagesQueryKey(roomId));
      const lastSeen = newestConfirmedCreatedAt(existing);
      if (!lastSeen) return; // nothing loaded to anchor on — the initial query covers it

      const collected: ChatMessage[] = [];
      let cursor: string | undefined;
      for (let i = 0; i < MAX_RESYNC_PAGES; i++) {
        const page = await api.get<MessageWithAuthor[]>(
          `/api/v1/rooms/${roomId}/messages?limit=${PAGE_SIZE}` +
            (cursor ? `&before=${encodeURIComponent(cursor)}` : ""),
        );
        collected.push(...page);
        const oldest = page[page.length - 1];
        if (page.length < PAGE_SIZE) break; // reached the start of history
        if (!oldest || oldest.created_at <= lastSeen) break; // overlapped what we hold
        cursor = oldest.created_at;
      }
      if (collected.length === 0) return;

      const currentUserId = useAuthStore.getState().user?.id ?? "";
      const incoming: IncomingMessage[] = collected.map((message) => ({ message }));
      queryClient.setQueryData<MessagesData>(messagesQueryKey(roomId), (old) =>
        upsertMessages(old, incoming, currentUserId),
      );
    },
    [queryClient],
  );

  // ---- Connection lifecycle ----

  useEffect(() => {
    if (!hasHydrated) return; // wait for the localStorage session read
    if (!token) {
      setStatus("closed");
      // Reset presence on logout so a stale "away" (or another user's cached
      // status) can't leak into the next session, which would otherwise get
      // re-asserted on that session's first connect.
      usePresenceStore.getState().clear();
      return; // logged out: no socket
    }

    intentionalCloseRef.current = false;

    function scheduleReconnect() {
      const attempt = reconnectAttemptsRef.current;
      const delay = Math.min(MAX_DELAY_MS, BASE_DELAY_MS * 2 ** attempt);
      const jittered = delay / 2 + Math.random() * (delay / 2);
      reconnectAttemptsRef.current = attempt + 1;
      reconnectTimerRef.current = setTimeout(connect, jittered);
    }

    function connect() {
      setStatus("connecting");
      const ws = new WebSocket(`${WS_URL}?token=${encodeURIComponent(token as string)}`);
      wsRef.current = ws;

      ws.onopen = () => {
        // Guards against a rare overlap: reconnectNow() (or two close events
        // in quick succession) can start a newer attempt before this one's
        // own event fires. Without this check, a stale socket's belated
        // onopen/onclose would clobber wsRef/status/reconnect scheduling out
        // from under the connection that's actually current.
        if (wsRef.current !== ws) return;
        reconnectAttemptsRef.current = 0;
        setStatus("open");
        const isReconnect = hasConnectedOnceRef.current;
        hasConnectedOnceRef.current = true;
        setHasConnectedOnce(true);
        // Re-join every active room. On a reconnect (not the first connect,
        // whose history the initial query already loaded), also backfill.
        joinedRoomsRef.current.forEach((roomId) => {
          ws.send(JSON.stringify({ type: "room.join", room_id: roomId } satisfies ClientEvent));
          if (isReconnect) void resyncRoom(roomId);
        });
        // Re-assert the user's chosen presence. The backend resets status to
        // "online" on every fresh 0→1 connect (internal/store/presence.go's
        // PresenceConnect) and deletes it on disconnect, so an "away" the user
        // picked would otherwise be silently lost on any reconnect. Guarded so
        // the default "online" costs nothing; also covers an "away" chosen
        // before this first open completed (the emit then no-op'd, socket down).
        const selfStatus = usePresenceStore.getState().selfStatus;
        if (selfStatus !== "online") {
          ws.send(JSON.stringify({ type: "presence.update", status: selfStatus } satisfies ClientEvent));
        }
      };

      ws.onmessage = (e) => {
        let event: ServerEvent;
        try {
          event = JSON.parse(e.data as string) as ServerEvent;
        } catch {
          return; // ignore a non-JSON frame
        }
        routeEvent(event);
      };

      ws.onclose = () => {
        if (wsRef.current !== ws) return; // superseded by a newer attempt — see onopen
        wsRef.current = null;
        setStatus("closed");
        if (!intentionalCloseRef.current) scheduleReconnect();
      };

      ws.onerror = () => {
        // onclose fires right after and owns the reconnect; just ensure a close.
        ws.close();
      };
    }

    connectFnRef.current = connect;
    connect();

    return () => {
      intentionalCloseRef.current = true;
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      const ws = wsRef.current;
      if (ws) {
        ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
        ws.close();
      }
      wsRef.current = null;
    };
  }, [token, hasHydrated, routeEvent, resyncRoom]);

  return (
    <WebSocketContext.Provider
      value={{ status, hasConnectedOnce, send, subscribe, joinRoom, leaveRoom, reconnectNow }}
    >
      {children}
    </WebSocketContext.Provider>
  );
}

export function useWebSocket(): WebSocketContextValue {
  const ctx = useContext(WebSocketContext);
  if (!ctx) {
    throw new Error("useWebSocket must be used within a WebSocketProvider");
  }
  return ctx;
}
