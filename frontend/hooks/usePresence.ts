"use client";

import { useCallback } from "react";

import { useAuthStore } from "@/lib/auth-store";
import { usePresenceStore } from "@/lib/presence-store";
import { useWebSocket } from "@/hooks/useWebSocket";
import type { PresenceStatus } from "@/lib/types";

interface PresenceInfo {
  status: PresenceStatus;
  lastSeenAt?: string;
}

// lib/presence-store.ts only learns another user's status from a live
// user.status_changed/user.joined event received *after* our socket
// connected — there's no REST endpoint to fetch current presence (see
// CLAUDE.md's REST endpoints table), so a member we haven't heard from yet
// has no entry at all, and defaults to "offline" (the same value they'd show
// if we *had* heard from them and they were offline).
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
