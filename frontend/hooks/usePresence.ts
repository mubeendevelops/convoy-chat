"use client";

import { useAuthStore } from "@/lib/auth-store";
import { usePresenceStore } from "@/lib/presence-store";
import type { PresenceStatus } from "@/lib/types";

interface PresenceInfo {
  status: PresenceStatus;
  lastSeenAt?: string;
}

// lib/presence-store.ts only learns a user's status from a live
// user.status_changed/user.joined event received *after* our socket
// connected — there's no REST endpoint to fetch current presence (see
// CLAUDE.md's REST endpoints table), so a member we haven't heard from yet
// has no entry at all. Two defaults fill that gap: the current user is
// always "online" (the UI is live, so they trivially are — and their own
// connect-time broadcast can itself race the room subscription the same way
// Phase 8's join/publish race did, so it isn't safe to assume it round-trips
// back to them), and anyone else with no entry defaults to "offline", the
// same value they'd show if we *had* heard from them and they were offline.
export function usePresence(userId: string | undefined): PresenceInfo {
  const entry = usePresenceStore((s) => (userId ? s.statuses[userId] : undefined));
  const currentUserId = useAuthStore((s) => s.user?.id);

  if (entry) return entry;
  if (userId && userId === currentUserId) return { status: "online" };
  return { status: "offline" };
}
