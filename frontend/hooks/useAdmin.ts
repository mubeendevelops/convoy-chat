"use client";

import { useQuery } from "@tanstack/react-query";

import { api } from "@/lib/api";
import type { AdminPresenceEntry, AdminRoomSummary } from "@/lib/types";

// Every room in the system, regardless of the caller's own membership —
// GET /admin/rooms (system-admin only, Phase 3 post-v1). Plain offset
// pagination on the backend; this hook fetches one page at a time rather
// than an infinite scroll, matching the "low-traffic, human-paged admin
// screen" framing from plan.md's admin-dashboard proposal.
export function useAdminRooms(limit = 50, offset = 0) {
  return useQuery({
    queryKey: ["admin", "rooms", limit, offset],
    queryFn: () => api.get<AdminRoomSummary[]>(`/api/v1/admin/rooms?limit=${limit}&offset=${offset}`),
  });
}

// A system-wide presence snapshot — GET /admin/presence (system-admin only).
// No pagination (see the store-level note in internal/store/presence.go);
// fine at this app's scale.
export function useAdminPresence() {
  return useQuery({
    queryKey: ["admin", "presence"],
    queryFn: () => api.get<AdminPresenceEntry[]>("/api/v1/admin/presence"),
  });
}
