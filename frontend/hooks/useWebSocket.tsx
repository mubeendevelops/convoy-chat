"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { api, isAccessTokenExpiringSoon, refreshAccessToken } from "@/lib/api";
import { useAuthStore } from "@/lib/auth-store";
import {
  addReadReceipt,
  applyEdit,
  applyReaction,
  markDeleted,
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
import { useRooms } from "@/hooks/useRooms";
import type { ClientEvent, MessageWithAuthor, Room, ServerEvent } from "@/lib/types";

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
  /** Record which room is currently on screen, so message.new can skip bumping
   * its unread badge (and null it when no room is open). */
  setActiveRoom: (roomId: string | null) => void;
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
  // Reactive rooms list — drives the "stay subscribed to every room" reconcile
  // effect below (deduped against RoomsList's own useRooms by React Query).
  const rooms = useRooms().data;

  const [status, setStatus] = useState<ConnStatus>("connecting");
  const [hasConnectedOnce, setHasConnectedOnce] = useState(false);

  const wsRef = useRef<WebSocket | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const intentionalCloseRef = useRef(false);
  const hasConnectedOnceRef = useRef(false);
  const joinedRoomsRef = useRef<Set<string>>(new Set());
  // The room currently on screen (ChatWindow reports it via setActiveRoom).
  // Read by routeEvent's message.new case to avoid bumping the active room's
  // unread badge — a ref, not state, so updating it never re-renders.
  const activeRoomRef = useRef<string | null>(null);
  // Guards only the pre-socket-creation gap introduced by the proactive
  // refresh below (an async await before `new WebSocket(...)` even runs) —
  // once a socket exists, the existing wsRef.current/readyState checks in
  // reconnectNow/onopen/onclose take over. Without this, reconnectNow()
  // firing while a scheduled reconnect's connect() is mid-refresh could
  // start a second concurrent connect attempt.
  const connectingRef = useRef(false);
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

  // Backfill messages missed while this room wasn't actively joined —
  // reused for two distinct gaps: the socket dropping and reconnecting (see
  // the isReconnect branch in the connect effect below), and a room simply
  // being closed and reopened while the socket stayed up the whole time.
  // The latter is the more common case in practice (switching between DMs/
  // channels) and was the actual missing piece: ChatWindow's join on mount
  // only sent room.join and otherwise relied on useMessages' own
  // useInfiniteQuery fetch, but that query's cache carries a 60s staleTime
  // (see app/providers.tsx), so re-opening a room within that window served
  // the stale cached page with nothing newer — the room's Hub-side
  // subscription was dropped on leave, so no message.new for it arrived
  // while it was closed, and the query had no reason to refetch on remount.
  // Anchored on the newest *confirmed* message we hold; pages backward
  // (reusing keyset pagination) until it overlaps that anchor, then merges —
  // deduped by id, preserving local optimistic/failed bubbles (see
  // upsertMessages). A no-op when nothing is cached yet (a room's first-ever
  // open), since the initial query fetch already covers that case.
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

  const joinRoom = useCallback(
    (roomId: string) => {
      joinedRoomsRef.current.add(roomId);
      send({ type: "room.join", room_id: roomId });
      // Catch up on anything sent while this room was closed (see
      // resyncRoom's comment) — cheap no-op when there's nothing cached yet.
      void resyncRoom(roomId);
    },
    [send, resyncRoom],
  );

  const leaveRoom = useCallback(
    (roomId: string) => {
      joinedRoomsRef.current.delete(roomId);
      send({ type: "room.leave", room_id: roomId });
    },
    [send],
  );

  const setActiveRoom = useCallback((roomId: string | null) => {
    activeRoomRef.current = roomId;
  }, []);

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
          // Live unread badge: a message in a room we're not currently viewing,
          // from someone other than us, bumps that room's cached unread_count.
          // The active room is excluded (it's marked read on open, see
          // ChatWindow/useMarkRoomRead), as are our own sends. Independent of
          // the message-cache write above — the count lives in the ["rooms"]
          // cache and updates even for rooms whose history isn't loaded yet.
          if (roomId !== activeRoomRef.current && event.message.user.id !== currentUserId) {
            queryClient.setQueryData<Room[]>(["rooms"], (old) =>
              old?.map((room) =>
                room.id === roomId ? { ...room, unread_count: (room.unread_count ?? 0) + 1 } : room,
              ),
            );
          }
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
        case "user.left": {
          const selfUserId = useAuthStore.getState().user?.id;
          if (event.user_id === selfUserId) {
            // It's us — either we left (REST leave now also broadcasts this,
            // see CLAUDE.md) or we were just kicked (DELETE .../members/{id}).
            // A REST membership change never touches this socket's own Hub-
            // side room subscription, so without this our client would keep
            // silently receiving that room's traffic until the next
            // reconnect — leaveRoom() both sends the WS room.leave that
            // cleans that up server-side and drops the room from
            // joinedRoomsRef so a future reconnect doesn't rejoin it.
            // **invalidateQueries, not removeQueries**, for room/messages:
            // unlike useLeaveRoom's onSuccess (which removes a cache entry
            // for a page the navigation has *already* swapped away from), a
            // kick can land while ChatWindow/RoomPage for this exact room is
            // still actively mounted and observing — removeQueries alone
            // doesn't reliably kick an active observer into an immediate
            // refetch the way invalidateQueries does, so the stale room
            // content kept rendering until an unrelated remount (caught via
            // real two-client WS verification, not just a type-check pass).
            // The refetch this triggers will 403 (server truth), which
            // RoomPage's existing error branch renders — the actual
            // navigate-away happens via a room-scoped subscribe() listener
            // in ChatWindow, not here (this hook has no router).
            leaveRoom(event.room_id);
            queryClient.invalidateQueries({ queryKey: ["room", event.room_id] });
            queryClient.invalidateQueries({ queryKey: ["messages", event.room_id] });
            queryClient.invalidateQueries({ queryKey: ["rooms"] });
          } else {
            // Mirror of user.joined: a member leaving makes the room's cached
            // members[] stale, so the members sheet, header count, and
            // TypingIndicator's username lookups all keep showing them until
            // a refetch. Invalidate the room detail to drop them live. Fires
            // for a WS room.leave, a dropped connection the hub synthesizes a
            // leave for, a REST leave, or a kick (see CLAUDE.md's WebSocket
            // event contract).
            queryClient.invalidateQueries({ queryKey: ["room", event.room_id] });
          }
          break;
        }
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
        case "message.edited":
          // Unlike message.read_by/message.reaction, this event does carry a
          // room_id (see CLAUDE.md), so it can target that room's cache
          // directly rather than broad-applying across every cached room.
          // Reaches every client watching the room, including the editor's
          // own — their optimistic edit (useEditMessage) already applied the
          // same content, so this is a harmless idempotent re-application,
          // just now with the server's authoritative edited_at.
          queryClient.setQueryData<MessagesData>(messagesQueryKey(event.room_id), (old) =>
            applyEdit(old, event.id, event.content, event.edited_at),
          );
          break;
        case "message.deleted":
          // Carries a room_id (like message.edited), so it targets that room's
          // cache directly rather than broad-applying. Closes the previously
          // documented gap where a delete only reached other clients on their
          // next refetch/resync. markDeleted no-ops if already masked, so the
          // deleter's own client re-applying is harmless.
          queryClient.setQueryData<MessagesData>(messagesQueryKey(event.room_id), (old) =>
            markDeleted(old, event.id, event.deleted_at),
          );
          break;
        case "room.invited":
          // We've just gained active membership in a room we didn't have
          // one in a moment ago (a DM's peer on first creation, a group's
          // initial members, or an explicit invite) — published to our own
          // personal channel (see CLAUDE.md) rather than the room's, since
          // we've never joined that room's channel yet. Refetching ["rooms"]
          // is the only thing needed here: once the new room lands in that
          // list, the reconcile effect below (which watches `rooms`)
          // automatically WS room.joins it and backfills its history via
          // resyncRoom, exactly as if we'd had it all along.
          queryClient.invalidateQueries({ queryKey: ["rooms"] });
          break;
        case "member.role_changed":
          // Same treatment as user.joined/user.left: the room's cached
          // members[] (embedded in RoomDetail) is now stale for one member's
          // role — invalidate to refetch rather than patching in place, since
          // there's no separate members-list cache key to surgically update.
          // Fires for both an explicit promote/demote and a leave-triggered
          // admin-succession auto-promotion.
          queryClient.invalidateQueries({ queryKey: ["room", event.room_id] });
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
    [queryClient, leaveRoom],
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

    // async, but callers never await it — fire-and-forget, same as
    // void resyncRoom(roomId) elsewhere in this file.
    async function connect() {
      if (connectingRef.current) return; // an attempt is already mid-refresh
      connectingRef.current = true;

      // The WS handshake authenticates via ?token= on the raw WebSocket
      // constructor, which never goes through lib/api.ts — so the 401-retry
      // refresh there doesn't cover it. Check proactively: with access
      // tokens now short-lived (15m, see plan.md Phase 3), a backgrounded
      // tab or a laptop waking from sleep would otherwise reconnect with an
      // already-expired token, get rejected pre-upgrade, and retry forever
      // with that same stale value.
      let currentToken = useAuthStore.getState().token;
      if (isAccessTokenExpiringSoon(currentToken)) {
        try {
          currentToken = await refreshAccessToken();
        } catch {
          // No usable session left — refreshAccessToken() already cleared
          // the store, so this effect's own `token` dependency will fire it
          // again with token null, which tears the socket down for good.
          connectingRef.current = false;
          return;
        }
      }
      connectingRef.current = false;

      setStatus("connecting");
      const ws = new WebSocket(`${WS_URL}?token=${encodeURIComponent(currentToken as string)}`);
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

  // Keep the socket subscribed to *every* room the caller belongs to — not just
  // the one on screen — so message.new arrives for all of them and unread
  // badges light up live. Reconciles joinedRoomsRef against the rooms list
  // whenever the socket is open or the list changes: joins rooms not yet
  // joined, leaves rooms that dropped out of the list (a leave/kick). Rooms
  // stay joined for the whole session; the onopen re-join loop already covers
  // reconnects, and joinRoom's own guard means re-running this is a cheap no-op.
  useEffect(() => {
    if (status !== "open" || !rooms) return;
    const desired = new Set(rooms.map((room) => room.id));
    desired.forEach((id) => {
      if (!joinedRoomsRef.current.has(id)) joinRoom(id);
    });
    // Collect first, then leave — mutating joinedRoomsRef while iterating it
    // (leaveRoom deletes from the set) is unsafe.
    const toLeave: string[] = [];
    joinedRoomsRef.current.forEach((id) => {
      if (!desired.has(id)) toLeave.push(id);
    });
    toLeave.forEach((id) => leaveRoom(id));
  }, [status, rooms, joinRoom, leaveRoom]);

  // Memoized so the context value is stable across the provider's own
  // re-renders — it now subscribes to the rooms list (for the reconcile effect
  // above), which changes on every unread bump; without this every useWebSocket
  // consumer would re-render on every incoming message. All the callbacks are
  // stable, so this only changes when status/hasConnectedOnce actually change.
  const value = useMemo(
    () => ({ status, hasConnectedOnce, send, subscribe, joinRoom, leaveRoom, setActiveRoom, reconnectNow }),
    [status, hasConnectedOnce, send, subscribe, joinRoom, leaveRoom, setActiveRoom, reconnectNow],
  );

  return <WebSocketContext.Provider value={value}>{children}</WebSocketContext.Provider>;
}

export function useWebSocket(): WebSocketContextValue {
  const ctx = useContext(WebSocketContext);
  if (!ctx) {
    throw new Error("useWebSocket must be used within a WebSocketProvider");
  }
  return ctx;
}
