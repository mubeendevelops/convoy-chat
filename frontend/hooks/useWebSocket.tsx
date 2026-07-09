"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { useAuthStore } from "@/lib/auth-store";
import {
  addReadReceipt,
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
  /** Send a typed client event. Returns true only if it went out (socket open). */
  send: (event: ClientEvent) => boolean;
  /** Register a listener for one server event type; returns an unsubscribe fn. */
  subscribe: <T extends ServerEventType>(type: T, handler: Handler<T>) => () => void;
  joinRoom: (roomId: string) => void;
  leaveRoom: (roomId: string) => void;
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

  const wsRef = useRef<WebSocket | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const intentionalCloseRef = useRef(false);
  const hasConnectedOnceRef = useRef(false);
  const joinedRoomsRef = useRef<Set<string>>(new Set());
  const subscribersRef = useRef<Map<ServerEventType, Set<(e: ServerEvent) => void>>>(new Map());

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
        reconnectAttemptsRef.current = 0;
        setStatus("open");
        const isReconnect = hasConnectedOnceRef.current;
        hasConnectedOnceRef.current = true;
        // Re-join every active room. On a reconnect (not the first connect,
        // whose history the initial query already loaded), also backfill.
        joinedRoomsRef.current.forEach((roomId) => {
          ws.send(JSON.stringify({ type: "room.join", room_id: roomId } satisfies ClientEvent));
          if (isReconnect) void resyncRoom(roomId);
        });
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
        wsRef.current = null;
        setStatus("closed");
        if (!intentionalCloseRef.current) scheduleReconnect();
      };

      ws.onerror = () => {
        // onclose fires right after and owns the reconnect; just ensure a close.
        ws.close();
      };
    }

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
    <WebSocketContext.Provider value={{ status, send, subscribe, joinRoom, leaveRoom }}>
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
