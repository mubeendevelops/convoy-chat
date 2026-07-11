"use client";

import { useCallback, useEffect } from "react";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { useAuthStore } from "@/lib/auth-store";
import { usePresenceStore } from "@/lib/presence-store";
import { useWebSocket } from "@/hooks/useWebSocket";
import type { PresenceStatus, UserPresence } from "@/lib/types";

interface PresenceInfo {
  status: PresenceStatus;
  lastSeenAt?: string;
}

// lib/presence-store.ts learns another user's status from a live
// user.status_changed/user.joined event received *after* our socket connected,
// or from a room-presence snapshot fetched on room open (useRoomPresence
// below). Without the snapshot, a member who went online before this session's
// socket connected would have no entry at all and default to "offline" — the
// bug this hydration path fixes. A member we still haven't heard from by either
// route defaults to "offline" (the same value they'd show if we *had* heard
// from them and they were offline).
//
// The current user is a special case: their displayed status comes from
// selfStatus (the presence store's authoritative record of what they picked
// in the self-presence control), never from statuses[currentUserId]. The
// server echo for one's own presence can't be trusted here — the backend
// resets it to "online" on every fresh connect and the round-trip races the
// room subscription (the Phase 8 join/publish race) — whereas selfStatus is
// exactly what we intend and re-assert to the server on reconnect. It
// defaults to "online", so a user who's picked nothing still shows online.
export function usePresence(userId: string | undefined): PresenceInfo {
  const entry = usePresenceStore((s) => (userId ? s.statuses[userId] : undefined));
  const selfStatus = usePresenceStore((s) => s.selfStatus);
  const currentUserId = useAuthStore((s) => s.user?.id);

  if (userId && userId === currentUserId) return { status: selfStatus };
  if (entry) return entry;
  return { status: "offline" };
}

// The self-presence control: reads the current user's chosen status and sets
// it. Setting both records the intent (so their own dot updates instantly and
// survives a reconnect) and emits presence.update over the socket. The emit
// can no-op if the socket is momentarily down — that's fine, the stored intent
// is re-asserted from useWebSocket's onopen once it (re)connects.
export function useSelfPresence(): { selfStatus: PresenceStatus; setSelfStatus: (status: PresenceStatus) => void } {
  const selfStatus = usePresenceStore((s) => s.selfStatus);
  const setSelfStatusState = usePresenceStore((s) => s.setSelfStatus);
  const { send } = useWebSocket();

  const setSelfStatus = useCallback(
    (status: PresenceStatus) => {
      setSelfStatusState(status);
      send({ type: "presence.update", status });
    },
    [send, setSelfStatusState],
  );

  return { selfStatus, setSelfStatus };
}

// Hydrates the presence store with a room's members' current statuses when the
// room opens, via GET /rooms/{id}/presence. This is the authoritative snapshot
// at open time; live WS events continue to update the store afterward
// (setStatus is last-write-wins). A benign race exists — a live event landing
// between the fetch starting and its result being applied could be briefly
// overwritten by the (now slightly stale) snapshot — but the next live event
// corrects it, and this only matters in the sub-second window around room open.
// Meant to be mounted once per room (e.g. ChatWindow, keyed on room.id).
export function useRoomPresence(roomId: string | undefined): void {
  const setStatus = usePresenceStore((s) => s.setStatus);
  const currentUserId = useAuthStore((s) => s.user?.id);

  const { data } = useQuery({
    queryKey: ["room-presence", roomId],
    queryFn: () => api.get<UserPresence[]>(`/api/v1/rooms/${roomId}/presence`),
    enabled: !!roomId,
    // Presence is high-churn and the live socket keeps it fresh after this
    // seed; refetching on every focus/remount would just add noise.
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });

  useEffect(() => {
    if (!data) return;
    for (const entry of data) {
      // Never let the snapshot override the current user's own dot — that's
      // driven by selfStatus (their chosen intent), not the server echo, which
      // the backend resets to "online" on every connect (see usePresence).
      if (entry.user_id === currentUserId) continue;
      setStatus(entry.user_id, entry.status, entry.last_seen_at);
    }
  }, [data, currentUserId, setStatus]);
}
